package downloader

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type Client struct {
	HTTP      *http.Client
	UserAgent string
}

func New(timeout time.Duration, userAgent string) *Client {
	return &Client{
		HTTP: &http.Client{Timeout: timeout},
		UserAgent: userAgent,
	}
}

func (c *Client) GetBytes(url string, headers map[string]string) ([]byte, string, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("http status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	return b, resp.Header.Get("Content-Type"), nil
}

func (c *Client) DownloadToFile(url string, headers map[string]string, outPath string) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("http status %d", resp.StatusCode)
	}

	if err := os.MkdirAll(dirOf(outPath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}

func dirOf(p string) string {
	for i := len(p)-1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[:i]
		}
	}
	return "."
}
