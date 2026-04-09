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
	"game-asset-pipeline-go/internal/imagegen/imagen"
	"game-asset-pipeline-go/internal/imagegen/postprocess"
	"game-asset-pipeline-go/internal/model"
)

type geminiReportEntry struct {
	InputFile       string `json:"inputFile"`
	RawOutputFile   string `json:"rawOutputFile"`
	FinalOutputFile string `json:"finalOutputFile"`
	ProviderRouteUsed string `json:"providerRouteUsed"`
	ExecutionModeUsed string `json:"executionModeUsed"`
	SourceImageUsed bool `json:"sourceImageUsed"`
	BatchJobID      string `json:"batchJobId,omitempty"`
	BatchItemID     string `json:"batchItemId,omitempty"`
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

type geminiJob struct {
	inputPath string
	baseName  string
	sz        model.ImageGenSizeSpec
}

// BatchGemini: (1) Gemini writes raw PNGs to outputDir/raw; (2) local postprocess writes finals to outputDir/final; reports stay in outputDir.
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
	preset, ok := ig.Presets[ig.ModelPreset]
	if !ok {
		return fmt.Errorf("imageGeneration.modelPreset %q is invalid; supported: gemini_default_realtime, gemini_default_batch, gemini_25_batch_cheap, imagen_fast_test", ig.ModelPreset)
	}
	providerRoute := strings.ToLower(strings.TrimSpace(preset.ProviderRoute))
	if providerRoute != "gemini" && providerRoute != "imagen" {
		return fmt.Errorf("imageGeneration.presets[%q].providerRoute %q is invalid; use gemini or imagen", ig.ModelPreset, preset.ProviderRoute)
	}
	resolvedModel := strings.TrimSpace(preset.Model)
	if resolvedModel == "" {
		return fmt.Errorf("imageGeneration.presets[%q].model is empty", ig.ModelPreset)
	}
	resolvedImageSize := strings.TrimSpace(preset.ImageSize)
	// For some Gemini batch models (e.g. gemini-2.5-flash-image), omitting imageSize is valid.
	// Keep fallback only for realtime or when preset explicitly sets it.
	if resolvedImageSize == "" && strings.ToLower(strings.TrimSpace(preset.ExecutionMode)) != "batch" {
		resolvedImageSize = ig.ImageSize
	}
	executionMode := strings.ToLower(strings.TrimSpace(preset.ExecutionMode))
	if executionMode == "" {
		executionMode = "realtime"
	}
	if executionMode != "realtime" && executionMode != "batch" {
		return fmt.Errorf("imageGeneration.presets[%q].executionMode %q is invalid; use realtime or batch", ig.ModelPreset, preset.ExecutionMode)
	}

	for _, sz := range ig.Sizes {
		if sz.TargetWidth <= 0 || sz.TargetHeight <= 0 {
			return fmt.Errorf("imageGeneration.sizes name=%q: targetWidth and targetHeight must be > 0 (got %dx%d)", sz.Name, sz.TargetWidth, sz.TargetHeight)
		}
		if strings.TrimSpace(sz.AspectRatio) == "" {
			return fmt.Errorf("imageGeneration.sizes name=%q: aspectRatio is required for Gemini raw generation", sz.Name)
		}
	}
	ff := strings.ToLower(strings.TrimSpace(ig.FinalFormat))
	if ff != "webp" {
		return fmt.Errorf("imageGeneration.finalFormat %q is not supported (only \"webp\")", ig.FinalFormat)
	}
	postprocessEnabled := ig.PostprocessEnabled == nil || *ig.PostprocessEnabled

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

	rawDir := filepath.Join(ig.OutputDir, "raw")
	finalDir := filepath.Join(ig.OutputDir, "final")
	for _, d := range []string{ig.OutputDir, rawDir, finalDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create output dir %s: %w", d, err)
		}
	}

	timeoutMs := *ig.TimeoutMs
	var httpClient *http.Client
	if timeoutMs > 0 {
		httpClient = &http.Client{Timeout: time.Duration(timeoutMs) * time.Millisecond}
	} else {
		fmt.Printf("[WARN] batch-gemini running with no HTTP timeout\n")
		httpClient = &http.Client{}
	}

	var jobs []geminiJob
	for _, p := range imageFiles {
		base := filepath.Base(p)
		ext := filepath.Ext(base)
		nameNoExt := strings.TrimSuffix(base, ext)
		for _, sz := range ig.Sizes {
			jobs = append(jobs, geminiJob{inputPath: p, baseName: nameNoExt, sz: sz})
		}
	}

	persistRaw := ig.KeepRaw != nil && *ig.KeepRaw
	if !postprocessEnabled && !persistRaw {
		fmt.Printf("[WARN] keepRaw=false is ignored when postprocessEnabled=false; raw-only mode always preserves raw outputs\n")
		persistRaw = true
	}
	mode := "raw+postprocess"
	if !postprocessEnabled {
		mode = "raw-only"
	}
	fmt.Printf("[INFO] batch-gemini mode = %s\n", mode)
	fmt.Printf("[INFO] executionMode = %s\n", executionMode)
	fmt.Printf("[INFO] Gemini batch (raw PNG + local postprocess=%v → final %s): %d sources × %d sizes = %d jobs (concurrency=%d keepRaw=%v)\n",
		postprocessEnabled, ff, len(imageFiles), len(ig.Sizes), len(jobs), ig.Concurrency, persistRaw)
	fmt.Printf("[INFO] preset=%s route=%s model=%s output=%s (raw=%s final=%s)\n", ig.ModelPreset, providerRoute, resolvedModel, ig.OutputDir, rawDir, finalDir)
	if providerRoute == "imagen" {
		fmt.Printf("[WARN] imagen_fast_test currently runs as text-to-image and does not use the source image as reference input\n")
	}
	if providerRoute == "gemini" && executionMode == "batch" {
		return a.runGeminiBatchMode(ig, jobs, rawDir, finalDir, ff, postprocessEnabled, persistRaw, providerRoute, executionMode, resolvedModel, resolvedImageSize, httpClient, apiKey)
	}

	gc := &gemini.Client{
		HTTP:   httpClient,
		APIKey: apiKey,
		Model:  resolvedModel,
	}
	ic := &imagen.Client{
		HTTP:   httpClient,
		APIKey: apiKey,
		Model:  resolvedModel,
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
			finalName := fmt.Sprintf("%s__%s.%s", j.baseName, j.sz.Name, ff)
			rawPath := filepath.Join(rawDir, rawName)
			finalPath := filepath.Join(finalDir, finalName)
			tw, th := j.sz.TargetWidth, j.sz.TargetHeight

			entry := geminiReportEntry{
				InputFile:       j.inputPath,
				RawOutputFile:   rawPath,
				FinalOutputFile: finalPath,
				ProviderRouteUsed: providerRoute,
				ExecutionModeUsed: executionMode,
				SourceImageUsed: providerRoute == "gemini",
				SizeName:        j.sz.Name,
			}
			if !postprocessEnabled {
				entry.FinalOutputFile = ""
			}

			if !ig.Overwrite {
				if postprocessEnabled {
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
				} else {
					if _, err := os.Stat(rawPath); err == nil {
						entry.Status = "skipped"
						skippedN.Add(1)
						reportMu.Lock()
						entries = append(entries, entry)
						reportMu.Unlock()
						n := completedN.Add(1)
						fmt.Printf("[%d/%d] SKIP %s (%s) raw exists -> %s\n", n, totalJobs, j.inputPath, j.sz.Name, rawPath)
						return
					}
				}
			}

			needGemini := ig.Overwrite
			if !needGemini {
				if _, err := os.Stat(rawPath); err != nil {
					needGemini = true
				}
			}

			var srcImg image.Image
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

				fullPrompt := composeGeminiUserPrompt(ig, j.sz)
				ctx := context.Background()
				backoff := 700 * time.Millisecond
				var genBytes []byte
				var genMime string
				switch providerRoute {
				case "gemini":
					genBytes, genMime, err = gemini.GenerateWithRetry(ctx, gc, fullPrompt, imgBytes, j.inputPath, j.sz.AspectRatio, resolvedImageSize, ig.Retry, backoff)
				case "imagen":
					genBytes, genMime, err = imagen.GenerateWithRetry(ctx, ic, fullPrompt, j.inputPath, j.sz.AspectRatio, ig.Retry, backoff)
				default:
					err = fmt.Errorf("unsupported provider route: %s", providerRoute)
				}
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

				if persistRaw {
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
					srcImg, err = decodeImageFile(rawPath)
				} else {
					fmt.Printf("[RAW] %s (%s) -> (memory only, keepRaw=false)\n", j.inputPath, j.sz.Name)
					srcImg, _, err = image.Decode(bytes.NewReader(genBytes))
				}
				if err != nil {
					entry.Status = "failed"
					entry.Error = fmt.Sprintf("decode gemini output: %v", err)
					failedN.Add(1)
					reportMu.Lock()
					entries = append(entries, entry)
					reportMu.Unlock()
					n := completedN.Add(1)
					fmt.Printf("[%d/%d] FAIL %s (%s) decode gemini output: %v\n", n, totalJobs, j.inputPath, j.sz.Name, err)
					return
				}
			} else {
				fmt.Printf("[REUSE] %s (%s) -> %s\n", j.inputPath, j.sz.Name, rawPath)
				var err error
				srcImg, err = decodeImageFile(rawPath)
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
			}

			if needGemini && !persistRaw {
				entry.RawOutputFile = ""
			}

			if !postprocessEnabled {
				entry.Status = "success"
				successN.Add(1)
				reportMu.Lock()
				entries = append(entries, entry)
				reportMu.Unlock()
				n := completedN.Add(1)
				fmt.Printf("[%d/%d] OK   %s (%s) raw-only -> %s\n", n, totalJobs, j.inputPath, j.sz.Name, rawPath)
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

			if err := postprocess.WriteFinal(finalPath, outImg, ig.FinalFormat, postprocess.DefaultWebPQuality); err != nil {
				entry.Status = "failed"
				entry.Error = fmt.Sprintf("write final: %v", err)
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

	if postprocessEnabled {
		fmt.Printf("\n[DONE] Gemini batch finished (raw + fixed-size finals).\n")
	} else {
		fmt.Printf("\n[DONE] Gemini batch finished (raw-only mode).\n")
	}
	fmt.Printf("  success=%d failed=%d skipped=%d\n", report.SuccessCount, report.FailedCount, report.SkippedCount)
	fmt.Printf("  outputDir=%s\n", ig.OutputDir)
	fmt.Printf("  rawDir=%s\n", rawDir)
	fmt.Printf("  finalDir=%s\n", finalDir)
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
	_ = w.Write([]string{"input_file", "raw_output_file", "final_output_file", "provider_route_used", "execution_mode_used", "source_image_used", "batch_job_id", "batch_item_id", "size_name", "status", "error"})
	for _, r := range rows {
		sourceUsed := "false"
		if r.SourceImageUsed {
			sourceUsed = "true"
		}
		_ = w.Write([]string{r.InputFile, r.RawOutputFile, r.FinalOutputFile, r.ProviderRouteUsed, r.ExecutionModeUsed, sourceUsed, r.BatchJobID, r.BatchItemID, r.SizeName, r.Status, r.Error})
	}
	return w.Error()
}
