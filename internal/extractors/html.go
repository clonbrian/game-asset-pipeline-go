package extractors

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"

	"game-asset-pipeline-go/internal/model"
	"game-asset-pipeline-go/internal/util"
)

type HTMLExtractor struct{}

func (e *HTMLExtractor) Extract(provider model.ProviderSource, body []byte, contentType string) ([]model.AssetCandidate, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	var out []model.AssetCandidate

	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "img") {
			src := attr(n, "src")
			if src == "" {
				src = attr(n, "data-src")
			}
			if src == "" {
				// try srcset first entry
				ss := attr(n, "srcset")
				src = firstSrcsetURL(ss)
			}
			if src != "" {
				u := util.ResolveURL(provider.SourceURL, src)
				if util.LooksLikeImageURL(u) {
					title := attr(n, "title")
					alt := attr(n, "alt")
					fn := util.URLFileName(u)
					out = append(out, model.AssetCandidate{
						Provider:  provider.Provider,
						Source:    provider.SourceURL,
						URL:       u,
						Title:     title,
						Alt:       alt,
						FileName:  fn,
						Width:     atoiSafe(attr(n, "width")),
						Height:    atoiSafe(attr(n, "height")),
					})
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	// de-dupe by URL
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

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return strings.TrimSpace(a.Val)
		}
	}
	return ""
}

func firstSrcsetURL(srcset string) string {
	srcset = strings.TrimSpace(srcset)
	if srcset == "" {
		return ""
	}
	parts := strings.Split(srcset, ",")
	if len(parts) == 0 {
		return ""
	}
	// "url 1x" => take first token
	first := strings.TrimSpace(parts[0])
	toks := strings.Fields(first)
	if len(toks) == 0 {
		return ""
	}
	return toks[0]
}

func atoiSafe(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	// very small parser
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}
