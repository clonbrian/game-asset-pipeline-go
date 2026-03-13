package extractors

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"game-asset-pipeline-go/internal/model"
)

type LocalDirExtractor struct{}

func (e *LocalDirExtractor) Extract(provider model.ProviderSource, body []byte, contentType string) ([]model.AssetCandidate, error) {
	dirPath := provider.SourceURL
	if dirPath == "" {
		return nil, fmt.Errorf("local_dir source_url is empty")
	}

	// Ensure the directory exists
	info, err := os.Stat(dirPath)
	if err != nil {
		return nil, fmt.Errorf("local_dir path does not exist: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("local_dir source_url is not a directory: %s", dirPath)
	}

	var candidates []model.AssetCandidate

	// Walk the directory recursively
	err = filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		// Check if file is an image
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".webp" {
			return nil
		}

		// Get absolute path
		absPath, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("failed to get absolute path for %s: %w", path, err)
		}

		// Create AssetCandidate
		fileName := filepath.Base(path)
		width, height := parseDimensionsFromFilename(fileName)
		candidate := model.AssetCandidate{
			Provider: provider.Provider,
			Source:   provider.SourceURL,
			URL:      absPath,
			FileName: fileName,
			Title:    "",
			Alt:      "",
			Width:    width,
			Height:   height,
		}

		candidates = append(candidates, candidate)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("error walking directory: %w", err)
	}

	return candidates, nil
}

// parseDimensionsFromFilename extracts width and height from filename patterns like "820x560" or "_820x560_"
// Returns (0, 0) if not found.
func parseDimensionsFromFilename(filename string) (width, height int) {
	// Pattern: digits x digits, optionally surrounded by non-word characters
	// Examples: "820x560", "_820x560_", "JL_820x560_GameID586_en-US.png"
	re := regexp.MustCompile(`(\d+)x(\d+)`)
	matches := re.FindStringSubmatch(filename)
	if len(matches) == 3 {
		if w, err := strconv.Atoi(matches[1]); err == nil {
			if h, err := strconv.Atoi(matches[2]); err == nil {
				return w, h
			}
		}
	}
	return 0, 0
}
