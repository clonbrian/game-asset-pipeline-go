package app

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"game-asset-pipeline-go/internal/extractors"
	"game-asset-pipeline-go/internal/imagex"
	"game-asset-pipeline-go/internal/matcher"
	"game-asset-pipeline-go/internal/model"
	"game-asset-pipeline-go/internal/util"
)

// needsAIInfo tracks information for games that need AI outpainting
type needsAIInfo struct {
	provider     string
	slug         string
	gameName     string
	candidateURL string
	origW        int
	origH        int
	minScale     float64
	triggerSizes []string
}

func (a *App) RunOnce() error {
	cfg := a.Cfg

	if err := os.MkdirAll(cfg.WorkDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return err
	}

	providers, err := loadProviders(cfg.ProvidersPath)
	if err != nil {
		return err
	}
	games, err := loadGames(cfg.GamesPath)
	if err != nil {
		return err
	}

	// Build provider->candidates cache
	providerCands := map[string][]model.AssetCandidate{}
	providerHeaders := map[string]map[string]string{}
	for _, p := range providers {
		if !p.Enabled {
			continue
		}
		providerHeaders[p.Provider] = p.Headers
		ex, err := extractors.GetExtractor(p.SourceType)
		if err != nil {
			return err
		}
		var body []byte
		var ctype string
		if p.SourceType == "local_dir" {
			// Skip HTTP download for local_dir
			body = nil
			ctype = ""
		} else {
			body, ctype, err = a.DL.GetBytes(p.SourceURL, p.Headers)
			if err != nil {
				fmt.Printf("[WARN] provider fetch failed %s: %v\n", p.SourceURL, err)
				continue
			}
		}
		cands, err := ex.Extract(p, body, ctype)
		if err != nil {
			fmt.Printf("[WARN] extractor failed %s: %v\n", p.SourceURL, err)
			continue
		}
		if len(cands) > cfg.Extract.MaxAssetsPerProvider {
			cands = cands[:cfg.Extract.MaxAssetsPerProvider]
		}
		providerCands[p.Provider] = append(providerCands[p.Provider], cands...)
		fmt.Printf("[INFO] provider=%s candidates=%d\n", p.Provider, len(cands))
	}

	// Prepare output folders
	outAssetsRoot := filepath.Join(cfg.OutputDir, "assets_by_game")
	outReviewDir := filepath.Join(cfg.OutputDir, "review")
	outManifestsDir := filepath.Join(cfg.OutputDir, "manifests")
	outZipDir := filepath.Join(cfg.OutputDir, "zip")
	_ = os.MkdirAll(outAssetsRoot, 0o755)
	_ = os.MkdirAll(outReviewDir, 0o755)
	_ = os.MkdirAll(outManifestsDir, 0o755)
	_ = os.MkdirAll(outZipDir, 0o755)

	reviewCSVPath := filepath.Join(outReviewDir, "review.csv")
	needsEditCSVPath := filepath.Join(outReviewDir, "needs_edit.csv")

	reviewRows := [][]string{{"provider", "game_name", "slug", "score", "reason", "candidate_url", "title", "alt", "needs_edit", "status", "error"}}
	needsEditRows := [][]string{{"provider", "game_name", "slug", "candidate_url", "title", "alt", "reason"}}

	// Track needs_ai information
	// Threshold: if scale < 0.75 for any target size, mark as needs_ai
	// This means the foreground image is too small relative to the target size,
	// requiring too much background fill, which suggests AI outpainting would be better.
	// To adjust the threshold, change the scaleThreshold constant below.
	const scaleThreshold = 0.75
	var needsAIList []needsAIInfo

	manifest := map[string]any{
		"run_at":         time.Now().Format(time.RFC3339),
		"providers_path": cfg.ProvidersPath,
		"games_path":     cfg.GamesPath,
		"output_dir":     cfg.OutputDir,
		"work_dir":       cfg.WorkDir,
		"sizes":          cfg.Sizes,
		"results":        []any{},
	}

	for _, g := range games {
		cands := providerCands[g.Provider]
		best, score, reason := matcher.BestMatch(g, cands)

		resObj := map[string]any{
			"provider":  g.Provider,
			"game_name": g.GameName,
			"slug":      g.OutputSlug,
			"score":     score,
			"reason":    reason,
		}

		if best == nil || score < cfg.Extract.MinimumMatchScore {
			errMsg := "no good match"
			reviewRows = append(reviewRows, []string{
				g.Provider, g.GameName, g.OutputSlug,
				fmt.Sprintf("%d", score), reason,
				val(best, "URL"), val(best, "Title"), val(best, "Alt"),
				"false", "FAIL", errMsg,
			})
			resObj["status"] = "FAIL"
			resObj["error"] = errMsg
			manifest["results"] = appendAny(manifest["results"], resObj)
			continue
		}

		needsEdit := util.ContainsNonBasicLatin(best.Title) || util.ContainsNonBasicLatin(best.Alt)
		if needsEdit {
			needsEditRows = append(needsEditRows, []string{
				g.Provider, g.GameName, g.OutputSlug,
				best.URL, best.Title, best.Alt, "non-basic-latin detected in title/alt",
			})
		}

		// Download master image
		masterName := best.FileName
		if masterName == "" {
			masterName = g.OutputSlug + "_" + util.HashShort(best.URL) + ".img"
		}
		masterPath := filepath.Join(cfg.WorkDir, "downloads", g.Provider, masterName)

		// Check if best.URL is a local path (not http:// or https://)
		isLocalPath := !strings.HasPrefix(strings.ToLower(best.URL), "http://") && !strings.HasPrefix(strings.ToLower(best.URL), "https://")
		if isLocalPath {
			// If it's a local path and file exists, use it directly
			if _, err := os.Stat(best.URL); err == nil {
				// File exists, copy it to masterPath
				if err := copyFile(best.URL, masterPath); err != nil {
					reviewRows = append(reviewRows, []string{
						g.Provider, g.GameName, g.OutputSlug,
						fmt.Sprintf("%d", score), reason,
						best.URL, best.Title, best.Alt,
						fmt.Sprintf("%v", needsEdit), "FAIL", "copy local file failed: " + err.Error(),
					})
					resObj["status"] = "FAIL"
					resObj["error"] = "copy local file failed: " + err.Error()
					manifest["results"] = appendAny(manifest["results"], resObj)
					continue
				}
			} else {
				// Local file doesn't exist
				reviewRows = append(reviewRows, []string{
					g.Provider, g.GameName, g.OutputSlug,
					fmt.Sprintf("%d", score), reason,
					best.URL, best.Title, best.Alt,
					fmt.Sprintf("%v", needsEdit), "FAIL", "local file does not exist: " + best.URL,
				})
				resObj["status"] = "FAIL"
				resObj["error"] = "local file does not exist: " + best.URL
				manifest["results"] = appendAny(manifest["results"], resObj)
				continue
			}
		} else {
			// HTTP download
			hdr := providerHeaders[g.Provider]
			if err := a.DL.DownloadToFile(best.URL, hdr, masterPath); err != nil {
				reviewRows = append(reviewRows, []string{
					g.Provider, g.GameName, g.OutputSlug,
					fmt.Sprintf("%d", score), reason,
					best.URL, best.Title, best.Alt,
					fmt.Sprintf("%v", needsEdit), "FAIL", "download failed: " + err.Error(),
				})
				resObj["status"] = "FAIL"
				resObj["error"] = "download failed: " + err.Error()
				manifest["results"] = appendAny(manifest["results"], resObj)
				continue
			}
		}

		// Output per game
		gameDir := filepath.Join(outAssetsRoot, g.OutputSlug)
		_ = os.MkdirAll(gameDir, 0o755)

		// Get original image dimensions
		origW, origH, err := imagex.GetImageDimensions(masterPath)
		if err != nil {
			fmt.Printf("[WARN] needs_ai: cannot read dimensions for %s err=%v\n", masterPath, err)
			// Continue without needs_ai detection for this game
			origW, origH = 0, 0
		}

		var outputs []string
		ffmpegFailed := false
		var minScale float64 = 1.0
		var triggerSizes []string
		var scale210, scale325, scale294 float64

		for _, sz := range cfg.Sizes {
			outName := fmt.Sprintf("%s_%dx%d.webp", g.OutputSlug, sz.Width, sz.Height)
			outPath := filepath.Join(gameDir, outName)

			if err := imagex.ToWebPScaledFFmpeg(masterPath, outPath, sz.Width, sz.Height, 85); err != nil {
				reviewRows = append(reviewRows, []string{
					g.Provider, g.GameName, g.OutputSlug,
					fmt.Sprintf("%d", score), reason,
					best.URL, best.Title, best.Alt,
					fmt.Sprintf("%v", needsEdit), "FAIL", "ffmpeg webp failed: " + err.Error(),
				})
				resObj["status"] = "FAIL"
				resObj["error"] = "ffmpeg webp failed: " + err.Error()
				manifest["results"] = appendAny(manifest["results"], resObj)
				ffmpegFailed = true
				break
			}

			outputs = append(outputs, outPath)

			// Calculate scale factor for this size (foreground fit scale)
			// For each target size (W,H):
			//   scaleW = float64(W) / float64(origW)
			//   scaleH = float64(H) / float64(origH)
			//   scale = min(scaleW, scaleH)
			// This scale must be <= 1 for downscaling (do NOT invert it).
			if origW > 0 && origH > 0 {
				scaleW := float64(sz.Width) / float64(origW)
				scaleH := float64(sz.Height) / float64(origH)
				scale := math.Min(scaleW, scaleH)
				if scale < minScale {
					minScale = scale
				}
				if scale < scaleThreshold {
					triggerSizes = append(triggerSizes, fmt.Sprintf("%dx%d", sz.Width, sz.Height))
				}
				// Store scale for each target size for debug output
				if sz.Width == 210 && sz.Height == 210 {
					scale210 = scale
				} else if sz.Width == 325 && sz.Height == 234 {
					scale325 = scale
				} else if sz.Width == 294 && sz.Height == 400 {
					scale294 = scale
				}
			}
		}
		if ffmpegFailed {
			continue
		}

		// Always print debug for every game/slug after computing scales
		if origW > 0 && origH > 0 {
			triggerSizesStr := strings.Join(triggerSizes, ",")
			if triggerSizesStr == "" {
				triggerSizesStr = "none"
			}
			candidateFilename := best.FileName
			if candidateFilename == "" {
				candidateFilename = filepath.Base(best.URL)
			}
			fmt.Printf("[DEBUG] needs_ai slug=%s candidate=%s candidate_size=%dx%d orig=%dx%d scales=210:%.3f 325:%.3f 294:%.3f min=%.3f triggers=%s\n",
				g.OutputSlug, candidateFilename, best.Width, best.Height, origW, origH, scale210, scale325, scale294, minScale, triggerSizesStr)
		}

		// Check if needs_ai (scale < threshold for any size)
		// If any scale < 0.75, mark needs_ai and record that size in trigger_sizes.
		if origW > 0 && origH > 0 && minScale < scaleThreshold {
			needsAIList = append(needsAIList, needsAIInfo{
				provider:     g.Provider,
				slug:         g.OutputSlug,
				gameName:     g.GameName,
				candidateURL: best.URL,
				origW:        origW,
				origH:        origH,
				minScale:     minScale,
				triggerSizes: triggerSizes,
			})
		}

		// per-game manifest
		perManifest := map[string]any{
			"provider":      g.Provider,
			"game_name":     g.GameName,
			"english_title": g.EnglishTitle,
			"slug":          g.OutputSlug,
			"candidate": map[string]any{
				"url":       best.URL,
				"title":     best.Title,
				"alt":       best.Alt,
				"file_name": best.FileName,
				"score":     score,
				"reason":    reason,
			},
			"needs_edit": needsEdit,
			"outputs":    outputs,
		}
		pmPath := filepath.Join(gameDir, "manifest.json")
		_ = writeJSON(pmPath, perManifest)

		reviewRows = append(reviewRows, []string{
			g.Provider, g.GameName, g.OutputSlug,
			fmt.Sprintf("%d", score), reason,
			best.URL, best.Title, best.Alt,
			fmt.Sprintf("%v", needsEdit), "OK", "",
		})
		resObj["status"] = "OK"
		resObj["candidate_url"] = best.URL
		resObj["needs_edit"] = needsEdit
		resObj["outputs"] = outputs
		manifest["results"] = appendAny(manifest["results"], resObj)
	}

	// Write review CSVs
	if err := writeCSV(reviewCSVPath, reviewRows); err != nil {
		return err
	}
	if err := writeCSV(needsEditCSVPath, needsEditRows); err != nil {
		return err
	}

	// Write needs_ai.csv and generate prompts
	if err := a.writeNeedsAI(outReviewDir, needsAIList); err != nil {
		fmt.Printf("[WARN] failed to write needs_ai.csv: %v\n", err)
	}

	// Write run manifest
	runManifestPath := filepath.Join(outManifestsDir, "run_manifest.json")
	if err := writeJSON(runManifestPath, manifest); err != nil {
		return err
	}

	// Zip output/assets_by_game
	ts := time.Now().Format("20060102_150405")
	zipPath := filepath.Join(outZipDir, fmt.Sprintf("game_assets_%s.zip", ts))
	if err := ZipFolder(outAssetsRoot, zipPath); err != nil {
		return err
	}

	fmt.Printf("[DONE] review=%s needs_edit=%s manifest=%s zip=%s\n",
		reviewCSVPath, needsEditCSVPath, runManifestPath, zipPath)
	return nil
}

func loadProviders(path string) ([]model.ProviderSource, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var v []model.ProviderSource
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return v, nil
}

func loadGames(path string) ([]model.GameSpec, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var v []model.GameSpec
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	// normalize slugs if empty
	for i := range v {
		if v[i].OutputSlug == "" {
			v[i].OutputSlug = util.SafeSlug(v[i].GameName)
		}
	}
	return v, nil
}

func writeCSV(path string, rows [][]string) error {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	for _, r := range rows {
		if err := w.Write(r); err != nil {
			return err
		}
	}
	return nil
}

func writeJSON(path string, v any) error {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func appendAny(cur any, v any) any {
	if cur == nil {
		return []any{v}
	}
	if s, ok := cur.([]any); ok {
		return append(s, v)
	}
	return []any{v}
}

func val(c *model.AssetCandidate, field string) string {
	if c == nil {
		return ""
	}
	switch field {
	case "URL":
		return c.URL
	case "Title":
		return c.Title
	case "Alt":
		return c.Alt
	default:
		return ""
	}
}

func (a *App) writeNeedsAI(outReviewDir string, needsAIList []needsAIInfo) error {
	// Always write needs_ai.csv with header, even if 0 entries
	needsAICSVPath := filepath.Join(outReviewDir, "needs_ai.csv")
	needsAIRows := [][]string{
		{"provider", "slug", "game_name", "candidate_url_or_path", "orig_w", "orig_h", "min_scale", "trigger_sizes"},
	}

	for _, item := range needsAIList {
		triggerSizesStr := strings.Join(item.triggerSizes, ";")
		needsAIRows = append(needsAIRows, []string{
			item.provider,
			item.slug,
			item.gameName,
			item.candidateURL,
			fmt.Sprintf("%d", item.origW),
			fmt.Sprintf("%d", item.origH),
			fmt.Sprintf("%.3f", item.minScale),
			triggerSizesStr,
		})
	}

	if err := writeCSV(needsAICSVPath, needsAIRows); err != nil {
		return fmt.Errorf("write needs_ai.csv: %w", err)
	}

	// Always create output/prompts/ directory
	cfg := a.Cfg
	outPromptsDir := filepath.Join(cfg.OutputDir, "prompts")
	if err := os.MkdirAll(outPromptsDir, 0o755); err != nil {
		return fmt.Errorf("create prompts directory: %w", err)
	}

	// Generate prompts for each needs_ai slug (only if n > 0)
	for _, item := range needsAIList {
		if err := a.GeneratePrompts(item.slug); err != nil {
			fmt.Printf("[WARN] failed to generate prompts for %s: %v\n", item.slug, err)
			// Continue with other slugs
		}
	}

	fmt.Printf("[INFO] needs_ai.csv written: %s (%d entries)\n", needsAICSVPath, len(needsAIList))
	return nil
}

func copyFile(src, dst string) error {
	_ = os.MkdirAll(filepath.Dir(dst), 0o755)
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return err
	}
	return destFile.Sync()
}
