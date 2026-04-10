package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// BatchJobStatus is a lightweight view of a batch job from GET (no output image decoding).
type BatchJobStatus struct {
	Name          string
	State         string
	CreateTime    string
	UpdateTime    string
	CompletedTime string // completedTime / endTime / doneTime / API endTime
	DisplayName   string
	Model         string
	ErrorMessage  string
}

type batchJobGetLight struct {
	Name          string          `json:"name"`
	State         string          `json:"state"`
	CreateTime    string          `json:"createTime"`
	UpdateTime    string          `json:"updateTime"`
	DisplayName   string          `json:"displayName"`
	Model         string          `json:"model"`
	CompletedTime string          `json:"completedTime"`
	EndTime       string          `json:"endTime"`
	DoneTime      string          `json:"doneTime"`
	Error         json.RawMessage `json:"error"`
	Output        json.RawMessage `json:"output"`
}

// BatchFetchResult is the parsed status plus the raw GET response body (for debug dumps).
type BatchFetchResult struct {
	Status       *BatchJobStatus
	ResponseBody []byte
}

// BatchGETError is returned when GET batches/{id} returns a non-2xx status.
type BatchGETError struct {
	StatusCode int
	Body       []byte
}

func (e *BatchGETError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("gemini batch GET failed: HTTP %d: %s", e.StatusCode, summarizeAPIError(e.Body, e.StatusCode))
}

// FetchBatchJobStatus calls GET on the batch resource and parses Operation-wrapped or flat batch JSON.
func (c *Client) FetchBatchJobStatus(ctx context.Context, batchJobID string) (*BatchFetchResult, error) {
	if c.APIKey == "" {
		return nil, fmt.Errorf("gemini: missing API key")
	}
	id, err := ValidateAndNormalizeBatchJobID(batchJobID)
	if err != nil {
		return nil, err
	}
	u := c.batchURL(id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-goog-api-key", c.APIKey)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &BatchGETError{StatusCode: resp.StatusCode, Body: body}
	}
	st, perr := ParseBatchGetResponse(body)
	res := &BatchFetchResult{Status: st, ResponseBody: body}
	if perr != nil {
		return res, fmt.Errorf("parse batch GET response: %w", perr)
	}
	return res, nil
}

// ParseBatchGetResponse maps batches.get JSON to BatchJobStatus.
// Official batches.get returns a long-running Operation: batch fields usually live under "metadata",
// not at the top level (top-level "state" is often empty).
func ParseBatchGetResponse(body []byte) (*BatchJobStatus, error) {
	var root map[string]interface{}
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("parse batch GET json: %w", err)
	}

	out := &BatchJobStatus{}

	// Primary: GenerateContentBatch object (flat GET or LRO metadata / response).
	batchBytes := extractBatchResourceJSON(body)
	var batchPart batchJobGetLight
	if err := json.Unmarshal(batchBytes, &batchPart); err != nil {
		return nil, fmt.Errorf("parse batch resource: %w", err)
	}
	out.absorbLight(batchPart)

	// LRO "response" may carry additional fields when done.
	if respRaw, ok := root["response"]; ok && respRaw != nil {
		rb, err := json.Marshal(respRaw)
		if err == nil {
			var respPart batchJobGetLight
			if json.Unmarshal(rb, &respPart) == nil {
				out.absorbLightFillEmpty(respPart)
			}
		}
	}

	// Top-level operation name (often same as batch name).
	var topLight batchJobGetLight
	_ = json.Unmarshal(body, &topLight)
	if strings.TrimSpace(out.Name) == "" {
		out.Name = strings.TrimSpace(topLight.Name)
	}

	out.CompletedTime = firstNonEmpty(
		strings.TrimSpace(out.CompletedTime),
		strings.TrimSpace(batchPart.EndTime),
		strings.TrimSpace(batchPart.DoneTime),
		strings.TrimSpace(batchPart.CompletedTime),
	)

	if len(batchPart.Error) > 0 && string(batchPart.Error) != "null" {
		if msg := summarizeEmbeddedError(batchPart.Error); msg != "" {
			if out.ErrorMessage == "" {
				out.ErrorMessage = msg
			}
		}
		if strings.TrimSpace(out.State) == "" {
			out.State = "FAILED"
		}
	}

	// LRO terminal error (operation-level).
	if errRaw, ok := root["error"]; ok && errRaw != nil {
		eb, _ := json.Marshal(errRaw)
		if len(eb) > 0 && string(eb) != "null" {
			if msg := summarizeEmbeddedError(eb); msg != "" {
				if out.ErrorMessage == "" {
					out.ErrorMessage = msg
				}
			}
			out.State = "FAILED"
		}
	}

	// Infer from "done" when state still unknown.
	if doneVal, ok := root["done"].(bool); ok && strings.TrimSpace(out.State) == "" {
		if doneVal {
			if strings.TrimSpace(out.ErrorMessage) == "" {
				out.State = "SUCCEEDED"
			} else {
				out.State = "FAILED"
			}
		} else {
			out.State = "RUNNING"
		}
	}

	out.Model = normalizeAPIModel(out.Model)
	out.Name = strings.TrimSpace(out.Name)
	out.State = strings.TrimSpace(out.State)
	out.CreateTime = strings.TrimSpace(out.CreateTime)
	out.UpdateTime = strings.TrimSpace(out.UpdateTime)
	out.DisplayName = strings.TrimSpace(out.DisplayName)
	out.CompletedTime = strings.TrimSpace(out.CompletedTime)
	return out, nil
}

func (out *BatchJobStatus) absorbLight(src batchJobGetLight) {
	out.absorbLightMerge(src, true)
}

func (out *BatchJobStatus) absorbLightFillEmpty(src batchJobGetLight) {
	out.absorbLightMerge(src, false)
}

func (out *BatchJobStatus) absorbLightMerge(src batchJobGetLight, overwrite bool) {
	set := func(dst *string, v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if overwrite || strings.TrimSpace(*dst) == "" {
			*dst = v
		}
	}
	set(&out.Name, src.Name)
	set(&out.State, src.State)
	set(&out.CreateTime, src.CreateTime)
	set(&out.UpdateTime, src.UpdateTime)
	set(&out.DisplayName, src.DisplayName)
	set(&out.Model, src.Model)
	ct := firstNonEmpty(
		strings.TrimSpace(src.CompletedTime),
		strings.TrimSpace(src.EndTime),
		strings.TrimSpace(src.DoneTime),
	)
	if ct != "" {
		if overwrite || out.CompletedTime == "" {
			out.CompletedTime = ct
		}
	}
}

func normalizeAPIModel(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "models/")
	return strings.TrimSpace(s)
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// ValidateAndNormalizeBatchJobID normalizes the id and rejects clearly invalid input.
func ValidateAndNormalizeBatchJobID(jobID string) (string, error) {
	s := strings.TrimSpace(jobID)
	if s == "" {
		return "", fmt.Errorf("batch job id is empty: expected non-empty -job-id (e.g. batches/abc123 or abc123)")
	}
	s = strings.TrimPrefix(s, "/")
	if strings.Contains(s, "..") {
		return "", fmt.Errorf("invalid batch job id %q: must not contain \"..\"", jobID)
	}
	id := NormalizeBatchJobID(s)
	if !strings.HasPrefix(strings.ToLower(id), "batches/") {
		return "", fmt.Errorf("invalid batch job id %q: must start with \"batches/\" (after normalization)", id)
	}
	slash := strings.IndexByte(id, '/')
	rest := id[slash+1:]
	if rest == "" || strings.Contains(rest, "/") {
		return "", fmt.Errorf("invalid batch job id %q: expected form \"batches/<jobId>\" with a single non-empty segment", id)
	}
	for _, ch := range rest {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-' {
			continue
		}
		return "", fmt.Errorf("invalid batch job id %q: job segment contains invalid character %q (allowed: letters, digits, _, -)", id, string(ch))
	}
	return strings.ToLower(id[:slash+1]) + rest, nil
}

// NormalizeBatchJobID returns a v1beta batch resource name (adds batches/ if missing).
func NormalizeBatchJobID(jobID string) string {
	s := strings.TrimSpace(jobID)
	s = strings.TrimPrefix(s, "/")
	if s == "" {
		return s
	}
	if !strings.HasPrefix(strings.ToLower(s), "batches/") {
		return "batches/" + s
	}
	return s
}

func summarizeEmbeddedError(raw json.RawMessage) string {
	var o struct {
		Message string `json:"message"`
		Status  string `json:"status"`
		Code    int    `json:"code"`
	}
	if json.Unmarshal(raw, &o) == nil && o.Message != "" {
		if o.Status != "" {
			return fmt.Sprintf("%s (%s)", o.Message, o.Status)
		}
		return o.Message
	}
	s := strings.TrimSpace(string(raw))
	if len(s) > 500 {
		return s[:500] + "..."
	}
	return s
}
