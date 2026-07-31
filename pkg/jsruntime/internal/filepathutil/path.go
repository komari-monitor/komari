package filepathutil

import (
	"fmt"
	"path/filepath"
	"strings"
)

func RelativeToBase(baseDir, name string) (string, error) {
	absolute, err := filepath.Abs(name)
	if err != nil {
		return "", fmt.Errorf("resolve JavaScript path: %w", err)
	}
	absolute = filepath.Clean(absolute)
	if !WithinBase(baseDir, absolute) {
		return "", fmt.Errorf("JavaScript path escapes BaseDir: %s", name)
	}
	relative, err := filepath.Rel(baseDir, absolute)
	if err != nil {
		return "", fmt.Errorf("make JavaScript path relative to BaseDir: %w", err)
	}
	if relative == "." {
		return ".", nil
	}
	return relative, nil
}

func WithinBase(baseDir, path string) bool {
	relative, err := filepath.Rel(baseDir, path)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
