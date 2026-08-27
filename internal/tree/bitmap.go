// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package tree

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"  // registered for image.Decode; a source may store any of these
	_ "image/jpeg" // the one that matters: Bliss ships as a JPEG
	_ "image/png"

	"github.com/ericpauley/go-quantize/quantize"
	"golang.org/x/image/bmp"
)

// bitmapColors is the palette size. 256 is the most an 8-bit BMP can address, and the
// measurements that chose this format were made at 256: fewer colours band visibly in a
// smooth sky, which is most of what a wallpaper like Bliss is.
const bitmapColors = 256

// ToBitmap re-encodes an image as an 8-bit, 256-colour, uncompressed BMP.
//
// It exists because the classic wallpaper path — SystemParametersInfo(SPI_SETDESKWALLPAPER)
// — paints bitmaps and nothing else, and 64-bit Windows has no Active Desktop to paint
// anything else with. XP itself does this conversion whenever a user picks a non-BMP
// wallpaper, writing a BMP beside the profile and pointing the registry at that; doing it
// at build time is the same transformation moved earlier, and it means nothing in the image
// needs an image decoder at run time. A PE has none to offer: gdiplus is on no XP medium,
// and the only JPEG decoder otherwise reachable is inside the Active Desktop machinery this
// removes.
//
// Choices worth not re-deriving, all measured against the real bliss.jpg (800x600):
//
//   - 256 colours, no dithering: 470 KB against 1406 KB for 24-bit, and visually close.
//     Dithering is worse on both counts — it adds a visible speckle to smooth gradients and,
//     because speckle destroys run lengths, makes an RLE-compressed file 60% LARGER.
//   - Uncompressed (BI_RGB) rather than BI_RLE8, which would save a further 65 KB. 8-bit
//     BI_RGB is the most widely supported BMP variant there is; RLE in the wallpaper path is
//     unverified, and 65 KB does not buy an assumption.
//   - Full resolution rather than a smaller image stretched to fit. Downscaling loses more
//     than quantising does at the same size: a 640x480 24-bit file is twice as large as this
//     one and visibly blurs the fine cirrus once the desktop stretches it back.
//
// The result must be deterministic — the same source has to produce the same image, so a
// rebuild is comparable and a receipt means something. go-quantize's median cut is stable
// across runs (verified), and no dithering means no error diffusion to vary.
func ToBitmap(data []byte, name string) ([]byte, error) {
	src, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode %q: %w", name, err)
	}
	// Already a bitmap: leave the bytes alone rather than re-encoding them. Re-quantising
	// an image that is already 8-bit would only lose colours a second time, and a source
	// that ships a BMP wallpaper needs no help from this at all.
	if format == "bmp" {
		return data, nil
	}

	pal := quantize.MedianCutQuantizer{}.Quantize(make(color.Palette, 0, bitmapColors), src)
	dst := image.NewPaletted(src.Bounds(), pal)
	// draw.Src, not a dithering drawer: nearest colour, for the reasons above.
	draw.Draw(dst, dst.Bounds(), src, src.Bounds().Min, draw.Src)

	var out bytes.Buffer
	if err := bmp.Encode(&out, dst); err != nil {
		return nil, fmt.Errorf("encode %q as a bitmap: %w", name, err)
	}
	return out.Bytes(), nil
}
