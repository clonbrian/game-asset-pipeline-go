package app

import (
	"path"
	"path/filepath"
	"strings"
)

// inputFileRelPath returns filepath.Rel(inputRootAbs, fileAbs) cleaned.
func inputFileRelPath(inputRootAbs, fileAbs string) (string, error) {
	rel, err := filepath.Rel(inputRootAbs, fileAbs)
	if err != nil {
		return "", err
	}
	return filepath.Clean(rel), nil
}

// joinOutputPreservingInputSubdirs places fileName under root, preserving the directory part of relFromInput
// (path relative to input root). Example: rel JILI/a.png → root/JILI/fileName.
func joinOutputPreservingInputSubdirs(root, relFromInput, fileName string) string {
	relFromInput = filepath.Clean(relFromInput)
	sub := filepath.Dir(relFromInput)
	if sub == "." {
		return filepath.Join(root, fileName)
	}
	return filepath.Join(root, sub, fileName)
}

// sourceRelPathForMetadata stores POSIX-style relative paths in batch metadata for recover.
func sourceRelPathForMetadata(relFromInput string) string {
	return filepath.ToSlash(filepath.Clean(relFromInput))
}

// recoverySubdirFromSourceRelPath returns the parent directory of source_rel_path metadata (e.g. JILI for JILI/a.png).
func recoverySubdirFromSourceRelPath(meta map[string]any) string {
	if meta == nil {
		return ""
	}
	v, ok := meta["source_rel_path"].(string)
	if !ok {
		return ""
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	v = path.Clean(filepath.ToSlash(v))
	d := path.Dir(v)
	if d == "." || d == "/" {
		return ""
	}
	return filepath.FromSlash(d)
}

// stripFirstPathSegment removes the first path segment of rel (e.g. batches_xxx/JILI/a.png → JILI/a.png).
func stripFirstPathSegment(rel string) string {
	rel = filepath.Clean(rel)
	if rel == "." || rel == "" {
		return rel
	}
	parts := strings.FieldsFunc(rel, func(r rune) bool {
		return r == '/' || r == filepath.Separator
	})
	var nonEmpty []string
	for _, p := range parts {
		if p != "" && p != "." {
			nonEmpty = append(nonEmpty, p)
		}
	}
	if len(nonEmpty) <= 1 {
		return rel
	}
	return filepath.Join(nonEmpty[1:]...)
}
