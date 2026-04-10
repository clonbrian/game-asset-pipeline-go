package gemini

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type BatchRequestItem struct {
	Prompt      string
	SourceBytes []byte
	SourcePath  string
	AspectRatio string
	ImageSize   string
	Metadata    map[string]any
}

type BatchOutputItem struct {
	Metadata map[string]any
	Image    []byte
	MimeType string
	Error    string
}

type BatchJob struct {
	Name  string
	State string
}

type batchCreateReq struct {
	Batch batchBody `json:"batch"`
}
type batchBody struct {
	DisplayName string     `json:"displayName,omitempty"`
	Model       string     `json:"model"`
	InputConfig batchInput `json:"inputConfig"`
}
type batchInput struct {
	Requests inlinedRequests `json:"requests"`
}
type inlinedRequests struct {
	Requests []inlinedRequest `json:"requests"`
}
type inlinedRequest struct {
	Request  genContentRequest `json:"request"`
	Metadata map[string]any    `json:"metadata,omitempty"`
}

// batchInlineRow is one element in output.inlinedResponses.{responses|inlinedResponses}.
type batchInlineRow struct {
	Response struct {
		Candidates []struct {
			Content struct {
				Parts []responsePart `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	} `json:"response"`
	Metadata map[string]any `json:"metadata"`
	Error    struct {
		Message string `json:"message"`
	} `json:"error"`
}

// API may use output.inlinedResponses.responses OR nested output.inlinedResponses.inlinedResponses.
type inlinedResponsesBlock struct {
	Responses           []batchInlineRow `json:"responses"`
	InlinedResponsesDup []batchInlineRow `json:"inlinedResponses"`
}

type batchGetResp struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Output struct {
		InlinedResponses inlinedResponsesBlock `json:"inlinedResponses"`
	} `json:"output"`
}

func inlineResponseRows(b inlinedResponsesBlock) []batchInlineRow {
	if len(b.InlinedResponsesDup) > 0 {
		return b.InlinedResponsesDup
	}
	return b.Responses
}

func countInlineRows(parsed *batchGetResp) int {
	return len(inlineResponseRows(parsed.Output.InlinedResponses))
}

func (c *Client) CreateBatch(ctx context.Context, displayName string, items []BatchRequestItem) (string, error) {
	reqItems := make([]inlinedRequest, 0, len(items))
	for _, it := range items {
		mime, err := DetectImageMime(it.SourcePath)
		if err != nil {
			return "", err
		}
		reqItems = append(reqItems, inlinedRequest{
			Request: genContentRequest{
				Contents: []contentMsg{{
					Parts: []partMsg{
						{Text: it.Prompt},
						{InlineData: &inlineData{
							MimeType:       mime,
							MimeTypeLegacy: mime,
							Data:           base64.StdEncoding.EncodeToString(it.SourceBytes),
						}},
					},
				}},
				GenerationConfig: genConfig{
					ResponseModalities: []string{"IMAGE"},
					ImageConfig: imageConfig{
						AspectRatio: it.AspectRatio,
						ImageSize:   it.ImageSize,
					},
				},
			},
			Metadata: it.Metadata,
		})
	}
	createReq := batchCreateReq{
		Batch: batchBody{
			DisplayName: displayName,
			Model:       "models/" + c.Model,
			InputConfig: batchInput{
				Requests: inlinedRequests{Requests: reqItems},
			},
		},
	}
	raw, _ := json.Marshal(createReq)
	u := c.batchURL("models/" + c.Model + ":batchGenerateContent")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", c.APIKey)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("gemini batch create: %s", summarizeAPIError(body, resp.StatusCode))
	}
	var parsed struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	if parsed.Name == "" {
		return "", fmt.Errorf("gemini batch create: missing batch name")
	}
	return parsed.Name, nil
}

func (c *Client) GetBatch(ctx context.Context, name string) (*BatchJob, []BatchOutputItem, error) {
	u := c.batchURL(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("x-goog-api-key", c.APIKey)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("gemini batch get: %s", summarizeAPIError(body, resp.StatusCode))
	}
	payload := pickBatchPayloadForParse(body)
	var parsed batchGetResp
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return nil, nil, err
	}
	var metaParsed batchGetResp
	_ = json.Unmarshal(extractBatchResourceJSON(body), &metaParsed)
	job := &BatchJob{
		Name:  strings.TrimSpace(coalesceNonEmpty(parsed.Name, metaParsed.Name)),
		State: strings.TrimSpace(coalesceNonEmpty(parsed.State, metaParsed.State)),
	}
	var outs []BatchOutputItem
	for _, r := range inlineResponseRows(parsed.Output.InlinedResponses) {
		out := BatchOutputItem{
			Metadata: r.Metadata,
			Error:    r.Error.Message,
			MimeType: "image/png",
		}
		if out.Error == "" && len(r.Response.Candidates) > 0 {
			for _, p := range r.Response.Candidates[0].Content.Parts {
				blob := p.imageBlob()
				if blob != nil && blob.Data != "" {
					img, err := base64.StdEncoding.DecodeString(blob.Data)
					if err == nil {
						out.Image = img
						if m := blob.resolvedMime(); m != "" {
							out.MimeType = m
						}
						break
					}
				}
			}
		}
		outs = append(outs, out)
	}
	return job, outs, nil
}

func coalesceNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// pickBatchPayloadForParse chooses JSON to unmarshal as batchGetResp.
// For completed LRO jobs, the final GenerateContentBatch-shaped payload is often in top-level "response"
// while "metadata" may lag or differ; prefer "response" when it contains inlined output rows.
func pickBatchPayloadForParse(body []byte) []byte {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return extractBatchResourceJSON(body)
	}
	if resp, ok := root["response"]; ok && len(resp) > 0 && string(resp) != "null" {
		var probe batchGetResp
		if json.Unmarshal(resp, &probe) == nil && countInlineRows(&probe) > 0 {
			return resp
		}
	}
	return extractBatchResourceJSON(body)
}

// extractBatchResourceJSON returns the JSON object that carries GenerateContentBatch fields.
// batches.get returns a long-running Operation: batch state/output usually live under "metadata",
// not at the top level — unmarshaling the raw body would leave "state" empty.
func extractBatchResourceJSON(body []byte) []byte {
	var root map[string]interface{}
	if err := json.Unmarshal(body, &root); err != nil {
		return body
	}
	if _, isLRO := root["done"]; !isLRO {
		return body
	}
	md, ok := root["metadata"].(map[string]interface{})
	if !ok || len(md) == 0 {
		return body
	}
	// Batch resource fields are embedded in metadata (with optional @type).
	hasBatchish := false
	for k := range md {
		if k == "@type" {
			continue
		}
		hasBatchish = true
		break
	}
	if !hasBatchish {
		return body
	}
	if _, ok := md["state"]; ok {
		out, err := json.Marshal(md)
		if err == nil {
			return out
		}
	}
	if _, ok := md["output"]; ok {
		out, err := json.Marshal(md)
		if err == nil {
			return out
		}
	}
	// Metadata is the LRO payload for this batch even if state is under another key later.
	out, err := json.Marshal(md)
	if err == nil {
		return out
	}
	return body
}

func (c *Client) WaitBatch(ctx context.Context, name string, interval time.Duration) (*BatchJob, []BatchOutputItem, error) {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	for {
		job, outs, err := c.GetBatch(ctx, name)
		if err != nil {
			return nil, nil, err
		}
		state := strings.ToUpper(job.State)
		if state == "BATCH_STATE_SUCCEEDED" || state == "SUCCEEDED" {
			return job, outs, nil
		}
		if strings.Contains(state, "FAILED") || strings.Contains(state, "CANCELLED") || strings.Contains(state, "EXPIRED") {
			return job, outs, fmt.Errorf("gemini batch ended with state=%s", job.State)
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(interval):
		}
	}
}

func (c *Client) batchURL(path string) string {
	base := c.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	u, _ := url.Parse(base)
	u.Path = strings.TrimSuffix(u.Path, "/") + "/" + strings.TrimPrefix(path, "/")
	return u.String()
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}
