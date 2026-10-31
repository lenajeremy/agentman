package daemon

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
)

// Windows icons (.ico): a site's favicon is usually one. Go has no decoder
// for them, so this reads one far enough to show it: the largest picture in
// the file, which is either a PNG kept whole or a bitmap without its file
// header, its rows bottom up, followed by a one-bit transparency mask.

var errNotIcon = errors.New("not a readable icon")

// iconPNG returns the largest picture in an .ico file as a PNG.
func iconPNG(data []byte) ([]byte, error) {
	if len(data) < 6 || binary.LittleEndian.Uint16(data[0:]) != 0 || binary.LittleEndian.Uint16(data[2:]) != 1 {
		return nil, errNotIcon
	}
	count := int(binary.LittleEndian.Uint16(data[4:]))
	if count == 0 || len(data) < 6+16*count {
		return nil, errNotIcon
	}
	best, bestArea, bestBits := -1, -1, -1
	for i := range count {
		entry := data[6+16*i:]
		width, height := int(entry[0]), int(entry[1])
		if width == 0 {
			width = 256
		}
		if height == 0 {
			height = 256
		}
		bits := int(binary.LittleEndian.Uint16(entry[6:]))
		if area := width * height; area > bestArea || (area == bestArea && bits > bestBits) {
			best, bestArea, bestBits = i, area, bits
		}
	}
	entry := data[6+16*best:]
	size := int(binary.LittleEndian.Uint32(entry[8:]))
	offset := int(binary.LittleEndian.Uint32(entry[12:]))
	if offset < 0 || size <= 0 || offset > len(data) || size > len(data)-offset {
		return nil, errNotIcon
	}
	picture := data[offset : offset+size]
	if bytes.HasPrefix(picture, []byte("\x89PNG\r\n\x1a\n")) {
		return picture, nil
	}
	img, err := iconBitmap(picture)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// iconBitmap reads an icon's bitmap: 1, 4 or 8 bits through a palette, or
// 24 or 32 bits of colour, then its transparency mask.
func iconBitmap(b []byte) (image.Image, error) {
	if len(b) < 40 {
		return nil, errNotIcon
	}
	header := int(binary.LittleEndian.Uint32(b[0:]))
	width := int(int32(binary.LittleEndian.Uint32(b[4:])))
	// The height counts the picture and its mask together.
	height := int(int32(binary.LittleEndian.Uint32(b[8:]))) / 2
	bits := int(binary.LittleEndian.Uint16(b[14:]))
	compression := binary.LittleEndian.Uint32(b[16:])
	used := int(binary.LittleEndian.Uint32(b[32:]))
	if header < 40 || header > len(b) || width <= 0 || height <= 0 || width > 1024 || height > 1024 || compression != 0 {
		return nil, errNotIcon
	}
	var palette []color.NRGBA
	at := header
	if bits <= 8 {
		entries := used
		if entries == 0 {
			entries = 1 << bits
		}
		if at+4*entries > len(b) {
			return nil, errNotIcon
		}
		for i := range entries {
			p := b[at+4*i:]
			palette = append(palette, color.NRGBA{R: p[2], G: p[1], B: p[0], A: 255})
		}
		at += 4 * entries
	}
	switch bits {
	case 1, 4, 8, 24, 32:
	default:
		return nil, errNotIcon
	}
	stride := (width*bits + 31) / 32 * 4
	maskStride := (width + 31) / 32 * 4
	if at+stride*height > len(b) {
		return nil, errNotIcon
	}
	mask := at + stride*height
	hasMask := mask+maskStride*height <= len(b)

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	anyAlpha := false
	for y := range height {
		row := b[at+stride*(height-1-y):]
		for x := range width {
			var c color.NRGBA
			switch bits {
			case 32:
				p := row[4*x:]
				c = color.NRGBA{R: p[2], G: p[1], B: p[0], A: p[3]}
				if p[3] != 0 {
					anyAlpha = true
				}
			case 24:
				p := row[3*x:]
				c = color.NRGBA{R: p[2], G: p[1], B: p[0], A: 255}
			default:
				perByte := 8 / bits
				shift := uint(8 - bits*(x%perByte+1))
				index := int(row[x/perByte]>>shift) & (1<<bits - 1)
				if index < len(palette) {
					c = palette[index]
				}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	// A 32-bit picture carries its own transparency; older ones, and a
	// 32-bit one whose alpha is all zero, take it from the mask.
	if hasMask && (bits != 32 || !anyAlpha) {
		for y := range height {
			row := b[mask+maskStride*(height-1-y):]
			for x := range width {
				c := img.NRGBAAt(x, y)
				if row[x/8]>>(7-uint(x%8))&1 == 1 {
					c.A = 0
				} else {
					c.A = 255
				}
				img.SetNRGBA(x, y, c)
			}
		}
	}
	return img, nil
}
