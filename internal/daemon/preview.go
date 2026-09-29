package daemon

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif" // registered for DecodeConfig and Decode
	"image/jpeg"
	"image/png"
	"io"
)

// Images an agent wrote are shown on the phone by sending them inline, base64
// encoded, in one message through the relay. That path has three limits, and
// the preview has to fit all of them:
//
//	the phone's check on the image field     3 MiB of base64
//	the relay's frame from the daemon        4 MiB
//	the relay's queue toward the phone       8 MiB
//
// maxImageFile is the most that fits: 2 MiB becomes 2.7 MiB once encoded.
// Raising it on its own does not show bigger images, it breaks the connection
// — a frame over the relay's limit makes it close the daemon's socket, taking
// every session offline rather than failing one preview.
//
// So a large image is made smaller rather than refused. A full-resolution
// Retina screenshot is routinely 3–5 MB, and was the common case for the
// refusal; resized to fit a phone it is a few hundred kilobytes, which also
// matters more than the limit does on a cellular connection. The phone does
// the same in the other direction before it uploads anything.
const (
	// maxImageFile is what one preview may weigh once encoded, before base64.
	maxImageFile = 2 << 20
	// maxImageInput is the largest file read at all. Past it the work of
	// decoding is not worth doing for a preview.
	maxImageInput = 32 << 20
	// maxImagePixels is checked from the header before anything is decoded. A
	// small file can declare an enormous image, and decoding it would allocate
	// four bytes for every pixel it claims. 40 megapixels is more than a
	// full-page capture of a 6K display.
	maxImagePixels = 40_000_000
	// previewMaxDimension is the longest edge a preview is sent at. Larger than
	// any phone screen, so a pinch-zoom still has somewhere to go.
	previewMaxDimension = 2048
	// previewJPEGQuality keeps text in a screenshot crisp; it is the text that
	// people open these to read.
	previewJPEGQuality = 85
)

var (
	errPreviewTooLarge    = errors.New("image is too large to preview (32 MiB limit)")
	errPreviewTooManyPx   = errors.New("image is too large to preview (over 40 megapixels)")
	errPreviewUnresizable = errors.New("image is too large to preview, and this format cannot be resized here")
)

// previewImage returns the bytes to show for an image, and their type.
//
// An image that already fits on a screen and under the limit is returned
// untouched, byte for byte, so nothing that previewed before looks any
// different now. Everything else is resized to fit previewMaxDimension and
// re-encoded: as JPEG when it has no transparency, since that is several
// times smaller for a screenshot, and as PNG when it does, so a transparent
// icon keeps its edges.
func previewImage(r io.Reader, mime string) ([]byte, string, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxImageInput+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxImageInput {
		return nil, "", errPreviewTooLarge
	}

	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	decodable := err == nil
	if decodable && int64(config.Width)*int64(config.Height) > maxImagePixels {
		return nil, "", errPreviewTooManyPx
	}

	fits := len(data) <= maxImageFile
	onScreen := decodable && max(config.Width, config.Height) <= previewMaxDimension
	// A GIF that fits is always passed through: re-encoding one keeps only its
	// first frame, and an animation stopping is a worse surprise than a large
	// preview. WebP is passed through when it fits because the standard
	// library cannot decode it to resize it.
	if fits && (onScreen || !decodable || format == "gif") {
		return data, mime, nil
	}
	if !decodable {
		return nil, "", errPreviewUnresizable
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", err
	}
	// Almost always the first size is enough. The smaller ones are for the
	// image that stays heavy even at phone size — dense noise, or a
	// transparent PNG — rather than failing it outright.
	for _, dimension := range []int{previewMaxDimension, 1536, 1024} {
		out, outMime, err := encodePreview(fitImage(img, dimension))
		if err == nil && len(out) <= maxImageFile {
			return out, outMime, nil
		}
	}
	return nil, "", errors.New("image is too large to preview even after resizing")
}

// fitImage scales an image so its longest edge is at most dimension.
func fitImage(img image.Image, dimension int) *image.RGBA {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	// Drawn into a plain RGBA first, whatever it was: paletted, CMYK, 16-bit.
	// That is also premultiplied alpha, which is what averaging needs — mixing
	// a transparent pixel into an opaque one otherwise darkens the edge.
	source := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(source, source.Bounds(), img, bounds.Min, draw.Src)

	longest := max(width, height)
	if longest <= dimension {
		return source
	}
	scaledWidth := max(1, width*dimension/longest)
	scaledHeight := max(1, height*dimension/longest)
	return resizeArea(source, scaledWidth, scaledHeight)
}

// resizeArea shrinks an image by area averaging: each output pixel is the
// mean of the source pixels it covers, weighted by how much of each it covers.
//
// That is the right filter for shrinking, and particularly for screenshots.
// Sampling methods pick a few source pixels per output pixel and skip the
// rest, which drops the thin strokes text is made of; averaging keeps every
// source pixel's contribution. Done as two one-dimensional passes, which is
// the same result as the two-dimensional average and far less work.
func resizeArea(source *image.RGBA, width, height int) *image.RGBA {
	sourceWidth, sourceHeight := source.Bounds().Dx(), source.Bounds().Dy()

	// Across each row first, into an image as tall as the source.
	across := image.NewRGBA(image.Rect(0, 0, width, sourceHeight))
	columns := areaWeights(sourceWidth, width)
	for y := 0; y < sourceHeight; y++ {
		in := source.Pix[y*source.Stride:]
		out := across.Pix[y*across.Stride:]
		for x, taps := range columns {
			blend(out[x*4:x*4+4], in, taps, 4)
		}
	}

	// Then down each column.
	scaled := image.NewRGBA(image.Rect(0, 0, width, height))
	rows := areaWeights(sourceHeight, height)
	for y, taps := range rows {
		out := scaled.Pix[y*scaled.Stride:]
		for x := 0; x < width; x++ {
			blend(out[x*4:x*4+4], across.Pix[x*4:], taps, across.Stride)
		}
	}
	return scaled
}

// tap is one source pixel's share of an output pixel.
type tap struct {
	index  int
	weight float32
}

// areaWeights works out, for each of to output positions, which of from
// source positions it covers and by how much. Each set of weights sums to one.
func areaWeights(from, to int) [][]tap {
	scale := float64(from) / float64(to)
	weights := make([][]tap, to)
	for i := range weights {
		start := float64(i) * scale
		end := start + scale
		for j := int(start); j < from && float64(j) < end; j++ {
			overlap := min(end, float64(j+1)) - max(start, float64(j))
			if overlap > 0 {
				weights[i] = append(weights[i], tap{j, float32(overlap / scale)})
			}
		}
	}
	return weights
}

// blend writes the weighted sum of pixels into out. stride is the distance
// between successive pixels along the axis being averaged.
func blend(out, in []byte, taps []tap, stride int) {
	var r, g, b, a float32
	for _, t := range taps {
		p := in[t.index*stride:]
		r += float32(p[0]) * t.weight
		g += float32(p[1]) * t.weight
		b += float32(p[2]) * t.weight
		a += float32(p[3]) * t.weight
	}
	out[0], out[1], out[2], out[3] = channel(r), channel(g), channel(b), channel(a)
}

func channel(value float32) byte {
	switch {
	case value <= 0:
		return 0
	case value >= 255:
		return 255
	default:
		return byte(value + 0.5)
	}
}

// encodePreview picks the format that suits the image.
func encodePreview(img *image.RGBA) ([]byte, string, error) {
	var out bytes.Buffer
	if img.Opaque() {
		err := jpeg.Encode(&out, img, &jpeg.Options{Quality: previewJPEGQuality})
		return out.Bytes(), "image/jpeg", err
	}
	if err := png.Encode(&out, img); err != nil {
		return nil, "", err
	}
	if out.Len() <= maxImageFile {
		return out.Bytes(), "image/png", nil
	}
	// Transparency is worth keeping only while it still fits. Past that, lay
	// it on white and send a JPEG: a preview beats a refusal. White rather
	// than the viewer's black, because a transparent image is most often dark
	// marks on nothing — a logo, an icon, a diagram — and black would erase
	// exactly those. A white box on a black viewer is the cheaper mistake.
	flat := image.NewRGBA(img.Bounds())
	draw.Draw(flat, flat.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), img, image.Point{}, draw.Over)
	out.Reset()
	err := jpeg.Encode(&out, flat, &jpeg.Options{Quality: previewJPEGQuality})
	return out.Bytes(), "image/jpeg", err
}
