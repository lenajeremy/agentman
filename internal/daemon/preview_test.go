package daemon

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"math/rand"
	"testing"
	"time"
)

// retinaScreenshot draws what an agent's screenshot mostly is: flat ground
// with dense one-pixel strokes. Encoded as PNG at the size a Retina MacBook
// captures, it lands in the 3–5 MB range that the old 2 MiB limit refused.
func retinaScreenshot(t *testing.T) []byte {
	t.Helper()
	const width, height = 3024, 1964
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	rng := rand.New(rand.NewSource(1))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			// Mostly paper, with "text": short dark runs on alternate rows,
			// plus a little noise so it does not compress to nothing.
			v := uint8(245)
			if y%6 < 2 && rng.Intn(3) == 0 {
				v = 20
			}
			v -= uint8(rng.Intn(8))
			img.Pix[y*img.Stride+x*4+0] = v
			img.Pix[y*img.Stride+x*4+1] = v
			img.Pix[y*img.Stride+x*4+2] = v
			img.Pix[y*img.Stride+x*4+3] = 255
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// The case this exists for.
func TestPreviewShrinksARetinaScreenshotInsteadOfRefusingIt(t *testing.T) {
	source := retinaScreenshot(t)
	if len(source) <= maxImageFile {
		t.Fatalf("fixture is %d bytes; it must be over the %d-byte limit to test anything", len(source), maxImageFile)
	}

	start := time.Now()
	out, mime, err := previewImage(bytes.NewReader(source), "image/png")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("refused a %.1f MB screenshot: %v", float64(len(source))/(1<<20), err)
	}
	if len(out) > maxImageFile {
		t.Errorf("preview is %d bytes, over the %d the relay path can carry", len(out), maxImageFile)
	}
	if mime != "image/jpeg" {
		t.Errorf("mime = %q; an opaque screenshot should become a JPEG", mime)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("preview does not decode: %v", err)
	}
	if max(config.Width, config.Height) != previewMaxDimension {
		t.Errorf("longest edge = %d, want %d", max(config.Width, config.Height), previewMaxDimension)
	}
	// The aspect ratio survives, to within a pixel of rounding.
	if want := 1964 * previewMaxDimension / 3024; abs(config.Height-want) > 1 {
		t.Errorf("height = %d, want %d: the aspect ratio moved", config.Height, want)
	}
	t.Logf("%.1f MB → %.0f KB, %dx%d, %v", float64(len(source))/(1<<20),
		float64(len(out))/1024, config.Width, config.Height, elapsed.Round(time.Millisecond))
}

// Everything that previewed before must look exactly as it did.
func TestPreviewPassesASmallImageThroughUntouched(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 800, 600))
	for i := range img.Pix {
		img.Pix[i] = byte(i)
	}
	var source bytes.Buffer
	if err := png.Encode(&source, img); err != nil {
		t.Fatal(err)
	}
	out, mime, err := previewImage(bytes.NewReader(source.Bytes()), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, source.Bytes()) || mime != "image/png" {
		t.Error("an image that already fit was re-encoded; it should be sent byte for byte")
	}
}

// The reason for averaging rather than sampling. Text is made of strokes one
// pixel wide, and a sampler that skips source pixels skips those strokes.
func TestPreviewKeepsOnePixelStrokes(t *testing.T) {
	const width, height = 3000, 400
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	// Vertical one-pixel lines, spaced so a 1.46× shrink would miss many of
	// them if it picked one source column per output column.
	for x := 7; x < width; x += 31 {
		for y := 0; y < height; y++ {
			o := y*img.Stride + x*4
			img.Pix[o], img.Pix[o+1], img.Pix[o+2] = 0, 0, 0
		}
	}
	scaled := fitImage(img, previewMaxDimension)
	lines := 0
	for x := 7; x < width; x += 31 {
		// Where that line landed, and whether it is still visibly darker.
		dx := x * scaled.Bounds().Dx() / width
		darkest := byte(255)
		for _, c := range []int{dx - 1, dx, dx + 1} {
			if c >= 0 && c < scaled.Bounds().Dx() {
				darkest = min(darkest, scaled.Pix[200*scaled.Stride+c*4])
			}
		}
		if darkest < 200 {
			lines++
		}
	}
	total := (width - 7 + 30) / 31
	if lines != total {
		t.Errorf("%d of %d one-pixel strokes survived the shrink; averaging keeps every one", lines, total)
	}
}

// A small file can declare an enormous image. The header is checked before
// any pixel is decoded, so this costs nothing but reading a few bytes.
func TestPreviewRefusesADecompressionBomb(t *testing.T) {
	bomb := pngHeaderOnly(100_000, 100_000)
	if len(bomb) > 100 {
		t.Fatalf("fixture is %d bytes; it should be tiny", len(bomb))
	}
	_, _, err := previewImage(bytes.NewReader(bomb), "image/png")
	if err != errPreviewTooManyPx {
		t.Fatalf("err = %v, want %v", err, errPreviewTooManyPx)
	}
}

// pngHeaderOnly is a PNG signature and a valid IHDR declaring the given size,
// with no pixel data behind it.
func pngHeaderOnly(width, height uint32) []byte {
	var ihdr bytes.Buffer
	ihdr.WriteString("IHDR")
	_ = binary.Write(&ihdr, binary.BigEndian, width)
	_ = binary.Write(&ihdr, binary.BigEndian, height)
	ihdr.Write([]byte{8, 6, 0, 0, 0}) // 8-bit RGBA
	var out bytes.Buffer
	out.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	_ = binary.Write(&out, binary.BigEndian, uint32(ihdr.Len()-4))
	out.Write(ihdr.Bytes())
	_ = binary.Write(&out, binary.BigEndian, crc32.ChecksumIEEE(ihdr.Bytes()))
	return out.Bytes()
}

// Re-encoding a GIF keeps its first frame only, so one that fits is never
// touched — however large its dimensions — rather than silently stopping.
func TestPreviewLeavesAnAnimatedGIFAlone(t *testing.T) {
	palette := color.Palette{color.White, color.Black}
	frames := &gif.GIF{}
	for i := 0; i < 3; i++ {
		frame := image.NewPaletted(image.Rect(0, 0, 3000, 200), palette)
		frame.SetColorIndex(i, 0, 1)
		frames.Image = append(frames.Image, frame)
		frames.Delay = append(frames.Delay, 10)
	}
	var source bytes.Buffer
	if err := gif.EncodeAll(&source, frames); err != nil {
		t.Fatal(err)
	}
	out, mime, err := previewImage(bytes.NewReader(source.Bytes()), "image/gif")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, source.Bytes()) || mime != "image/gif" {
		t.Error("an animated GIF that fit was re-encoded, which would have stopped it")
	}
}

// Transparency is kept when the result still fits, so an icon keeps its
// edges instead of arriving on a box.
func TestPreviewKeepsTransparency(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 3000, 3000))
	for y := 1000; y < 2000; y++ {
		for x := 1000; x < 2000; x++ {
			img.Set(x, y, color.NRGBA{20, 40, 200, 255})
		}
	}
	var source bytes.Buffer
	if err := png.Encode(&source, img); err != nil {
		t.Fatal(err)
	}
	out, mime, err := previewImage(bytes.NewReader(source.Bytes()), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if mime != "image/png" {
		t.Fatalf("mime = %q; a transparent image that fits should stay PNG", mime)
	}
	decoded, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, a := decoded.At(0, 0).RGBA(); a != 0 {
		t.Error("the transparent corner came back opaque")
	}
}

// The worst case for size: noise does not compress, so even at phone size it
// can be over the limit. It steps down rather than failing.
func TestPreviewStepsDownForAnImageThatStaysHeavy(t *testing.T) {
	const width, height = 3000, 3000
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	rand.New(rand.NewSource(2)).Read(img.Pix)
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	var source bytes.Buffer
	if err := png.Encode(&source, img); err != nil {
		t.Fatal(err)
	}
	out, _, err := previewImage(bytes.NewReader(source.Bytes()), "image/png")
	if err != nil {
		t.Fatalf("gave up on a noisy image: %v", err)
	}
	if len(out) > maxImageFile {
		t.Errorf("preview is %d bytes, over the limit", len(out))
	}
}

// WebP is previewable but the standard library cannot decode it, so one that
// is over the limit cannot be shrunk. It says so rather than sending
// something the relay would drop.
func TestPreviewRefusesAWebPItCannotResize(t *testing.T) {
	large := append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), make([]byte, maxImageFile)...)
	if _, _, err := previewImage(bytes.NewReader(large), "image/webp"); err != errPreviewUnresizable {
		t.Fatalf("err = %v, want %v", err, errPreviewUnresizable)
	}
	small := append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), make([]byte, 1024)...)
	out, mime, err := previewImage(bytes.NewReader(small), "image/webp")
	if err != nil || mime != "image/webp" || !bytes.Equal(out, small) {
		t.Errorf("a WebP that fits should pass through untouched: err=%v mime=%q", err, mime)
	}
}

func TestPreviewRefusesAnEnormousFileWithoutReadingItAll(t *testing.T) {
	huge := bytes.NewReader(make([]byte, maxImageInput+1))
	if _, _, err := previewImage(huge, "image/png"); err != errPreviewTooLarge {
		t.Fatalf("err = %v, want %v", err, errPreviewTooLarge)
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
