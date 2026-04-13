package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"game-asset-pipeline-go/internal/imagegen/gemini"
)

// RecoverBatchResults downloads a succeeded batch job's inlined image outputs and writes files under
// {imageGeneration.outputDir}/recovered/{sanitizedJobId}/. Does not submit jobs or use modelPreset for guessing.
func (a *App) RecoverBatchResults(jobID string) error {
	return a.recoverBatchResultsWithBaseDir(jobID, "")
}

// recoverBatchResultsWithBaseDir writes under {baseOutputDir}/recovered/... when baseOutputDir is non-empty;
// otherwise uses config.imageGeneration.outputDir.
func (a *App) recoverBatchResultsWithBaseDir(jobID string, baseOutputDir string) error {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return fmt.Errorf(`-job-id is required (example: -job-id "batches/abc123")`)
	}
	canonicalID, err := gemini.ValidateAndNormalizeBatchJobID(jobID)
	if err != nil {
		return fmt.Errorf("invalid batch job id: %w", err)
	}

	ig := a.Cfg.ImageGeneration
	if ig == nil {
		return fmt.Errorf("config.imageGeneration is missing")
	}
	apiKey := strings.TrimSpace(os.Getenv(ig.APIKeyEnv))
	if apiKey == "" {
		return fmt.Errorf("missing API key: environment variable %q is empty", ig.APIKeyEnv)
	}

	outBase := strings.TrimSpace(baseOutputDir)
	if outBase == "" {
		outBase = ig.OutputDir
	}

	timeoutMs := 120000
	if ig.TimeoutMs != nil {
		timeoutMs = *ig.TimeoutMs
	}
	var httpClient *http.Client
	if timeoutMs > 0 {
		httpClient = &http.Client{Timeout: time.Duration(timeoutMs) * time.Millisecond}
	} else {
		httpClient = &http.Client{}
	}

	gc := &gemini.Client{
		HTTP:   httpClient,
		APIKey: apiKey,
		Model:  "",
	}

	ctx := context.Background()
	job, items, err := gc.GetBatch(ctx, canonicalID)
	if err != nil {
		return err
	}

	stateUp := strings.ToUpper(strings.TrimSpace(job.State))
	succeeded := stateUp == "BATCH_STATE_SUCCEEDED" || stateUp == "SUCCEEDED"
	if !succeeded {
		return fmt.Errorf("batch job is not in succeeded state (state=%q); only completed jobs can be recovered", job.State)
	}
	if len(items) == 0 {
		return fmt.Errorf("no inlined batch responses in API payload (check output.inlinedResponses shape)")
	}

	outDir := filepath.Join(outBase, "recovered", sanitizeRecoveryJobDir(canonicalID))
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create recovery dir: %w", err)
	}

	recovered, skipped, errCount := 0, 0, 0
	nameSeq := map[string]int{}

	for i, it := range items {
		if strings.TrimSpace(it.Error) != "" {
			errCount++
			fmt.Printf("[ERROR] item %d: %s\n", i, strings.TrimSpace(it.Error))
			continue
		}
		if len(it.Image) == 0 {
			skipped++
			fmt.Printf("[SKIP] item %d: no image in response (e.g. text-only parts)\n", i)
			continue
		}

		base := recoveryBaseName(it.Metadata, i)
		ext := extForImageMime(it.MimeType)
		path := uniqueRecoveryPath(outDir, base, ext, nameSeq)
		if err := os.WriteFile(path, it.Image, 0o644); err != nil {
			errCount++
			fmt.Printf("[ERROR] item %d: write %s: %v\n", i, path, err)
			continue
		}
		recovered++
		fmt.Printf("[OK] %s\n", path)
	}

	fmt.Printf("\n[DONE] recover-batch-results: recovered=%d skipped=%d errors=%d\n", recovered, skipped, errCount)
	fmt.Printf("  outputDir=%s\n", outDir)
	if recovered == 0 && errCount > 0 {
		return fmt.Errorf("recovery finished with errors and no files written")
	}
	return nil
}

func sanitizeRecoveryJobDir(canonicalJobID string) string {
	s := strings.TrimSpace(canonicalJobID)
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	s = strings.ReplaceAll(s, ":", "_")
	if s == "" {
		return "unknown_job"
	}
	return s
}

// recoveryBaseName uses the same stem as raw/final: metadata output_stem when present,
// else derives stem from item_id via stemFromBatchItemID (never uses raw item_id as the filename).
func recoveryBaseName(meta map[string]any, index int) string {
	if meta != nil {
		if v, ok := meta["output_stem"].(string); ok {
			if s := strings.TrimSpace(v); s != "" {
				if s2 := sanitizeRecoveryFileName(s); s2 != "" {
					return s2
				}
			}
		}
		if v, ok := meta["item_id"].(string); ok {
			if stem, ok := stemFromBatchItemID(v); ok {
				if s2 := sanitizeRecoveryFileName(stem); s2 != "" {
					return s2
				}
			}
		}
	}
	return fmt.Sprintf("item_%04d", index)
}

func sanitizeRecoveryFileName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "._-")
	if out == "" {
		return ""
	}
	return out
}

func extForImageMime(mime string) string {
	m := strings.ToLower(strings.TrimSpace(mime))
	switch {
	case strings.Contains(m, "png"):
		return ".png"
	case strings.Contains(m, "webp"):
		return ".webp"
	case strings.Contains(m, "jpeg"), strings.Contains(m, "jpg"):
		return ".jpg"
	default:
		return ".bin"
	}
}

func uniqueRecoveryPath(dir, base, ext string, used map[string]int) string {
	key := base + ext
	n := used[key]
	used[key] = n + 1
	if n == 0 {
		return filepath.Join(dir, base+ext)
	}
	return filepath.Join(dir, fmt.Sprintf("%s_%d%s", base, n, ext))
}
