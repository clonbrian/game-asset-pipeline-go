// Package gemini calls the Google Gemini generateContent API for image-to-image adaptation.
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

const defaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// Client performs Gemini REST requests. Model is the API model id (e.g. gemini-2.5-flash-image).
type Client struct {
	HTTP    *http.Client
	BaseURL string
	APIKey  string
	Model   string
}

type genContentRequest struct {
	Contents         []contentMsg `json:"contents"`
	GenerationConfig genConfig    `json:"generationConfig"`
}

type contentMsg struct {
	Parts []partMsg `json:"parts"`
}

type partMsg struct {
	Text       string      `json:"text,omitempty"`
	InlineData *inlineData `json:"inlineData,omitempty"`
}

type inlineData struct {
	MimeType       string `json:"mimeType"`
	MimeTypeLegacy string `json:"mime_type"`
	Data           string `json:"data"`
}

func (b *inlineData) resolvedMime() string {
	if b.MimeType != "" {
		return b.MimeType
	}
	return b.MimeTypeLegacy
}

type genConfig struct {
	ResponseModalities []string     `json:"responseModalities"`
	ImageConfig        imageConfig  `json:"imageConfig"`
}

type imageConfig struct {
	AspectRatio string `json:"aspectRatio"`
	ImageSize   string `json:"imageSize,omitempty"`
}

type responsePart struct {
	Text         string      `json:"text"`
	InlineData   *inlineData `json:"inlineData"`
	InlineDataSn *inlineData `json:"inline_data"`
}

func (p responsePart) imageBlob() *inlineData {
	if p.InlineData != nil {
		return p.InlineData
	}
	return p.InlineDataSn
}

type genContentResponse struct {
	Candidates []struct {
		Content struct {
			Parts []responsePart `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
}

type apiErrorBody struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

// GenerateImageAdaptation sends the source image and prompt; returns decoded image bytes and output MIME type.
func (c *Client) GenerateImageAdaptation(ctx context.Context, prompt string, imageBytes []byte, inputMime, aspectRatio, imageSize string) ([]byte, string, error) {
	if c.APIKey == "" {
		return nil, "", fmt.Errorf("gemini: missing API key (set environment variable from config apiKeyEnv)")
	}
	if c.Model == "" {
		return nil, "", fmt.Errorf("gemini: model is empty")
	}
	base := c.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, "", fmt.Errorf("gemini: base URL: %w", err)
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/models/" + c.Model + ":generateContent"
	q := u.Query()
	q.Set("key", c.APIKey)
	u.RawQuery = q.Encode()

	b64 := base64.StdEncoding.EncodeToString(imageBytes)
	mime := inputMime
	if mime == "" {
		mime = "image/png"
	}

	reqBody := genContentRequest{
		Contents: []contentMsg{{
			Parts: []partMsg{
				{Text: prompt},
				{InlineData: &inlineData{MimeType: mime, Data: b64}},
			},
		}},
		GenerationConfig: genConfig{
			ResponseModalities: []string{"IMAGE"},
			ImageConfig: imageConfig{
				AspectRatio: aspectRatio,
				ImageSize:   imageSize,
			},
		},
	}

	raw, err := json.Marshal(reqBody)
	if err != nil {
		return nil, "", fmt.Errorf("gemini: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(raw))
	if err != nil {
		return nil, "", fmt.Errorf("gemini: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, "", fmt.Errorf("gemini: http: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("gemini: read body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := summarizeAPIError(body, resp.StatusCode)
		return nil, "", fmt.Errorf("gemini: %s", msg)
	}

	var parsed genContentResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, "", fmt.Errorf("gemini: parse response json: %w", err)
	}
	if parsed.PromptFeedback != nil && parsed.PromptFeedback.BlockReason != "" {
		return nil, "", fmt.Errorf("gemini: prompt blocked (%s)", parsed.PromptFeedback.BlockReason)
	}
	if len(parsed.Candidates) == 0 {
		return nil, "", fmt.Errorf("gemini: empty candidates (body may contain safety block); raw=%s", truncate(string(body), 500))
	}
	for _, p := range parsed.Candidates[0].Content.Parts {
		blob := p.imageBlob()
		if blob != nil && blob.Data != "" {
			out, err := base64.StdEncoding.DecodeString(blob.Data)
			if err != nil {
				return nil, "", fmt.Errorf("gemini: decode output image: %w", err)
			}
			mt := blob.resolvedMime()
			if mt == "" {
				mt = "image/png"
			}
			return out, mt, nil
		}
	}
	reason := parsed.Candidates[0].FinishReason
	return nil, "", fmt.Errorf("gemini: no image in response (finishReason=%s)", reason)
}

func summarizeAPIError(body []byte, status int) string {
	var eb apiErrorBody
	if json.Unmarshal(body, &eb) == nil && eb.Error.Message != "" {
		return fmt.Sprintf("http %d: %s (%s)", status, eb.Error.Message, eb.Error.Status)
	}
	return fmt.Sprintf("http %d: %s", status, truncate(string(body), 400))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// GenerateWithRetry wraps GenerateImageAdaptation with retries on transient errors and rate limits.
func GenerateWithRetry(ctx context.Context, client *Client, prompt string, imageBytes []byte, inputMime, aspectRatio, imageSize string, maxAttempts int, baseBackoff time.Duration) ([]byte, string, error) {
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		out, mt, err := client.GenerateImageAdaptation(ctx, prompt, imageBytes, inputMime, aspectRatio, imageSize)
		if err == nil {
			return out, mt, nil
		}
		lastErr = err
		if attempt >= maxAttempts || !isRetriable(err) {
			break
		}
		d := baseBackoff * time.Duration(1<<uint(attempt-1))
		if d > 30*time.Second {
			d = 30 * time.Second
		}
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-time.After(d):
		}
	}
	return nil, "", lastErr
}

func isRetriable(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "http 429") ||
		strings.Contains(s, "http 503") ||
		strings.Contains(s, "http 500") ||
		strings.Contains(s, "resource_exhausted") ||
		strings.Contains(s, "unavailable")
}
