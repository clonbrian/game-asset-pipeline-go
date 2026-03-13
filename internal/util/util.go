package util

import (
	"crypto/sha1"
	"encoding/hex"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode"
)

func Normalize(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)

	// replace separators with space
	reSep := regexp.MustCompile(`[_\-\.\+]+`)
	s = reSep.ReplaceAllString(s, " ")

	// remove non-alnum except space
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func Tokenize(s string) []string {
	s = Normalize(s)
	if s == "" {
		return nil
	}
	return strings.Split(s, " ")
}

func ContainsNonBasicLatin(s string) bool {
	for _, r := range s {
		// Basic Latin + Latin-1 Supplement
		if r > 0x00FF {
			// ignore common punctuation beyond 0xFF? keep it simple
			return true
		}
	}
	return false
}

func SafeSlug(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.ReplaceAll(s, " ", "_")
	re := regexp.MustCompile(`[^a-z0-9_]+`)
	s = re.ReplaceAllString(s, "")
	if s == "" {
		s = "unknown"
	}
	return s
}

func URLFileName(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	base := path.Base(u.Path)
	if base == "." || base == "/" {
		return ""
	}
	return base
}

func HashShort(s string) string {
	h := sha1.Sum([]byte(s))
	return hex.EncodeToString(h[:])[:10]
}

func ResolveURL(baseStr, refStr string) string {
	refStr = strings.TrimSpace(refStr)
	if refStr == "" {
		return ""
	}
	ref, err := url.Parse(refStr)
	if err == nil && ref.IsAbs() {
		return ref.String()
	}

	base, err := url.Parse(baseStr)
	if err != nil {
		return refStr
	}
	u := base.ResolveReference(ref)
	return u.String()
}

func LooksLikeImageURL(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.Contains(s, ".png") ||
		strings.Contains(s, ".jpg") ||
		strings.Contains(s, ".jpeg") ||
		strings.Contains(s, ".webp") ||
		strings.Contains(s, ".gif")
}
