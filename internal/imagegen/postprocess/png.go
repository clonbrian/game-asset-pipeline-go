package postprocess

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
)

// WritePNG writes img as PNG to path (creates parent directories).
func WritePNG(path string, img image.Image) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return fmt.Errorf("encode png: %w", err)
	}
	return nil
}
