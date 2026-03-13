package app

import (
	"fmt"
	"os"
	"path/filepath"
)

func (a *App) GeneratePrompts(slug string) error {
	cfg := a.Cfg

	// Prepare output directory
	outPromptsDir := filepath.Join(cfg.OutputDir, "prompts")
	if err := os.MkdirAll(outPromptsDir, 0o755); err != nil {
		return err
	}

	// Define the 3 fixed sizes
	sizes := []struct {
		width  int
		height int
		name   string
	}{
		{210, 210, "square"},
		{325, 234, "wide"},
		{294, 400, "tall"},
	}

	// Build prompts content
	var content string
	for i, size := range sizes {
		prompt := fmt.Sprintf(
			"Size: %dx%d (%s)\n"+
				"Outpaint to %dx%d. "+
				"NO crop, NO stretch. "+
				"Extend background naturally to fill the new canvas (no blur, no gradient). "+
				"Keep game title fully visible. "+
				"Match original art style and lighting.\n",
			size.width, size.height, size.name,
			size.width, size.height,
		)
		content += prompt
		if i < len(sizes)-1 {
			content += "\n"
		}
	}

	// Write to file
	outputPath := filepath.Join(outPromptsDir, fmt.Sprintf("%s_outpaint_prompts.txt", slug))
	if err := os.WriteFile(outputPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("failed to write prompts file: %w", err)
	}

	fmt.Printf("[OK] Generated prompts: %s\n", outputPath)
	return nil
}
