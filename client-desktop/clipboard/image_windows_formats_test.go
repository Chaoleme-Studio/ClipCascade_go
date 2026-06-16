package clipboard

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestEncodeWindowsImageClipboardFormatsAcceptsJPEG(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(40 + x*20), G: uint8(80 + y*30), B: 160, A: 255})
		}
	}

	var jpegData bytes.Buffer
	if err := jpeg.Encode(&jpegData, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}

	formats, err := encodeWindowsImageClipboardFormats(jpegData.Bytes())
	if err != nil {
		t.Fatalf("encode clipboard formats from jpeg: %v", err)
	}
	if len(formats.dib) == 0 || len(formats.dibV5) == 0 || len(formats.png) == 0 {
		t.Fatalf("expected all formats to be populated: dib=%d dibV5=%d png=%d", len(formats.dib), len(formats.dibV5), len(formats.png))
	}

	dib, err := decodeDIB(formats.dib)
	if err != nil {
		t.Fatalf("decode dib: %v", err)
	}
	if got := dib.Bounds().Size(); got.X != img.Bounds().Dx() || got.Y != img.Bounds().Dy() {
		t.Fatalf("unexpected dib size: got %v want %v", got, img.Bounds().Size())
	}

	dibV5, err := decodeDIB(formats.dibV5)
	if err != nil {
		t.Fatalf("decode dibV5: %v", err)
	}
	if got := dibV5.Bounds().Size(); got.X != img.Bounds().Dx() || got.Y != img.Bounds().Dy() {
		t.Fatalf("unexpected dibV5 size: got %v want %v", got, img.Bounds().Size())
	}

	if _, err := png.Decode(bytes.NewReader(formats.png)); err != nil {
		t.Fatalf("decode normalized png: %v", err)
	}
}
