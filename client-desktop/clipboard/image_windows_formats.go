package clipboard

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"syscall"

	_ "golang.org/x/image/webp"
)

const (
	dibHeaderSize   = 40
	dibV5HeaderSize = 124

	biRGB       = 0
	biBitfields = 3
)

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type windowsImageClipboardFormats struct {
	dib   []byte
	dibV5 []byte
	png   []byte
}

func encodeWindowsImageClipboardFormats(data []byte) (windowsImageClipboardFormats, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return windowsImageClipboardFormats{}, err
	}

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	if width <= 0 || height <= 0 {
		return windowsImageClipboardFormats{}, fmt.Errorf("invalid image dimensions %dx%d", width, height)
	}

	nrgba := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.Draw(nrgba, nrgba.Bounds(), img, bounds.Min, draw.Src)

	var pngOut bytes.Buffer
	if err := png.Encode(&pngOut, nrgba); err != nil {
		return windowsImageClipboardFormats{}, err
	}

	return windowsImageClipboardFormats{
		dib:   encodeNRGBAToDIB(nrgba),
		dibV5: encodeNRGBAToDIBV5(nrgba),
		png:   pngOut.Bytes(),
	}, nil
}

func encodeNRGBAToDIB(img *image.NRGBA) []byte {
	width := img.Bounds().Dx()
	height := img.Bounds().Dy()
	out := make([]byte, dibHeaderSize+width*height*4)

	binary.LittleEndian.PutUint32(out[0:4], dibHeaderSize)
	binary.LittleEndian.PutUint32(out[4:8], uint32(width))
	binary.LittleEndian.PutUint32(out[8:12], uint32(height))
	binary.LittleEndian.PutUint16(out[12:14], 1)
	binary.LittleEndian.PutUint16(out[14:16], 32)
	binary.LittleEndian.PutUint32(out[16:20], biRGB)
	binary.LittleEndian.PutUint32(out[20:24], uint32(width*height*4))
	writeBottomUpBGRA(out[dibHeaderSize:], img)
	return out
}

func encodeNRGBAToDIBV5(img *image.NRGBA) []byte {
	width := img.Bounds().Dx()
	height := img.Bounds().Dy()
	out := make([]byte, dibV5HeaderSize+width*height*4)

	binary.LittleEndian.PutUint32(out[0:4], dibV5HeaderSize)
	binary.LittleEndian.PutUint32(out[4:8], uint32(width))
	binary.LittleEndian.PutUint32(out[8:12], uint32(height))
	binary.LittleEndian.PutUint16(out[12:14], 1)
	binary.LittleEndian.PutUint16(out[14:16], 32)
	binary.LittleEndian.PutUint32(out[16:20], biBitfields)
	binary.LittleEndian.PutUint32(out[20:24], uint32(width*height*4))
	binary.LittleEndian.PutUint32(out[40:44], 0x00ff0000)
	binary.LittleEndian.PutUint32(out[44:48], 0x0000ff00)
	binary.LittleEndian.PutUint32(out[48:52], 0x000000ff)
	binary.LittleEndian.PutUint32(out[52:56], 0xff000000)
	binary.LittleEndian.PutUint32(out[56:60], 0x73524742) // sRGB
	binary.LittleEndian.PutUint32(out[108:112], 4)        // LCS_GM_IMAGES
	writeBottomUpBGRA(out[dibV5HeaderSize:], img)
	return out
}

func writeBottomUpBGRA(dst []byte, img *image.NRGBA) {
	width := img.Bounds().Dx()
	height := img.Bounds().Dy()
	for y := 0; y < height; y++ {
		srcY := height - 1 - y
		for x := 0; x < width; x++ {
			c := img.NRGBAAt(x, srcY)
			i := (y*width + x) * 4
			dst[i+0] = c.B
			dst[i+1] = c.G
			dst[i+2] = c.R
			dst[i+3] = c.A
		}
	}
}

func decodeDIB(data []byte) (*image.NRGBA, error) {
	if len(data) < dibHeaderSize {
		return nil, syscall.EINVAL
	}

	var hdr bitmapInfoHeader
	hdr.Size = binary.LittleEndian.Uint32(data[0:4])
	hdr.Width = int32(binary.LittleEndian.Uint32(data[4:8]))
	hdr.Height = int32(binary.LittleEndian.Uint32(data[8:12]))
	hdr.Planes = binary.LittleEndian.Uint16(data[12:14])
	hdr.BitCount = binary.LittleEndian.Uint16(data[14:16])
	hdr.Compression = binary.LittleEndian.Uint32(data[16:20])
	hdr.SizeImage = binary.LittleEndian.Uint32(data[20:24])
	hdr.ClrUsed = binary.LittleEndian.Uint32(data[32:36])

	if hdr.Planes != 1 || hdr.Width == 0 || hdr.Height == 0 || hdr.Size < dibHeaderSize {
		return nil, syscall.EINVAL
	}
	if int(hdr.Size) > len(data) {
		return nil, syscall.EINVAL
	}

	width := int(hdr.Width)
	height := int(hdr.Height)
	topDown := false
	if height < 0 {
		topDown = true
		height = -height
	}

	offset := int(hdr.Size)
	switch hdr.BitCount {
	case 32:
		if hdr.Compression != biRGB && hdr.Compression != biBitfields {
			return nil, syscall.EINVAL
		}
		if hdr.Compression == biBitfields && hdr.Size == dibHeaderSize {
			offset += 12
		}
	case 24:
		if hdr.Compression != biRGB {
			return nil, syscall.EINVAL
		}
	default:
		return nil, syscall.EINVAL
	}

	if offset >= len(data) {
		return nil, syscall.EINVAL
	}

	rowStride := ((width*int(hdr.BitCount) + 31) / 32) * 4
	required := offset + rowStride*height
	if required > len(data) {
		return nil, syscall.EINVAL
	}

	hasExplicitAlpha := false
	if hdr.BitCount == 32 {
		hasExplicitAlpha = hdr.Compression == biBitfields
		if !hasExplicitAlpha && hdr.Size >= 56 && len(data) >= 56 {
			hasExplicitAlpha = binary.LittleEndian.Uint32(data[52:56]) != 0
		}
	}

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		srcY := y
		if !topDown {
			srcY = height - 1 - y
		}
		row := data[offset+srcY*rowStride : offset+(srcY+1)*rowStride]
		for x := 0; x < width; x++ {
			switch hdr.BitCount {
			case 32:
				i := x * 4
				b, g, r, a := row[i], row[i+1], row[i+2], row[i+3]
				if a == 0 && !hasExplicitAlpha {
					a = 255
				}
				img.SetNRGBA(x, y, color.NRGBA{R: r, G: g, B: b, A: a})
			case 24:
				i := x * 3
				b, g, r := row[i], row[i+1], row[i+2]
				img.SetNRGBA(x, y, color.NRGBA{R: r, G: g, B: b, A: 255})
			}
		}
	}

	return img, nil
}
