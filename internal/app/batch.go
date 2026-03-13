package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"game-asset-pipeline-go/internal/imagex"
	"game-asset-pipeline-go/internal/util"
)

func (a *App) Batch() error {
	cfg := a.Cfg

	// Prepare output folders
	outAssetsRoot := filepath.Join(cfg.OutputDir, "assets_by_game")
	outReviewDir := filepath.Join(cfg.OutputDir, "review")
	outZipDir := filepath.Join(cfg.OutputDir, "zip")
	_ = os.MkdirAll(outAssetsRoot, 0o755)
	_ = os.MkdirAll(outReviewDir, 0o755)
	_ = os.MkdirAll(outZipDir, 0o755)

	reviewCSVPath := filepath.Join(outReviewDir, "report.csv")
	reviewRows := [][]string{{"input_file", "slug", "status", "error"}}

	// Scan incoming_dir recursively for image files
	var imageFiles []string
	err := filepath.Walk(cfg.IncomingDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp" {
			imageFiles = append(imageFiles, path)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("error scanning incoming_dir: %w", err)
	}

	if len(imageFiles) == 0 {
		fmt.Printf("[INFO] No image files found in %s\n", cfg.IncomingDir)
		return nil
	}

	fmt.Printf("[INFO] Found %d image files\n", len(imageFiles))

	// Process each image file
	for _, inputPath := range imageFiles {
		// Generate slug from base filename
		baseName := filepath.Base(inputPath)
		ext := filepath.Ext(baseName)
		nameWithoutExt := strings.TrimSuffix(baseName, ext)
		slug := util.SafeSlug(nameWithoutExt)

		if slug == "" {
			slug = "unknown"
		}

		// Create output directory for this slug
		gameDir := filepath.Join(outAssetsRoot, slug)
		_ = os.MkdirAll(gameDir, 0o755)

		// Process each size
		var outputs []string
		var processErr error
		for _, sz := range cfg.Sizes {
			outName := fmt.Sprintf("%s_%dx%d.webp", slug, sz.Width, sz.Height)
			outPath := filepath.Join(gameDir, outName)

			if err := imagex.ToWebPScaledFFmpeg(inputPath, outPath, sz.Width, sz.Height, 85); err != nil {
				processErr = err
				break
			}

			outputs = append(outputs, outPath)
		}

		// Record result
		if processErr != nil {
			reviewRows = append(reviewRows, []string{
				inputPath,
				slug,
				"FAIL",
				processErr.Error(),
			})
		} else {
			reviewRows = append(reviewRows, []string{
				inputPath,
				slug,
				"OK",
				"",
			})
			fmt.Printf("[OK] %s -> %s (%d outputs)\n", inputPath, slug, len(outputs))
		}
	}

	// Write review CSV
	if err := writeCSV(reviewCSVPath, reviewRows); err != nil {
		return err
	}

	// Create zip
	ts := time.Now().Format("20060102_150405")
	zipPath := filepath.Join(outZipDir, fmt.Sprintf("batch_assets_%s.zip", ts))
	if err := ZipFolder(outAssetsRoot, zipPath); err != nil {
		return err
	}

	fmt.Printf("[DONE] review=%s zip=%s\n", reviewCSVPath, zipPath)
	return nil
}
