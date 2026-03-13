package imagex

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func ToWebPScaledFFmpeg(inputPath, outputPath string, w, h int, quality int) error {
	if quality <= 0 || quality > 100 {
		quality = 85
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}

	// Blur-fill resize: split input into background and foreground.
	// Background: enlarge to cover WxH, center-crop to WxH, then blur (sigma=30) for natural fill.
	// Foreground: scale to fit WxH (preserving entire image, no cropping).
	// Overlay foreground on blurred background, centered.
	// This produces full-bleed output with no padding borders and no cropping of important content.
	filter := fmt.Sprintf(
		"split[bg][fg];[bg]scale=%d:%d:force_original_aspect_ratio=increase:flags=lanczos,crop=%d:%d:(in_w-%d)/2:(in_h-%d)/2,gblur=sigma=30[bg];[fg]scale=%d:%d:force_original_aspect_ratio=decrease:flags=lanczos[fg];[bg][fg]overlay=(%d-w)/2:(%d-h)/2",
		w, h, w, h, w, h, w, h, w, h,
	)

	cmd := exec.Command(
		"ffmpeg",
		"-y",
		"-hide_banner",
		"-loglevel", "error",
		"-i", inputPath,
		"-vf", filter,
		"-c:v", "libwebp",
		"-q:v", fmt.Sprintf("%d", quality),
		outputPath,
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("ffmpeg not found. Install ffmpeg and ensure it is in PATH. Try: winget install --id=Gyan.FFmpeg -e")
		}
		return fmt.Errorf("ffmpeg failed: %v\n%s", err, string(out))
	}
	return nil
}

// GetImageDimensions returns the width and height of an image file.
// Uses pure Go image decode as primary method (supports PNG/JPEG).
// Falls back to ffprobe for WebP or if Go decode fails.
func GetImageDimensions(imagePath string) (width, height int, err error) {
	// Try pure Go image decode first (primary method)
	width, height, err = getImageDimensionsGo(imagePath)
	if err == nil {
		return width, height, nil
	}

	// Fallback to ffprobe for WebP or if Go decode fails
	return getImageDimensionsFFprobe(imagePath)
}

func getImageDimensionsFFprobe(imagePath string) (width, height int, err error) {
	cmd := exec.Command(
		"ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height",
		"-of", "json",
		imagePath,
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, 0, fmt.Errorf("ffprobe failed: %w", err)
	}

	var result struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
	}

	if err := json.Unmarshal(out, &result); err != nil {
		return 0, 0, fmt.Errorf("ffprobe json parse failed: %w", err)
	}

	if len(result.Streams) == 0 {
		return 0, 0, fmt.Errorf("ffprobe: no streams found")
	}

	return result.Streams[0].Width, result.Streams[0].Height, nil
}

func getImageDimensionsGo(imagePath string) (width, height int, err error) {
	file, err := os.Open(imagePath)
	if err != nil {
		return 0, 0, fmt.Errorf("open image file: %w", err)
	}
	defer file.Close()

	img, _, err := image.DecodeConfig(file)
	if err != nil {
		// Try webp with a different approach if standard decode fails
		if strings.HasSuffix(strings.ToLower(imagePath), ".webp") {
			return 0, 0, fmt.Errorf("webp decode not supported by standard library, use ffprobe: %w", err)
		}
		return 0, 0, fmt.Errorf("image decode: %w", err)
	}

	return img.Width, img.Height, nil
}
