package extractors

import (
	"fmt"
	"strings"

	"game-asset-pipeline-go/internal/model"
	"game-asset-pipeline-go/internal/util"
)

type Extractor interface {
	Extract(provider model.ProviderSource, body []byte, contentType string) ([]model.AssetCandidate, error)
}

func GetExtractor(sourceType string) (Extractor, error) {
	switch strings.ToLower(strings.TrimSpace(sourceType)) {
	case "html":
		return &HTMLExtractor{}, nil
	case "json":
		return &JSONFeedExtractor{}, nil
	case "direct":
		// direct means the SourceURL is the image itself
		return &DirectExtractor{}, nil
	case "local_dir":
		return &LocalDirExtractor{}, nil
	default:
		return nil, fmt.Errorf("unknown source_type: %s", sourceType)
	}
}

type DirectExtractor struct{}

func (e *DirectExtractor) Extract(provider model.ProviderSource, body []byte, contentType string) ([]model.AssetCandidate, error) {
	u := provider.SourceURL
	if !util.LooksLikeImageURL(u) {
		return nil, fmt.Errorf("direct source_url not an image: %s", u)
	}
	return []model.AssetCandidate{
		{
			Provider: provider.Provider,
			Source: provider.SourceURL,
			URL: u,
			FileName: util.URLFileName(u),
		},
	}, nil
}
