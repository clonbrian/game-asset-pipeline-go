package postprocess

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"

	deepteamswebp "github.com/deepteams/webp"
)

// DefaultWebPQuality is used when a non-positive quality is passed.
const DefaultWebPQuality float32 = 85

// WriteFinal writes the postprocessed image using the given format (only "webp" supported).
func WriteFinal(path string, img image.Image, format string, webpQuality float32) error {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "webp":
		return writeWebP(path, img, webpQuality)
	default:
		return fmt.Errorf("postprocess: unsupported final format %q (supported: webp)", format)
	}
}

func writeWebP(path string, img image.Image, quality float32) error {
	if quality <= 0 {
		quality = DefaultWebPQuality
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	opts := &deepteamswebp.EncoderOptions{
		Quality: quality,
		Method:  4,
	}
	if err := deepteamswebp.Encode(f, img, opts); err != nil {
		return fmt.Errorf("encode webp: %w", err)
	}
	return nil
}
