package postprocess

import (
	"fmt"
	"image"
	"math"

	"golang.org/x/image/draw"
)

// FixedSizeCover scales the source uniformly (same factor for width and height) so that it
// fully covers the target rectangle, then center-crops to exactly dstW×dstH.
// This matches CSS object-fit: cover — no non-uniform stretch; edges may be cropped.
func FixedSizeCover(src image.Image, dstW, dstH int) (image.Image, error) {
	if dstW <= 0 || dstH <= 0 {
		return nil, fmt.Errorf("postprocess: target size must be positive, got %dx%d", dstW, dstH)
	}
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= 0 || sh <= 0 {
		return nil, fmt.Errorf("postprocess: source image has empty bounds")
	}
	scale := math.Max(float64(dstW)/float64(sw), float64(dstH)/float64(sh))
	nw := int(math.Ceil(float64(sw) * scale))
	nh := int(math.Ceil(float64(sh) * scale))
	if nw < dstW {
		nw = dstW
	}
	if nh < dstH {
		nh = dstH
	}
	scaled := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(scaled, scaled.Bounds(), src, b, draw.Src, nil)
	x0 := (nw - dstW) / 2
	y0 := (nh - dstH) / 2
	return scaled.SubImage(image.Rect(x0, y0, x0+dstW, y0+dstH)), nil
}
