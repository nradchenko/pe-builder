// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package tree

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// sampleJPEG is a smooth blue-to-white gradient — the shape a wallpaper mostly is, and the one
// that shows quantisation first. Built rather than committed so the test carries no binary.
func sampleJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := uint8(255 * y / h)
			img.Set(x, y, color.RGBA{v, 128 + v/2, 235, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// bmpHeader reads back the fields a wallpaper depends on. The classic path takes bitmaps only,
// and only some bitmaps: the depth and compression are the whole contract with it.
func bmpHeader(t *testing.T, b []byte) (w, h int32, bpp uint16, compression uint32) {
	t.Helper()
	if len(b) < 54 || string(b[:2]) != "BM" {
		t.Fatalf("not a BMP: %d bytes, magic %q", len(b), b[:min(2, len(b))])
	}
	w = int32(binary.LittleEndian.Uint32(b[18:]))
	h = int32(binary.LittleEndian.Uint32(b[22:]))
	bpp = binary.LittleEndian.Uint16(b[28:])
	compression = binary.LittleEndian.Uint32(b[30:])
	return
}

// The contract with the wallpaper path: 8-bit, uncompressed, same dimensions as the source.
// A 24-bit or RLE-compressed result would be a different file format decision, not a detail.
func TestToBitmapProducesAnEightBitBMP(t *testing.T) {
	out, err := ToBitmap(sampleJPEG(t, 64, 48), "sample.jpg")
	if err != nil {
		t.Fatal(err)
	}
	w, h, bpp, comp := bmpHeader(t, out)
	if w != 64 || h != 48 {
		t.Errorf("dimensions = %dx%d, want 64x48 — the image must not be rescaled", w, h)
	}
	if bpp != 8 {
		t.Errorf("bpp = %d, want 8", bpp)
	}
	if comp != 0 {
		t.Errorf("compression = %d, want 0 (BI_RGB) — RLE is not verified in the wallpaper path", comp)
	}
}

// A build must be reproducible: the same source has to give the same bytes, or a rebuild is not
// comparable and a receipt says nothing. Quantisers are where this goes wrong — a palette search
// that iterates a map, or dithers with a seeded error term, varies per run.
func TestToBitmapIsDeterministic(t *testing.T) {
	src := sampleJPEG(t, 64, 48)
	first, err := ToBitmap(src, "sample.jpg")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		again, err := ToBitmap(src, "sample.jpg")
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, again) {
			t.Fatalf("run %d differs from the first: the conversion is not deterministic", i+2)
		}
	}
}

// A source that already ships a bitmap needs nothing done to it, and re-encoding one would
// quantise colours a second time for no gain.
func TestToBitmapPassesABitmapThrough(t *testing.T) {
	once, err := ToBitmap(sampleJPEG(t, 32, 32), "sample.jpg")
	if err != nil {
		t.Fatal(err)
	}
	twice, err := ToBitmap(once, "sample.bmp")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(once, twice) {
		t.Errorf("a BMP was re-encoded; it should be returned unchanged")
	}
}

// The failure has to name the file. A copy action that cannot be converted stops the build, and
// "decode: invalid format" without a name is not something a plugin author can act on.
func TestToBitmapNamesTheFileItCouldNotDecode(t *testing.T) {
	_, err := ToBitmap([]byte("this is not an image"), "broken.jpg")
	if err == nil {
		t.Fatal("expected an error for input that is not an image")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("broken.jpg")) {
		t.Errorf("error %q does not name the file", err)
	}
}
