package app

import "strings"

// normalizeImageFileBase maps '_' → '-' in the input filename stem (no extension).
func normalizeImageFileBase(nameNoExt string) string {
	return strings.ReplaceAll(strings.TrimSpace(nameNoExt), "_", "-")
}

// imageGenOutputStem returns the file stem (no extension) for raw/final/recover outputs:
// square → base only; tall → base + "-t"; wide → base + "-w"; other size names → base + "-" + hyphenated name.
// base must already be normalized (e.g. via normalizeImageFileBase).
func imageGenOutputStem(normalizedBase string, sizeName string) string {
	base := strings.TrimSpace(normalizedBase)
	sz := strings.ToLower(strings.TrimSpace(sizeName))
	switch sz {
	case "square":
		return base
	case "tall":
		return base + "-t"
	case "wide":
		return base + "-w"
	default:
		suffix := strings.ReplaceAll(strings.TrimSpace(sizeName), "_", "-")
		if suffix == "" {
			return base
		}
		return base + "-" + suffix
	}
}

// geminiJobOutputStem combines normalizeImageFileBase + imageGenOutputStem for one input stem + size.
func geminiJobOutputStem(nameNoExt string, sizeName string) string {
	return imageGenOutputStem(normalizeImageFileBase(nameNoExt), sizeName)
}

// stemFromBatchItemID parses batch item ids from runGeminiBatchMode:
//
//	item_<index>_<originalBaseNoExt>_<sizeName>
//
// originalBaseNoExt may contain underscores; sizeName is the suffix after the last underscore.
// The returned stem matches geminiJobOutputStem(originalBaseNoExt, sizeName) and raw/final outputStem.
func stemFromBatchItemID(itemID string) (string, bool) {
	const prefix = "item_"
	itemID = strings.TrimSpace(itemID)
	if !strings.HasPrefix(itemID, prefix) {
		return "", false
	}
	rest := itemID[len(prefix):]
	digits := 0
	for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
		digits++
	}
	if digits == 0 || digits >= len(rest) || rest[digits] != '_' {
		return "", false
	}
	tail := rest[digits+1:]
	last := strings.LastIndex(tail, "_")
	if last <= 0 || last >= len(tail)-1 {
		return "", false
	}
	baseName := tail[:last]
	sizeName := tail[last+1:]
	if strings.TrimSpace(baseName) == "" || strings.TrimSpace(sizeName) == "" {
		return "", false
	}
	return geminiJobOutputStem(baseName, sizeName), true
}
