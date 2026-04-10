package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"game-asset-pipeline-go/internal/imagegen/gemini"
)

const unknownFromAPIOrMeta = "(unknown from API/metadata)"

// batchJobInspect is one batches.get via FetchBatchJobStatus + local metadata (shared by check-batch-job / sync-batch-job).
type batchJobInspect struct {
	CanonicalID   string
	OutputDir     string
	Status        *gemini.BatchJobStatus
	RawBody       []byte
	MetadataPath  string
	Meta          *batchJobMetaFile
	Mapped        string
	IsDone        bool
	IsSuccess     bool
	IsFailed      bool
	ErrSummary    string
	ModelDisplay  string
	ExecDisplay   string
	PresetDisplay string
	ProvDisplay   string
	JobIDDisplay  string
}

func (a *App) inspectBatchJob(jobID string) (*batchJobInspect, error) {
	return a.inspectBatchJobWithMetaDir(jobID, "")
}

// inspectBatchJobWithMetaDir runs batches.get and resolves local metadata under metadataOutputDir when non-empty;
// otherwise uses config.imageGeneration.outputDir.
func (a *App) inspectBatchJobWithMetaDir(jobID string, metadataOutputDir string) (*batchJobInspect, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return nil, fmt.Errorf(`-job-id is required (example: -job-id "batches/abc123")`)
	}
	canonicalID, err := gemini.ValidateAndNormalizeBatchJobID(jobID)
	if err != nil {
		return nil, fmt.Errorf("invalid batch job id: %w", err)
	}

	ig := a.Cfg.ImageGeneration
	if ig == nil {
		return nil, fmt.Errorf("config.imageGeneration is missing")
	}
	apiKey := strings.TrimSpace(os.Getenv(ig.APIKeyEnv))
	if apiKey == "" {
		return nil, fmt.Errorf("missing API key: environment variable %q is empty (set it to your Gemini API key)", ig.APIKeyEnv)
	}

	metaOut := strings.TrimSpace(metadataOutputDir)
	if metaOut == "" {
		metaOut = ig.OutputDir
	}
	metadataPath, meta := findBatchJobMetadata(metaOut, canonicalID)

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
	res, err := gc.FetchBatchJobStatus(ctx, canonicalID)
	if err != nil {
		var ge *gemini.BatchGETError
		if errors.As(err, &ge) {
			fmt.Fprintf(os.Stderr, "[ERROR] Gemini Batch API returned HTTP %d\n", ge.StatusCode)
			fmt.Fprintf(os.Stderr, "response body (truncated):\n%s\n", truncateForLog(string(ge.Body), 2000))
			if ge.StatusCode == http.StatusNotFound {
				return nil, fmt.Errorf("batch job not found: no resource for job id %q (HTTP 404); verify the id and that the API key matches the project that created the job", canonicalID)
			}
			return nil, fmt.Errorf("gemini batch GET failed with HTTP %d", ge.StatusCode)
		}
		if res != nil && len(res.ResponseBody) > 0 {
			p, werr := writeCheckBatchDebugFile(metaOut, canonicalID, res.ResponseBody)
			if werr == nil {
				fmt.Fprintf(os.Stderr, "[WARN] Failed to parse API response; raw body saved to %s\n", p)
			}
		}
		return nil, fmt.Errorf("check batch job: %w", err)
	}

	st := res.Status
	if st == nil {
		return nil, fmt.Errorf("internal error: empty batch status after successful GET")
	}

	kind, isDone, isSuccess, isFailed := mapBatchLifecycle(st.State)
	errMsg := strings.TrimSpace(st.ErrorMessage)
	if isFailed && errMsg == "" && strings.TrimSpace(st.State) != "" {
		errMsg = "state=" + strings.TrimSpace(st.State)
	}

	return &batchJobInspect{
		CanonicalID:  canonicalID,
		OutputDir:    metaOut,
		Status:       st,
		RawBody:      res.ResponseBody,
		MetadataPath: metadataPath,
		Meta:         meta,
		Mapped:       kind,
		IsDone:       isDone,
		IsSuccess:    isSuccess,
		IsFailed:     isFailed,
		ErrSummary:   errMsg,
		ModelDisplay: pickModelDisplay(st.Model, meta),
		ExecDisplay:  pickMetaString(meta, func(m *batchJobMetaFile) string { return m.ExecutionMode }, unknownFromAPIOrMeta),
		PresetDisplay: pickMetaString(meta, func(m *batchJobMetaFile) string {
			if s := strings.TrimSpace(m.ModelPreset); s != "" {
				return s
			}
			return strings.TrimSpace(m.PresetKey)
		}, unknownFromAPIOrMeta),
		ProvDisplay:  pickMetaString(meta, func(m *batchJobMetaFile) string { return m.ProviderRoute }, unknownFromAPIOrMeta),
		JobIDDisplay: firstNonEmpty(strings.TrimSpace(st.Name), canonicalID),
	}, nil
}

// CheckBatchJob queries the Gemini Batch API for the given job id and prints status (API is authoritative).
func (a *App) CheckBatchJob(jobID string, debug bool) error {
	in, err := a.inspectBatchJob(jobID)
	if err != nil {
		return err
	}
	st := in.Status

	needDebugDump := debug || strings.TrimSpace(st.State) == ""
	if needDebugDump && len(in.RawBody) > 0 {
		p, werr := writeCheckBatchDebugFile(in.OutputDir, in.CanonicalID, in.RawBody)
		if werr != nil {
			fmt.Fprintf(os.Stderr, "[WARN] could not write debug response file: %v\n", werr)
		} else {
			if debug {
				fmt.Fprintf(os.Stderr, "[DEBUG] full API response saved to %s\n", p)
			} else {
				fmt.Fprintf(os.Stderr, "[WARN] could not resolve state from API; raw response saved to %s\n", p)
			}
		}
	}
	if debug && len(in.RawBody) > 0 {
		var pretty bytes.Buffer
		if json.Indent(&pretty, in.RawBody, "", "  ") == nil {
			fmt.Fprintf(os.Stderr, "[DEBUG] API response JSON:\n%s\n", pretty.String())
		} else {
			fmt.Fprintf(os.Stderr, "[DEBUG] API response (raw):\n%s\n", string(in.RawBody))
		}
	}

	fmt.Println("[INFO] Batch job status (API is authoritative; config is not shown as job facts)")
	fmt.Printf("  jobId          : %s\n", in.JobIDDisplay)
	fmt.Printf("  state (raw)    : %s\n", emptyAsDash(strings.TrimSpace(st.State)))
	fmt.Printf("  status (mapped): %s\n", in.Mapped)
	fmt.Printf("  providerRoute  : %s\n", in.ProvDisplay)
	fmt.Printf("  model          : %s\n", in.ModelDisplay)
	fmt.Printf("  executionMode  : %s\n", in.ExecDisplay)
	fmt.Printf("  modelPreset    : %s\n", in.PresetDisplay)
	printTimeField("  createdAt      : ", st.CreateTime)
	printTimeField("  updatedAt      : ", st.UpdateTime)
	printTimeField("  completedAt    : ", st.CompletedTime)
	fmt.Printf("  isDone         : %v\n", in.IsDone)
	fmt.Printf("  isSuccess      : %v\n", in.IsSuccess)
	fmt.Printf("  isFailed       : %v\n", in.IsFailed)
	if in.MetadataPath != "" {
		fmt.Printf("  metadata       : %s\n", in.MetadataPath)
	} else {
		fmt.Printf("  metadata       : (none found under outputDir for this job id)\n")
	}
	if in.ErrSummary != "" {
		fmt.Printf("  errorMessage   : %s\n", in.ErrSummary)
	}

	if in.Mapped == "running" {
		if warn := batchStaleWarning(st.CreateTime, st.UpdateTime); warn != "" {
			fmt.Printf("[WARN] %s\n", warn)
		}
	}

	return nil
}

func emptyAsDash(s string) string {
	if s == "" {
		return "(empty)"
	}
	return s
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func pickModelDisplay(apiModel string, meta *batchJobMetaFile) string {
	if strings.TrimSpace(apiModel) != "" {
		return strings.TrimSpace(apiModel) + " (from API)"
	}
	if meta != nil && strings.TrimSpace(meta.Model) != "" {
		return strings.TrimSpace(meta.Model) + " (from metadata)"
	}
	return unknownFromAPIOrMeta
}

func pickMetaString(meta *batchJobMetaFile, get func(*batchJobMetaFile) string, unknown string) string {
	if meta == nil {
		return unknown
	}
	v := strings.TrimSpace(get(meta))
	if v == "" {
		return unknown
	}
	return v + " (from metadata)"
}

func writeCheckBatchDebugFile(outputDir, canonicalJobID string, body []byte) (string, error) {
	dir := filepath.Join(outputDir, "check_batch_debug")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	safe := strings.ReplaceAll(canonicalJobID, "/", "_")
	safe = strings.ReplaceAll(safe, "\\", "_")
	safe = strings.ReplaceAll(safe, ":", "_")
	outPath := filepath.Join(dir, safe+"_response.json")
	var buf bytes.Buffer
	toWrite := body
	if err := json.Indent(&buf, body, "", "  "); err == nil {
		toWrite = buf.Bytes()
	} else {
		toWrite = append([]byte(nil), body...)
	}
	if err := os.WriteFile(outPath, toWrite, 0o644); err != nil {
		return "", err
	}
	return outPath, nil
}

type batchJobMetaFile struct {
	BatchJobID    string `json:"batchJobId"`
	BatchName     string `json:"batchName"`
	ModelPreset   string `json:"modelPreset"`
	PresetKey     string `json:"presetKey"`
	ExecutionMode string `json:"executionMode"`
	Model         string `json:"model"`
	ProviderRoute string `json:"providerRoute"`
}

func normBatchIDForMatch(id string) string {
	s := strings.TrimSpace(id)
	s = strings.TrimPrefix(s, "/")
	s = strings.TrimPrefix(strings.ToLower(s), "batches/")
	return s
}

func batchMetaMatches(canonicalJobID string, m *batchJobMetaFile) bool {
	if m == nil {
		return false
	}
	canon := normBatchIDForMatch(canonicalJobID)
	if canon == "" {
		return false
	}
	for _, c := range []string{m.BatchJobID, m.BatchName} {
		if normBatchIDForMatch(c) == canon {
			return true
		}
		if strings.TrimSpace(c) == strings.TrimSpace(canonicalJobID) {
			return true
		}
	}
	return false
}

// findBatchJobMetadata scans outputDir/jobs/*.json and legacy gemini_batch_job_meta.json.
func findBatchJobMetadata(outputDir, canonicalJobID string) (path string, meta *batchJobMetaFile) {
	jobsDir := filepath.Join(outputDir, "jobs")
	if entries, err := os.ReadDir(jobsDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".json") {
				continue
			}
			p := filepath.Join(jobsDir, e.Name())
			m := readBatchMetaFile(p)
			if m != nil && batchMetaMatches(canonicalJobID, m) {
				return p, m
			}
		}
	}
	legacy := filepath.Join(outputDir, "gemini_batch_job_meta.json")
	if m := readBatchMetaFile(legacy); m != nil && batchMetaMatches(canonicalJobID, m) {
		return legacy, m
	}
	return "", nil
}

func readBatchMetaFile(p string) *batchJobMetaFile {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var m batchJobMetaFile
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return &m
}

func printTimeField(prefix, v string) {
	v = strings.TrimSpace(v)
	if v == "" {
		fmt.Printf("%s(n/a)\n", prefix)
		return
	}
	fmt.Printf("%s%s\n", prefix, v)
}

func truncateForLog(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "\n...[truncated]"
}

// mapBatchLifecycle maps vendor state strings to a small vocabulary and boolean flags.
func mapBatchLifecycle(rawState string) (kind string, isDone, isSuccess, isFailed bool) {
	u := strings.ToUpper(strings.TrimSpace(rawState))
	u = strings.ReplaceAll(u, " ", "_")
	if u == "" {
		return "unknown", false, false, false
	}
	if strings.Contains(u, "INCOMPLETE") {
		return "unknown", false, false, false
	}
	if strings.Contains(u, "SUCCEEDED") || u == "COMPLETED" || strings.HasSuffix(u, "_COMPLETED") {
		return "succeeded", true, true, false
	}
	if strings.Contains(u, "CANCELLED") || strings.Contains(u, "CANCELED") {
		return "cancelled", true, false, true
	}
	if strings.Contains(u, "EXPIRED") {
		return "expired", true, false, true
	}
	if strings.Contains(u, "FAILED") {
		return "failed", true, false, true
	}
	if strings.Contains(u, "RUNNING") || strings.Contains(u, "PROCESSING") || strings.Contains(u, "PENDING") {
		return "running", false, false, false
	}
	return "unknown", false, false, false
}

func batchStaleWarning(createTime, updateTime string) string {
	t := parseGoogleTime(createTime)
	if t.IsZero() {
		t = parseGoogleTime(updateTime)
	}
	if t.IsZero() {
		return ""
	}
	if time.Since(t) < 24*time.Hour {
		return ""
	}
	return "batch job still in a non-terminal state after 24+ hours since last known create/update timestamp; check Google console or retry later"
}

func parseGoogleTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
