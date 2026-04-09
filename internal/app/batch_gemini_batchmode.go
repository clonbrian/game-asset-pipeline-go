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
	"time"

	"game-asset-pipeline-go/internal/imagegen/gemini"
	"game-asset-pipeline-go/internal/imagegen/postprocess"
	"game-asset-pipeline-go/internal/model"
)

type persistedBatchMeta struct {
	PresetKey  string   `json:"presetKey"`
	Model      string   `json:"model"`
	BatchName  string   `json:"batchName"`
	ItemIDs    []string `json:"itemIDs"`
	CreatedAt  string   `json:"createdAt"`
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
	metaPath := filepath.Join(ig.OutputDir, "gemini_batch_job_meta.json")
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
	if !ig.Overwrite {
		if b, err := readPersistedMeta(metaPath); err == nil && b != nil && b.PresetKey == ig.ModelPreset && b.Model == modelName {
			batchName = b.BatchName
			fmt.Printf("[INFO] Reusing existing Gemini batch job metadata: %s\n", batchName)
		}
	}
	if batchName == "" {
		ctx := context.Background()
		name, err := gc.CreateBatch(ctx, "game-asset-pipeline-batch", batchItems)
		if err != nil {
			return err
		}
		batchName = name
		fmt.Printf("[INFO] Created Gemini batch job: %s\n", batchName)
		_ = writeJSON(metaPath, persistedBatchMeta{
			PresetKey: ig.ModelPreset,
			Model:     modelName,
			BatchName: batchName,
			ItemIDs:   itemIDs,
			CreatedAt: time.Now().Format(time.RFC3339),
		})
	}

	ctx := context.Background()
	jobInfo, outputs, err := gc.WaitBatch(ctx, batchName, 10*time.Second)
	if err != nil {
		return fmt.Errorf("wait batch %s failed: %w", batchName, err)
	}
	fmt.Printf("[INFO] Gemini batch completed: %s state=%s\n", jobInfo.Name, jobInfo.State)

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
	return a.writeGeminiFinalReport(ig.OutputDir, reportRows, postprocessEnabled)
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
	success, failed, skipped := 0, 0, 0
	for _, r := range rows {
		switch r.Status {
		case "success":
			success++
		case "failed":
			failed++
		case "skipped":
			skipped++
		}
	}
	report := geminiBatchReport{
		RunAt:        time.Now().Format(time.RFC3339),
		SuccessCount: success,
		FailedCount:  failed,
		SkippedCount: skipped,
		Entries:      rows,
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
	fmt.Printf("  success=%d failed=%d skipped=%d\n", success, failed, skipped)
	fmt.Printf("  report_json=%s\n", reportJSON)
	fmt.Printf("  report_csv=%s\n", reportCSV)
	return nil
}
