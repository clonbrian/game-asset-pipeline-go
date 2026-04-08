package app

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"image"
	"image/png"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "golang.org/x/image/webp"

	"game-asset-pipeline-go/internal/imagegen/gemini"
	"game-asset-pipeline-go/internal/imagegen/postprocess"
	"game-asset-pipeline-go/internal/model"
)

type geminiReportEntry struct {
	InputFile       string `json:"inputFile"`
	RawOutputFile   string `json:"rawOutputFile"`
	FinalOutputFile string `json:"finalOutputFile"`
	SizeName        string `json:"sizeName"`
	Status          string `json:"status"`
	Error           string `json:"error,omitempty"`
}

type geminiBatchReport struct {
	RunAt        string              `json:"runAt"`
	SuccessCount int                 `json:"successCount"`
	FailedCount  int                 `json:"failedCount"`
	SkippedCount int                 `json:"skippedCount"`
	Entries      []geminiReportEntry `json:"entries"`
}

// BatchGemini: (1) Gemini produces aspect-ratio raw PNGs __{name}__raw.png; (2) local cover+center-crop to targetWidth×targetHeight → __{name}.png.
func (a *App) BatchGemini() error {
	cfg := a.Cfg
	ig := cfg.ImageGeneration
	if ig == nil {
		return fmt.Errorf("config.imageGeneration is missing: add the imageGeneration block to your config JSON")
	}
	if !ig.Enabled {
		return fmt.Errorf("imageGeneration.enabled is false: set enabled to true to run batch-gemini")
	}
	if strings.ToLower(strings.TrimSpace(ig.Provider)) != "gemini" {
		return fmt.Errorf("unsupported imageGeneration.provider %q (only \"gemini\" is implemented)", ig.Provider)
	}

	for _, sz := range ig.Sizes {
		if sz.TargetWidth <= 0 || sz.TargetHeight <= 0 {
			return fmt.Errorf("imageGeneration.sizes name=%q: targetWidth and targetHeight must be > 0 (got %dx%d)", sz.Name, sz.TargetWidth, sz.TargetHeight)
		}
		if strings.TrimSpace(sz.AspectRatio) == "" {
			return fmt.Errorf("imageGeneration.sizes name=%q: aspectRatio is required for Gemini raw generation", sz.Name)
		}
	}

	apiKey := strings.TrimSpace(os.Getenv(ig.APIKeyEnv))
	if apiKey == "" {
		return fmt.Errorf("environment variable %s is empty (set your Gemini API key)", ig.APIKeyEnv)
	}

	extOK := map[string]struct{}{}
	for _, e := range ig.SupportedExtensions {
		extOK[strings.ToLower(e)] = struct{}{}
	}

	var imageFiles []string
	err := filepath.Walk(ig.InputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if _, ok := extOK[ext]; !ok {
			return nil
		}
		imageFiles = append(imageFiles, path)
		return nil
	})
	if err != nil {
		return fmt.Errorf("scan inputDir: %w", err)
	}

	if len(imageFiles) == 0 {
		fmt.Printf("[INFO] No images under %s (extensions %v)\n", ig.InputDir, ig.SupportedExtensions)
		return nil
	}

	if err := os.MkdirAll(ig.OutputDir, 0o755); err != nil {
		return fmt.Errorf("create outputDir: %w", err)
	}

	type job struct {
		inputPath string
		baseName  string
		sz        model.ImageGenSizeSpec
	}
	var jobs []job
	for _, p := range imageFiles {
		base := filepath.Base(p)
		ext := filepath.Ext(base)
		nameNoExt := strings.TrimSuffix(base, ext)
		for _, sz := range ig.Sizes {
			jobs = append(jobs, job{inputPath: p, baseName: nameNoExt, sz: sz})
		}
	}

	fmt.Printf("[INFO] Gemini batch (raw + local postprocess): %d sources × %d sizes = %d jobs (concurrency=%d)\n",
		len(imageFiles), len(ig.Sizes), len(jobs), ig.Concurrency)
	fmt.Printf("[INFO] Model=%s output=%s\n", ig.Model, ig.OutputDir)

	httpClient := &http.Client{Timeout: time.Duration(ig.TimeoutMs) * time.Millisecond}
	gc := &gemini.Client{
		HTTP:   httpClient,
		APIKey: apiKey,
		Model:  strings.TrimSpace(ig.Model),
	}

	var (
		reportMu   sync.Mutex
		entries    []geminiReportEntry
		successN   atomic.Int64
		failedN    atomic.Int64
		skippedN   atomic.Int64
		completedN atomic.Int64
	)
	totalJobs := int64(len(jobs))

	sem := make(chan struct{}, ig.Concurrency)
	var wg sync.WaitGroup
	for _, j := range jobs {
		j := j
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			rawName := fmt.Sprintf("%s__%s__raw.png", j.baseName, j.sz.Name)
			finalName := fmt.Sprintf("%s__%s.png", j.baseName, j.sz.Name)
			rawPath := filepath.Join(ig.OutputDir, rawName)
			finalPath := filepath.Join(ig.OutputDir, finalName)
			tw, th := j.sz.TargetWidth, j.sz.TargetHeight

			entry := geminiReportEntry{
				InputFile:       j.inputPath,
				RawOutputFile:   rawPath,
				FinalOutputFile: finalPath,
				SizeName:        j.sz.Name,
			}

			if !ig.Overwrite {
				if _, err := os.Stat(finalPath); err == nil {
					entry.Status = "skipped"
					skippedN.Add(1)
					reportMu.Lock()
					entries = append(entries, entry)
					reportMu.Unlock()
					n := completedN.Add(1)
					fmt.Printf("[%d/%d] SKIP %s (%s) final exists -> %s\n", n, totalJobs, j.inputPath, j.sz.Name, finalPath)
					return
				}
			}

			needGemini := ig.Overwrite
			if !needGemini {
				if _, err := os.Stat(rawPath); err != nil {
					needGemini = true
				}
			}

			if needGemini {
				imgBytes, err := os.ReadFile(j.inputPath)
				if err != nil {
					entry.Status = "failed"
					entry.Error = fmt.Sprintf("read input: %v", err)
					failedN.Add(1)
					reportMu.Lock()
					entries = append(entries, entry)
					reportMu.Unlock()
					n := completedN.Add(1)
					fmt.Printf("[%d/%d] FAIL %s (%s): %v\n", n, totalJobs, j.inputPath, j.sz.Name, err)
					return
				}

				sizePrompt := sizeSpecificPrompt(j.sz)
				fullPrompt := strings.TrimSpace(ig.PromptTemplate) + "\n\n" + sizePrompt
				ctx := context.Background()
				inMime := mimeForExt(filepath.Ext(j.inputPath))
				backoff := 700 * time.Millisecond
				genBytes, genMime, err := gemini.GenerateWithRetry(ctx, gc, fullPrompt, imgBytes, inMime, j.sz.AspectRatio, ig.ImageSize, ig.Retry, backoff)
				if err != nil {
					entry.Status = "failed"
					entry.Error = err.Error()
					failedN.Add(1)
					reportMu.Lock()
					entries = append(entries, entry)
					reportMu.Unlock()
					n := completedN.Add(1)
					fmt.Printf("[%d/%d] FAIL %s (%s) gemini: %v\n", n, totalJobs, j.inputPath, j.sz.Name, err)
					return
				}

				if err := writeGeminiOutputPNG(rawPath, genBytes, genMime); err != nil {
					entry.Status = "failed"
					entry.Error = fmt.Sprintf("write raw png: %v", err)
					failedN.Add(1)
					reportMu.Lock()
					entries = append(entries, entry)
					reportMu.Unlock()
					n := completedN.Add(1)
					fmt.Printf("[%d/%d] FAIL %s (%s) raw write: %v\n", n, totalJobs, j.inputPath, j.sz.Name, err)
					return
				}
				fmt.Printf("[RAW] %s (%s) -> %s\n", j.inputPath, j.sz.Name, rawPath)
			} else {
				fmt.Printf("[REUSE] %s (%s) -> %s\n", j.inputPath, j.sz.Name, rawPath)
			}

			srcImg, err := decodeImageFile(rawPath)
			if err != nil {
				entry.Status = "failed"
				entry.Error = fmt.Sprintf("decode raw: %v", err)
				failedN.Add(1)
				reportMu.Lock()
				entries = append(entries, entry)
				reportMu.Unlock()
				n := completedN.Add(1)
				fmt.Printf("[%d/%d] FAIL %s (%s) decode raw: %v\n", n, totalJobs, j.inputPath, j.sz.Name, err)
				return
			}

			outImg, err := postprocess.FixedSizeCover(srcImg, tw, th)
			if err != nil {
				entry.Status = "failed"
				entry.Error = fmt.Sprintf("postprocess: %v", err)
				failedN.Add(1)
				reportMu.Lock()
				entries = append(entries, entry)
				reportMu.Unlock()
				n := completedN.Add(1)
				fmt.Printf("[%d/%d] FAIL %s (%s) postprocess: %v\n", n, totalJobs, j.inputPath, j.sz.Name, err)
				return
			}

			if err := postprocess.WritePNG(finalPath, outImg); err != nil {
				entry.Status = "failed"
				entry.Error = fmt.Sprintf("write final png: %v", err)
				failedN.Add(1)
				reportMu.Lock()
				entries = append(entries, entry)
				reportMu.Unlock()
				n := completedN.Add(1)
				fmt.Printf("[%d/%d] FAIL %s (%s) final write: %v\n", n, totalJobs, j.inputPath, j.sz.Name, err)
				return
			}

			entry.Status = "success"
			successN.Add(1)
			reportMu.Lock()
			entries = append(entries, entry)
			reportMu.Unlock()
			n := completedN.Add(1)
			fmt.Printf("[%d/%d] OK   %s (%s) final %dx%d -> %s\n", n, totalJobs, j.inputPath, j.sz.Name, tw, th, finalPath)
		}()
	}
	wg.Wait()

	report := geminiBatchReport{
		RunAt:        time.Now().Format(time.RFC3339),
		SuccessCount: int(successN.Load()),
		FailedCount:  int(failedN.Load()),
		SkippedCount: int(skippedN.Load()),
		Entries:      entries,
	}

	reportJSON := filepath.Join(ig.OutputDir, "gemini_batch_report.json")
	if err := writeJSON(reportJSON, report); err != nil {
		return fmt.Errorf("write report json: %w", err)
	}
	reportCSV := filepath.Join(ig.OutputDir, "gemini_batch_report.csv")
	if err := writeGeminiReportCSV(reportCSV, entries); err != nil {
		return fmt.Errorf("write report csv: %w", err)
	}

	fmt.Printf("\n[DONE] Gemini batch finished (raw + fixed-size finals).\n")
	fmt.Printf("  success=%d failed=%d skipped=%d\n", report.SuccessCount, report.FailedCount, report.SkippedCount)
	fmt.Printf("  outputs=%s\n", ig.OutputDir)
	fmt.Printf("  report_json=%s\n", reportJSON)
	fmt.Printf("  report_csv=%s\n", reportCSV)
	if report.FailedCount > 0 {
		fmt.Printf("[WARN] Failed jobs (see report for details):\n")
		for _, e := range entries {
			if e.Status == "failed" {
				fmt.Printf("  - %s [%s]: %s\n", e.InputFile, e.SizeName, e.Error)
			}
		}
	}
	return nil
}

func decodeImageFile(path string) (image.Image, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	return img, err
}

func sizeSpecificPrompt(sz model.ImageGenSizeSpec) string {
	if s := strings.TrimSpace(sz.SizePrompt); s != "" {
		return s
	}
	switch sz.Name {
	case "square":
		return "Adapt the image to a balanced 1:1 composition suitable for icon-like or lobby usage."
	case "wide":
		return "Adapt the image to a cinematic 16:9 composition suitable for banner or desktop promotional usage."
	case "tall":
		return "Adapt the image to a mobile-first 9:16 composition suitable for portrait promotional usage."
	default:
		return fmt.Sprintf("Adapt the composition for aspect ratio %s for polished game marketing use.", sz.AspectRatio)
	}
}

func mimeForExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	default:
		return "image/png"
	}
}

func writeGeminiOutputPNG(path string, data []byte, mime string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if strings.EqualFold(mime, "image/png") {
		return os.WriteFile(path, data, 0o644)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func writeGeminiReportCSV(path string, rows []geminiReportEntry) error {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	_ = w.Write([]string{"input_file", "raw_output_file", "final_output_file", "size_name", "status", "error"})
	for _, r := range rows {
		_ = w.Write([]string{r.InputFile, r.RawOutputFile, r.FinalOutputFile, r.SizeName, r.Status, r.Error})
	}
	return w.Error()
}
