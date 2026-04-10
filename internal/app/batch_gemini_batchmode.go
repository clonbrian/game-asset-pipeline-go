package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"game-asset-pipeline-go/internal/imagegen/gemini"
	"game-asset-pipeline-go/internal/imagegen/postprocess"
	"game-asset-pipeline-go/internal/model"
)

type persistedBatchMeta struct {
	BatchJobID         string   `json:"batchJobId"`
	ModelPreset        string   `json:"modelPreset"`
	ProviderRoute      string   `json:"providerRoute"`
	ExecutionMode      string   `json:"executionMode"`
	Model              string   `json:"model"`
	CreatedAt          string   `json:"createdAt"`
	InputDir           string   `json:"inputDir"`
	OutputDir          string   `json:"outputDir"`
	PostprocessEnabled bool     `json:"postprocessEnabled"`
	KeepRaw            bool     `json:"keepRaw"`
	RequestCount       int      `json:"requestCount"`
	ItemCount          int      `json:"itemCount"`
	ItemIDs            []string `json:"itemIDs"`
	Status             string   `json:"status"`
}

func (a *App) runGeminiBatchMode(
	ig *model.ImageGenerationSpec,
	jobs []geminiJob,
	rawDir, finalDir, ff string,
	postprocessEnabled, persistRaw bool,
	providerRoute, executionMode, modelName, imageSize string,
	httpClient *http.Client,
	apiKey string,
) error {
	gc := &gemini.Client{HTTP: httpClient, APIKey: apiKey, Model: modelName}
	jobsDir := filepath.Join(ig.OutputDir, "jobs")
	if err := os.MkdirAll(jobsDir, 0o755); err != nil {
		return fmt.Errorf("create jobs metadata dir: %w", err)
	}
	legacyMetaPath := filepath.Join(ig.OutputDir, "gemini_batch_job_meta.json")
	if _, err := os.Stat(legacyMetaPath); err == nil {
		fmt.Printf("[WARN] Legacy batch metadata file is deprecated: %s (new format: %s/*.json)\n", legacyMetaPath, jobsDir)
	}
	var reportRows []geminiReportEntry

	var batchItems []gemini.BatchRequestItem
	jobByItem := map[string]geminiJob{}
	for i, j := range jobs {
		rawPath := filepath.Join(rawDir, fmt.Sprintf("%s__%s__raw.png", j.baseName, j.sz.Name))
		finalPath := filepath.Join(finalDir, fmt.Sprintf("%s__%s.%s", j.baseName, j.sz.Name, ff))
		entry := geminiReportEntry{
			InputFile:         j.inputPath,
			RawOutputFile:     rawPath,
			FinalOutputFile:   finalPath,
			ProviderRouteUsed: providerRoute,
			ExecutionModeUsed: executionMode,
			SourceImageUsed:   true,
			SizeName:          j.sz.Name,
		}
		if !postprocessEnabled {
			entry.FinalOutputFile = ""
		}
		if !ig.Overwrite {
			if postprocessEnabled {
				if _, err := os.Stat(finalPath); err == nil {
					entry.Status = "skipped"
					reportRows = append(reportRows, entry)
					continue
				}
			} else if _, err := os.Stat(rawPath); err == nil {
				entry.Status = "skipped"
				reportRows = append(reportRows, entry)
				continue
			}
		}
		if !ig.Overwrite {
			if _, err := os.Stat(rawPath); err == nil {
				// reuse raw
				if postprocessEnabled {
					if err := a.finalizeFromRawWithSize(rawPath, finalPath, ff, j.sz.TargetWidth, j.sz.TargetHeight); err != nil {
						entry.Status = "failed"
						entry.Error = err.Error()
					} else {
						entry.Status = "success"
					}
				} else {
					entry.Status = "success"
				}
				reportRows = append(reportRows, entry)
				continue
			}
		}
		imgBytes, err := os.ReadFile(j.inputPath)
		if err != nil {
			entry.Status = "failed"
			entry.Error = fmt.Sprintf("read input: %v", err)
			reportRows = append(reportRows, entry)
			continue
		}
		itemID := fmt.Sprintf("item_%d_%s_%s", i, j.baseName, j.sz.Name)
		entry.BatchItemID = itemID
		meta := map[string]any{"item_id": itemID}
		batchItems = append(batchItems, gemini.BatchRequestItem{
			Prompt:      composeGeminiUserPrompt(ig, j.sz),
			SourceBytes: imgBytes,
			SourcePath:  j.inputPath,
			AspectRatio: j.sz.AspectRatio,
			ImageSize:   imageSize,
			Metadata:    meta,
		})
		jobByItem[itemID] = j
		reportRows = append(reportRows, entry)
	}

	if len(batchItems) == 0 {
		return a.writeGeminiFinalReport(ig.OutputDir, reportRows, postprocessEnabled)
	}

	itemIDs := make([]string, 0, len(batchItems))
	for _, it := range batchItems {
		if v, ok := it.Metadata["item_id"].(string); ok {
			itemIDs = append(itemIDs, v)
		}
	}

	var batchName string
	var createdThisRun bool
	if !ig.Overwrite {
		b, err := findReusableBatchMeta(jobsDir, ig, providerRoute, executionMode, modelName, postprocessEnabled, persistRaw, itemIDs)
		if err != nil {
			return err
		}
		if b != nil {
			batchName = b.BatchJobID
			fmt.Printf("[INFO] Reusing Gemini batch job from metadata: %s\n", batchName)
		}
	}
	if batchName == "" {
		ctx := context.Background()
		name, err := gc.CreateBatch(ctx, "game-asset-pipeline-batch", batchItems)
		if err != nil {
			return err
		}
		batchName = name
		createdThisRun = true
		fmt.Printf("[INFO] Created Gemini batch job: %s\n", batchName)
		meta := persistedBatchMeta{
			BatchJobID:         batchName,
			ModelPreset:        ig.ModelPreset,
			ProviderRoute:      providerRoute,
			ExecutionMode:      executionMode,
			Model:              modelName,
			CreatedAt:          time.Now().Format(time.RFC3339),
			InputDir:           ig.InputDir,
			OutputDir:          ig.OutputDir,
			PostprocessEnabled: postprocessEnabled,
			KeepRaw:            persistRaw,
			RequestCount:       len(batchItems),
			ItemCount:          len(itemIDs),
			ItemIDs:            itemIDs,
			Status:             "submitted",
		}
		metaPath := batchMetaPathForJob(jobsDir, batchName)
		if err := writeJSON(metaPath, meta); err != nil {
			fmt.Printf("[WARN] Failed to persist batch metadata: %v\n", err)
		} else {
			fmt.Printf("[INFO] Wrote batch metadata: %s\n", metaPath)
		}
	}

	if createdThisRun {
		if err := a.appendPendingBatchJobIfMissing(ig.OutputDir, batchName, providerRoute, modelName, ig.ModelPreset); err != nil {
			fmt.Printf("[WARN] Could not write pending batch registry: %v\n", err)
		}
		for i := range reportRows {
			if reportRows[i].Status == "skipped" || reportRows[i].Status == "failed" || reportRows[i].BatchItemID == "" {
				continue
			}
			reportRows[i].BatchJobID = batchName
			reportRows[i].Status = "pending_remote"
		}
		if err := a.writeGeminiFinalReport(ig.OutputDir, reportRows, postprocessEnabled); err != nil {
			return err
		}
		printBatchAsyncFollowUp(batchName, ig.OutputDir)
		return nil
	}

	in, err := a.inspectBatchJobWithMetaDir(batchName, ig.OutputDir)
	if err != nil {
		return err
	}

	terminalFail := in.IsFailed || in.Mapped == "failed" || in.Mapped == "cancelled" || in.Mapped == "expired"
	okInline := in.Mapped == "succeeded" && in.IsSuccess

	switch {
	case okInline:
		if err := a.applyGeminiBatchJobOutputs(gc, batchName, reportRows, jobByItem, postprocessEnabled, persistRaw, ff, jobsDir); err != nil {
			_ = updateBatchMetaStatus(jobsDir, batchName, "fetch_failed")
			return fmt.Errorf("get batch outputs %s: %w", batchName, err)
		}
		if err := a.upsertPendingRegistryTerminal(ig.OutputDir, batchName, "done", "", providerRoute, modelName, ig.ModelPreset); err != nil {
			fmt.Printf("[WARN] Could not update pending batch registry: %v\n", err)
		}
		return a.writeGeminiFinalReport(ig.OutputDir, reportRows, postprocessEnabled)

	case terminalFail:
		_ = updateBatchMetaStatus(jobsDir, batchName, "failed")
		sum := strings.TrimSpace(in.ErrSummary)
		if sum == "" {
			sum = "status=" + in.Mapped
		}
		if err := a.upsertPendingRegistryTerminal(ig.OutputDir, batchName, "failed", sum, providerRoute, modelName, ig.ModelPreset); err != nil {
			fmt.Printf("[WARN] Could not update pending batch registry: %v\n", err)
		}
		for i := range reportRows {
			if reportRows[i].Status == "skipped" || reportRows[i].Status == "failed" || reportRows[i].BatchItemID == "" {
				continue
			}
			reportRows[i].BatchJobID = batchName
			reportRows[i].Status = "failed"
			reportRows[i].Error = sum
		}
		if err := a.writeGeminiFinalReport(ig.OutputDir, reportRows, postprocessEnabled); err != nil {
			return err
		}
		fmt.Printf("\n[INFO] Batch job is in a terminal failure state (no wait/poll was performed beyond this single check).\n")
		return nil

	default:
		if err := a.ensurePendingBatchJobEntry(ig.OutputDir, batchName, providerRoute, modelName, ig.ModelPreset); err != nil {
			fmt.Printf("[WARN] Could not ensure pending batch registry entry: %v\n", err)
		}
		for i := range reportRows {
			if reportRows[i].Status == "skipped" || reportRows[i].Status == "failed" || reportRows[i].BatchItemID == "" {
				continue
			}
			reportRows[i].BatchJobID = batchName
			reportRows[i].Status = "pending_remote"
		}
		if err := a.writeGeminiFinalReport(ig.OutputDir, reportRows, postprocessEnabled); err != nil {
			return err
		}
		printBatchAsyncFollowUp(batchName, ig.OutputDir)
		return nil
	}
}

func printBatchAsyncFollowUp(batchJobID, outputDir string) {
	regPath := pendingBatchRegistryPath(outputDir)
	fmt.Printf("\n[INFO] Async batch: job submitted or still running (no long wait in this CLI run).\n")
	fmt.Printf("  jobId: %s\n", strings.TrimSpace(batchJobID))
	fmt.Printf("  This job is recorded in the pending registry:\n")
	fmt.Printf("    %s\n", regPath)
	fmt.Printf("  When Google marks the batch succeeded, pull results without passing -job-id:\n")
	fmt.Printf("    go run ./cmd/game-asset-pipeline sync-batch-pending -config ./config.json\n")
}

func (a *App) applyGeminiBatchJobOutputs(
	gc *gemini.Client,
	batchName string,
	reportRows []geminiReportEntry,
	jobByItem map[string]geminiJob,
	postprocessEnabled, persistRaw bool,
	ff string,
	jobsDir string,
) error {
	ctx := context.Background()
	jobInfo, outputs, err := gc.GetBatch(ctx, batchName)
	if err != nil {
		return err
	}
	_ = updateBatchMetaStatus(jobsDir, batchName, "succeeded")
	fmt.Printf("[INFO] Gemini batch outputs available: %s state=%s\n", jobInfo.Name, jobInfo.State)

	outByItem := map[string]gemini.BatchOutputItem{}
	for _, o := range outputs {
		if id, ok := o.Metadata["item_id"].(string); ok {
			outByItem[id] = o
		}
	}
	for i := range reportRows {
		if reportRows[i].Status == "skipped" || reportRows[i].Status == "failed" || reportRows[i].BatchItemID == "" {
			continue
		}
		reportRows[i].BatchJobID = batchName
		out, ok := outByItem[reportRows[i].BatchItemID]
		if !ok {
			reportRows[i].Status = "failed"
			reportRows[i].Error = "batch output missing for item"
			continue
		}
		if out.Error != "" {
			reportRows[i].Status = "failed"
			reportRows[i].Error = out.Error
			continue
		}
		if persistRaw {
			if err := writeGeminiOutputPNG(reportRows[i].RawOutputFile, out.Image, out.MimeType); err != nil {
				reportRows[i].Status = "failed"
				reportRows[i].Error = "write raw: " + err.Error()
				continue
			}
		}
		if !postprocessEnabled {
			reportRows[i].FinalOutputFile = ""
			reportRows[i].Status = "success"
			continue
		}
		j, ok := jobByItem[reportRows[i].BatchItemID]
		if !ok {
			reportRows[i].Status = "failed"
			reportRows[i].Error = "missing job mapping for batch item"
			continue
		}
		if err := a.finalizeFromBatchImage(reportRows[i].RawOutputFile, out.Image, reportRows[i].FinalOutputFile, ff, persistRaw, j.sz.TargetWidth, j.sz.TargetHeight); err != nil {
			reportRows[i].Status = "failed"
			reportRows[i].Error = err.Error()
			continue
		}
		reportRows[i].Status = "success"
	}
	return nil
}

func readPersistedMeta(path string) (*persistedBatchMeta, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var v persistedBatchMeta
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

func findReusableBatchMeta(
	jobsDir string,
	ig *model.ImageGenerationSpec,
	providerRoute, executionMode, modelName string,
	postprocessEnabled, keepRaw bool,
	itemIDs []string,
) (*persistedBatchMeta, error) {
	entries, err := os.ReadDir(jobsDir)
	if err != nil {
		return nil, fmt.Errorf("read jobs metadata dir: %w", err)
	}
	var latest *persistedBatchMeta
	for _, e := range entries {
		if e.IsDir() || strings.ToLower(filepath.Ext(e.Name())) != ".json" {
			continue
		}
		p := filepath.Join(jobsDir, e.Name())
		m, err := readPersistedMeta(p)
		if err != nil || m == nil {
			continue
		}
		if !metaMatchesCurrentRun(m, ig, providerRoute, executionMode, modelName, postprocessEnabled, keepRaw, itemIDs) {
			continue
		}
		if latest == nil || strings.Compare(m.CreatedAt, latest.CreatedAt) > 0 {
			tmp := *m
			latest = &tmp
		}
	}
	return latest, nil
}

func metaMatchesCurrentRun(
	m *persistedBatchMeta,
	ig *model.ImageGenerationSpec,
	providerRoute, executionMode, modelName string,
	postprocessEnabled, keepRaw bool,
	itemIDs []string,
) bool {
	if m.BatchJobID == "" || m.ModelPreset != ig.ModelPreset || m.ProviderRoute != providerRoute || m.ExecutionMode != executionMode || m.Model != modelName {
		return false
	}
	if m.InputDir != ig.InputDir || m.OutputDir != ig.OutputDir || m.PostprocessEnabled != postprocessEnabled || m.KeepRaw != keepRaw {
		return false
	}
	if m.ItemCount != len(itemIDs) || m.RequestCount != len(itemIDs) || len(m.ItemIDs) != len(itemIDs) {
		return false
	}
	want := make(map[string]struct{}, len(itemIDs))
	for _, id := range itemIDs {
		want[id] = struct{}{}
	}
	for _, id := range m.ItemIDs {
		if _, ok := want[id]; !ok {
			return false
		}
	}
	switch strings.ToLower(strings.TrimSpace(m.Status)) {
	case "", "submitted", "running", "wait_failed":
		return true
	default:
		return false
	}
}

func batchMetaPathForJob(jobsDir, batchJobID string) string {
	fileName := sanitizeBatchJobID(batchJobID) + ".json"
	return filepath.Join(jobsDir, fileName)
}

func sanitizeBatchJobID(batchJobID string) string {
	s := strings.TrimSpace(batchJobID)
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "?", "_", "*", "_", "\"", "_", "<", "_", ">", "_", "|", "_", " ", "_")
	s = replacer.Replace(s)
	if s == "" {
		return "unknown_batch_job"
	}
	return s
}

func updateBatchMetaStatus(jobsDir, batchJobID, status string) error {
	path := batchMetaPathForJob(jobsDir, batchJobID)
	m, err := readPersistedMeta(path)
	if err != nil || m == nil {
		return err
	}
	m.Status = status
	return writeJSON(path, m)
}

func (a *App) finalizeFromBatchImage(rawPath string, imgBytes []byte, finalPath, ff string, rawPersisted bool, tw, th int) error {
	var src image.Image
	var err error
	if rawPersisted {
		src, err = decodeImageFile(rawPath)
	} else {
		src, _, err = image.Decode(bytes.NewReader(imgBytes))
	}
	if err != nil {
		return fmt.Errorf("decode generated image: %w", err)
	}
	return a.finalizeFromImage(src, finalPath, ff, tw, th)
}

func (a *App) finalizeFromRawWithSize(rawPath, finalPath, ff string, tw, th int) error {
	src, err := decodeImageFile(rawPath)
	if err != nil {
		return fmt.Errorf("decode raw: %w", err)
	}
	return a.finalizeFromImage(src, finalPath, ff, tw, th)
}

func (a *App) finalizeFromImage(src image.Image, finalPath, ff string, tw, th int) error {
	outImg, err := postprocess.FixedSizeCover(src, tw, th)
	if err != nil {
		return fmt.Errorf("postprocess: %w", err)
	}
	return postprocess.WriteFinal(finalPath, outImg, ff, postprocess.DefaultWebPQuality)
}

func (a *App) writeGeminiFinalReport(outputDir string, rows []geminiReportEntry, postprocessEnabled bool) error {
	success, failed, skipped, pendingRemote := 0, 0, 0, 0
	for _, r := range rows {
		switch r.Status {
		case "success":
			success++
		case "failed":
			failed++
		case "skipped":
			skipped++
		case "pending_remote":
			pendingRemote++
		}
	}
	report := geminiBatchReport{
		RunAt:              time.Now().Format(time.RFC3339),
		SuccessCount:       success,
		FailedCount:        failed,
		SkippedCount:       skipped,
		PendingRemoteCount: pendingRemote,
		Entries:            rows,
	}
	reportJSON := filepath.Join(outputDir, "gemini_batch_report.json")
	if err := writeJSON(reportJSON, report); err != nil {
		return err
	}
	reportCSV := filepath.Join(outputDir, "gemini_batch_report.csv")
	if err := writeGeminiReportCSV(reportCSV, rows); err != nil {
		return err
	}
	if postprocessEnabled {
		fmt.Printf("\n[DONE] Gemini batch finished (batch API + postprocess).\n")
	} else {
		fmt.Printf("\n[DONE] Gemini batch finished (batch API raw-only).\n")
	}
	fmt.Printf("  success=%d failed=%d skipped=%d pending_remote=%d\n", success, failed, skipped, pendingRemote)
	fmt.Printf("  report_json=%s\n", reportJSON)
	fmt.Printf("  report_csv=%s\n", reportCSV)
	return nil
}
