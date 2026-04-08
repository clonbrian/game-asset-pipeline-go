package imagen

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

type Client struct {
	HTTP    *http.Client
	BaseURL string
	APIKey  string
	Model   string
}

type predictRequest struct {
	Instances  []predictInstance `json:"instances"`
	Parameters predictParams     `json:"parameters"`
}

type predictInstance struct {
	Prompt string `json:"prompt"`
}

type predictParams struct {
	SampleCount int    `json:"sampleCount,omitempty"`
	AspectRatio string `json:"aspectRatio,omitempty"`
}

type predictResponse struct {
	Predictions []struct {
		BytesBase64Encoded string `json:"bytesBase64Encoded"`
		Image              struct {
			BytesBase64Encoded string `json:"bytesBase64Encoded"`
		} `json:"image"`
	} `json:"predictions"`
}

type apiErrorBody struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

// GenerateWithSourceFallback generates an image using Imagen predict route.
// Source image is not supported in this route and intentionally ignored.
func (c *Client) GenerateWithSourceFallback(ctx context.Context, prompt, _sourcePath, aspectRatio string) ([]byte, string, error) {
	if c.APIKey == "" {
		return nil, "", fmt.Errorf("imagen: missing API key")
	}
	if c.Model == "" {
		return nil, "", fmt.Errorf("imagen: model is empty")
	}
	base := c.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, "", fmt.Errorf("imagen: base URL: %w", err)
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/models/" + c.Model + ":predict"
	q := u.Query()
	q.Set("key", c.APIKey)
	u.RawQuery = q.Encode()

	reqObj := predictRequest{
		Instances: []predictInstance{{Prompt: prompt}},
		Parameters: predictParams{
			SampleCount: 1,
			AspectRatio: aspectRatio,
		},
	}
	raw, err := json.Marshal(reqObj)
	if err != nil {
		return nil, "", fmt.Errorf("imagen: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(raw))
	if err != nil {
		return nil, "", fmt.Errorf("imagen: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("imagen: http: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("imagen: read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("imagen: %s", summarizeAPIError(body, resp.StatusCode))
	}
	var parsed predictResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, "", fmt.Errorf("imagen: parse response: %w", err)
	}
	if len(parsed.Predictions) == 0 {
		return nil, "", fmt.Errorf("imagen: empty predictions")
	}
	b64 := parsed.Predictions[0].BytesBase64Encoded
	if b64 == "" {
		b64 = parsed.Predictions[0].Image.BytesBase64Encoded
	}
	if b64 == "" {
		return nil, "", fmt.Errorf("imagen: missing base64 image bytes in response")
	}
	img, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, "", fmt.Errorf("imagen: decode base64: %w", err)
	}
	return img, "image/png", nil
}

func GenerateWithRetry(ctx context.Context, client *Client, prompt, sourcePath, aspectRatio string, maxAttempts int, baseBackoff time.Duration) ([]byte, string, error) {
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		out, mt, err := client.GenerateWithSourceFallback(ctx, prompt, sourcePath, aspectRatio)
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

func summarizeAPIError(body []byte, status int) string {
	var eb apiErrorBody
	if json.Unmarshal(body, &eb) == nil && eb.Error.Message != "" {
		return fmt.Sprintf("http %d: %s (%s)", status, eb.Error.Message, eb.Error.Status)
	}
	s := string(body)
	if len(s) > 400 {
		s = s[:400] + "..."
	}
	return fmt.Sprintf("http %d: %s", status, s)
}

func isRetriable(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "http 429") ||
		strings.Contains(s, "http 503") ||
		strings.Contains(s, "http 500") ||
		strings.Contains(s, "resource_exhausted") ||
		strings.Contains(s, "unavailable")
}
