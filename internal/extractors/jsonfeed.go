package extractors

import (
	"encoding/json"
	"fmt"
	"strings"

	"game-asset-pipeline-go/internal/model"
	"game-asset-pipeline-go/internal/util"
)

type JSONFeedExtractor struct{}

func (e *JSONFeedExtractor) Extract(provider model.ProviderSource, body []byte, contentType string) ([]model.AssetCandidate, error) {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}

	var out []model.AssetCandidate
	walkJSON(v, provider, &out)

	seen := map[string]bool{}
	var uniq []model.AssetCandidate
	for _, c := range out {
		if c.URL == "" || seen[c.URL] {
			continue
		}
		seen[c.URL] = true
		uniq = append(uniq, c)
	}
	return uniq, nil
}

func walkJSON(v any, provider model.ProviderSource, out *[]model.AssetCandidate) {
	switch t := v.(type) {
	case []any:
		for _, it := range t {
			walkJSON(it, provider, out)
		}
	case map[string]any:
		// try to form a candidate if map has url/src/image-like keys
		urlStr := pickStringKey(t, "url", "src", "image", "img", "image_url", "imageUrl")
		if urlStr == "" {
			// sometimes nested: {"image":{"url":"..."}} -> handle recursion below
		} else {
			u := util.ResolveURL(provider.SourceURL, urlStr)
			if util.LooksLikeImageURL(u) {
				title := pickStringKey(t, "title", "name", "game", "game_name")
				alt := pickStringKey(t, "alt", "caption", "label")
				fn := util.URLFileName(u)

				c := model.AssetCandidate{
					Provider: provider.Provider,
					Source: provider.SourceURL,
					URL: u,
					Title: title,
					Alt: alt,
					FileName: fn,
					Width: pickIntKey(t, "width", "w"),
					Height: pickIntKey(t, "height", "h"),
				}
				*out = append(*out, c)
			}
		}

		// Also scan all string fields that look like image URLs (fallback)
		for k, vv := range t {
			_ = k
			switch s := vv.(type) {
			case string:
				ss := strings.TrimSpace(s)
				if ss != "" && util.LooksLikeImageURL(ss) {
					u := util.ResolveURL(provider.SourceURL, ss)
					fn := util.URLFileName(u)
					*out = append(*out, model.AssetCandidate{
						Provider: provider.Provider,
						Source: provider.SourceURL,
						URL: u,
						FileName: fn,
					})
				}
			default:
				walkJSON(vv, provider, out)
			}
		}
	}
}

func pickStringKey(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok2 := v.(string); ok2 {
				return strings.TrimSpace(s)
			}
		}
		// case-insensitive
		for kk, vv := range m {
			if strings.EqualFold(kk, k) {
				if s, ok2 := vv.(string); ok2 {
					return strings.TrimSpace(s)
				}
			}
		}
	}
	return ""
}

func pickIntKey(m map[string]any, keys ...string) int {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch n := v.(type) {
			case float64:
				return int(n)
			case int:
				return n
			}
		}
		for kk, vv := range m {
			if strings.EqualFold(kk, k) {
				switch n := vv.(type) {
				case float64:
					return int(n)
				case int:
					return n
				}
			}
		}
	}
	return 0
}
