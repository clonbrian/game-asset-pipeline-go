package app

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"game-asset-pipeline-go/internal/model"
)

// PostprocessGeminiOutput rescans local images under source, applies the same FixedSizeCover + WriteFinal
// pipeline as batch-gemini with postprocessEnabled=true, and writes only under dest (never modifies source).
// Does not call Gemini; GEMINI_API_KEY is not required.
func (a *App) PostprocessGeminiOutput(source, dest string, recursive bool) error {
	source = strings.TrimSpace(source)
	dest = strings.TrimSpace(dest)
	if source == "" {
		return fmt.Errorf("-source is required")
	}
	if dest == "" {
		return fmt.Errorf("-dest is required")
	}

	ig := a.Cfg.ImageGeneration
	if ig == nil {
		return fmt.Errorf("config.imageGeneration is missing")
	}
	if len(ig.Sizes) == 0 {
		return fmt.Errorf("config.imageGeneration.sizes is empty")
	}
	ff := strings.ToLower(strings.TrimSpace(ig.FinalFormat))
	if ff != "webp" {
		return fmt.Errorf("imageGeneration.finalFormat %q is not supported (only \"webp\")", ig.FinalFormat)
	}

	srcAbs, err := filepath.Abs(source)
	if err != nil {
		return fmt.Errorf("source path: %w", err)
	}
	dstAbs, err := filepath.Abs(dest)
	if err != nil {
		return fmt.Errorf("dest path: %w", err)
	}
	st, err := os.Stat(srcAbs)
	if err != nil {
		return fmt.Errorf("source: %w", err)
	}
	if !st.IsDir() {
		return fmt.Errorf("source must be a directory: %s", srcAbs)
	}

	extOK := map[string]struct{}{}
	for _, e := range ig.SupportedExtensions {
		extOK[strings.ToLower(strings.TrimSpace(e))] = struct{}{}
	}
	if len(extOK) == 0 {
		extOK[".png"] = struct{}{}
		extOK[".jpg"] = struct{}{}
		extOK[".jpeg"] = struct{}{}
		extOK[".webp"] = struct{}{}
	}

	var okN, errN int

	err = filepath.WalkDir(srcAbs, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if path != srcAbs && !recursive {
				return filepath.SkipDir
			}
			return nil
		}
		if pathInOrUnderDir(path, dstAbs) {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if _, ok := extOK[ext]; !ok {
			return nil
		}

		rel, err := filepath.Rel(srcAbs, path)
		if err != nil {
			return err
		}
		base := strings.TrimSuffix(filepath.Base(rel), ext)
		sz, err := sizeSpecForOutputStem(base, ig.Sizes)
		if err != nil {
			errN++
			fmt.Fprintf(os.Stderr, "[SKIP] %s: %v\n", rel, err)
			return nil
		}

		outRel := strings.TrimSuffix(rel, ext) + "." + ff
		outPath := filepath.Join(dstAbs, outRel)
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			errN++
			fmt.Fprintf(os.Stderr, "[ERROR] %s: mkdir: %v\n", rel, err)
			return nil
		}

		srcImg, err := decodeImageFile(path)
		if err != nil {
			errN++
			fmt.Fprintf(os.Stderr, "[ERROR] %s: decode: %v\n", rel, err)
			return nil
		}
		if err := a.finalizeFromImage(srcImg, outPath, ff, sz.TargetWidth, sz.TargetHeight); err != nil {
			errN++
			fmt.Fprintf(os.Stderr, "[ERROR] %s: %v\n", rel, err)
			return nil
		}
		okN++
		fmt.Printf("[OK] %s -> %s (%s %dx%d)\n", rel, outRel, sz.Name, sz.TargetWidth, sz.TargetHeight)
		return nil
	})
	if err != nil {
		return err
	}

	fmt.Printf("\n[DONE] postprocess-gemini-output: ok=%d errors=%d\n", okN, errN)
	fmt.Printf("  source=%s\n", srcAbs)
	fmt.Printf("  dest=%s\n", dstAbs)
	if errN > 0 && okN == 0 {
		return fmt.Errorf("postprocess finished with errors and no successful outputs")
	}
	return nil
}

// imageGenEffectiveExecutionMode returns the resolved executionMode for imageGeneration.modelPreset
// (empty preset field defaults to "realtime"), matching batch-gemini preset resolution.
func imageGenEffectiveExecutionMode(ig *model.ImageGenerationSpec) (string, error) {
	if ig == nil {
		return "", fmt.Errorf("config.imageGeneration is missing")
	}
	key := strings.TrimSpace(ig.ModelPreset)
	if key == "" {
		return "", fmt.Errorf("imageGeneration.modelPreset is empty")
	}
	preset, ok := ig.Presets[key]
	if !ok {
		return "", fmt.Errorf("imageGeneration.modelPreset %q is not defined in presets", ig.ModelPreset)
	}
	em := strings.ToLower(strings.TrimSpace(preset.ExecutionMode))
	if em == "" {
		em = "realtime"
	}
	if em != "realtime" && em != "batch" {
		return "", fmt.Errorf("imageGeneration.presets[%q].executionMode %q is invalid; use realtime or batch", key, preset.ExecutionMode)
	}
	return em, nil
}

// PostprocessCurrentMode runs PostprocessGeminiOutput with source/dest derived from imageGeneration.outputDir
// and the active preset's executionMode: batch → recovered/ → recovered_webp/ (recursive); realtime → raw/ → final/ (non-recursive).
// Purely local; does not require GEMINI_API_KEY.
func (a *App) PostprocessCurrentMode() error {
	ig := a.Cfg.ImageGeneration
	em, err := imageGenEffectiveExecutionMode(ig)
	if err != nil {
		return err
	}
	out := strings.TrimSpace(ig.OutputDir)
	if out == "" {
		return fmt.Errorf("imageGeneration.outputDir is empty")
	}
	var source, dest string
	var recursive bool
	switch em {
	case "batch":
		source = filepath.Join(out, "recovered")
		dest = filepath.Join(out, "recovered_webp")
		recursive = true
	default:
		source = filepath.Join(out, "raw")
		dest = filepath.Join(out, "final")
		recursive = false
	}
	fmt.Printf("[INFO] postprocess-current-mode: preset=%s executionMode=%s\n", ig.ModelPreset, em)
	fmt.Printf("[INFO] source=%s dest=%s recursive=%v\n", source, dest, recursive)
	return a.PostprocessGeminiOutput(source, dest, recursive)
}

// sizeSpecForOutputStem maps filename stem to config imageGeneration.sizes using the same rules as
// geminiJobOutputStem / imageGenOutputStem (wide → tall → longest custom suffix → square).
func sizeSpecForOutputStem(stem string, sizes []model.ImageGenSizeSpec) (*model.ImageGenSizeSpec, error) {
	stem = strings.TrimSpace(stem)
	if stem == "" {
		return nil, fmt.Errorf("empty filename stem")
	}
	if len(sizes) == 0 {
		return nil, fmt.Errorf("no sizes in config")
	}

	byName := make(map[string]model.ImageGenSizeSpec, len(sizes))
	for _, sz := range sizes {
		byName[strings.ToLower(strings.TrimSpace(sz.Name))] = sz
	}

	if w, ok := byName["wide"]; ok && len(stem) > 2 && strings.HasSuffix(stem, "-w") {
		return &w, nil
	}
	if t, ok := byName["tall"]; ok && len(stem) > 2 && strings.HasSuffix(stem, "-t") {
		return &t, nil
	}

	type sufSpec struct {
		suf  string
		spec model.ImageGenSizeSpec
	}
	var customs []sufSpec
	for _, sz := range sizes {
		n := strings.ToLower(strings.TrimSpace(sz.Name))
		if n == "square" || n == "tall" || n == "wide" {
			continue
		}
		suf := "-" + strings.ReplaceAll(strings.TrimSpace(sz.Name), "_", "-")
		if len(suf) <= 1 {
			continue
		}
		customs = append(customs, sufSpec{suf, sz})
	}
	sort.Slice(customs, func(i, j int) bool { return len(customs[i].suf) > len(customs[j].suf) })
	for _, c := range customs {
		if len(stem) > len(c.suf) && strings.HasSuffix(stem, c.suf) {
			cp := c.spec
			return &cp, nil
		}
	}

	if s, ok := byName["square"]; ok {
		return &s, nil
	}
	cp := sizes[0]
	return &cp, nil
}

// pathInOrUnderDir reports whether path is dest itself or a file inside dest (both resolved absolute).
func pathInOrUnderDir(path, destRoot string) bool {
	p, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	r, err := filepath.Abs(destRoot)
	if err != nil {
		return false
	}
	p = filepath.Clean(p)
	r = filepath.Clean(r)
	if p == r {
		return true
	}
	return strings.HasPrefix(p, r+string(os.PathSeparator))
}
