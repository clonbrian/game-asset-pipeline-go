package app

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func ZipFolder(folder string, zipPath string) error {
	if err := os.MkdirAll(filepath.Dir(zipPath), 0o755); err != nil {
		return err
	}
	zf, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer zf.Close()

	zw := zip.NewWriter(zf)
	defer zw.Close()

	base := filepath.Clean(folder)

	return filepath.Walk(folder, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(base, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "../") {
			return fmt.Errorf("invalid rel path: %s", rel)
		}

		h, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		h.Name = rel
		h.Method = zip.Deflate

		w, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}

		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()

		_, err = io.Copy(w, f)
		return err
	})
}
