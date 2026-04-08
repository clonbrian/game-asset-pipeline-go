package app

import (
	"fmt"
	"strings"

	"game-asset-pipeline-go/internal/model"
)

func geminiTitlePreservationBlock() string {
	return strings.TrimSpace(`Title and on-image text (critical):
- Preserve the original title text exactly when it appears in the source image.
- Keep the title readable and visually prominent.
- Enlarge the title area moderately so it remains a strong focal point; do not shrink the title relative to the source image.
- Do not replace, rewrite, misspell, or stylize the title into different wording.
- Maintain logo/title hierarchy; keep the composition suitable for polished game marketing assets.`)
}

// composeGeminiUserPrompt builds: promptTemplate + title-preservation block + size-specific instructions.
func composeGeminiUserPrompt(ig *model.ImageGenerationSpec, sz model.ImageGenSizeSpec) string {
	parts := []string{
		strings.TrimSpace(ig.PromptTemplate),
		geminiTitlePreservationBlock(),
		sizeSpecificPrompt(sz),
	}
	var b strings.Builder
	first := true
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !first {
			b.WriteString("\n\n")
		}
		b.WriteString(p)
		first = false
	}
	return b.String()
}

func sizeSpecificPrompt(sz model.ImageGenSizeSpec) string {
	if s := strings.TrimSpace(sz.SizePrompt); s != "" {
		return s + "\n\nKeep the on-image title large, exact, and prominent per the title-preservation rules above."
	}
	switch sz.Name {
	case "square":
		return strings.TrimSpace(`Adapt the image to a balanced 1:1 composition suitable for icon-like or lobby usage.
Keep the main title large, centered, and visually balanced; it must remain a clear focal point.`)
	case "wide":
		return strings.TrimSpace(`Adapt the image to a cinematic 16:9 composition suitable for banner or desktop promotional usage.
Keep the title visibly prominent in the wide layout; do not shrink it when extending or recomposing the background.`)
	case "tall":
		return strings.TrimSpace(`Adapt the image to a mobile-first 9:16 composition suitable for portrait promotional usage.
Keep the title clearly readable and visually dominant in the vertical frame.`)
	default:
		return strings.TrimSpace(fmt.Sprintf(
			"Adapt the composition for aspect ratio %s for polished game marketing use.\n"+
				"Keep any on-image title large, exact, and prominent per the title-preservation rules above.",
			sz.AspectRatio))
	}
}
