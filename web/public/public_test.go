package public

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeHTMLLanguage(t *testing.T) {
	tests := map[string]struct {
		input string
		want  string
	}{
		"hyphen language": {
			input: "zh-CN",
			want:  "zh-CN",
		},
		"underscore language": {
			input: "zh_CN",
			want:  "zh-CN",
		},
		"reject script injection": {
			input: `zh-CN" autofocus`,
		},
		"reject too short": {
			input: "z",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := normalizeHTMLLanguage(tt.input); got != tt.want {
				t.Fatalf("normalizeHTMLLanguage(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestReplaceHTMLLanguage(t *testing.T) {
	tests := map[string]struct {
		html     string
		language string
		want     string
	}{
		"replace existing lang": {
			html:     `<html lang="en"><head></head></html>`,
			language: "zh-CN",
			want:     `<html lang="zh-CN"><head></head></html>`,
		},
		"insert missing lang": {
			html:     `<html><head></head></html>`,
			language: "ja_JP",
			want:     `<html lang="ja-JP"><head></head></html>`,
		},
		"ignore invalid lang": {
			html:     `<html lang="en"><head></head></html>`,
			language: `zh-CN" autofocus`,
			want:     `<html lang="en"><head></head></html>`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := replaceHTMLLanguage(tt.html, tt.language); got != tt.want {
				t.Fatalf("replaceHTMLLanguage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReadLocalThemeFileAllowsDefaultOverlay(t *testing.T) {
	root := t.TempDir()
	originalWorkingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalWorkingDirectory) })

	indexPath := filepath.Join(DataDir, ThemesDir, DefaultTheme, DistDir, IndexFile)
	if err := os.MkdirAll(filepath.Dir(indexPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, []byte("updated default"), 0644); err != nil {
		t.Fatal(err)
	}

	content, _, exists := readLocalThemeFile(DefaultTheme, filepath.Join(DistDir, IndexFile))
	if !exists || string(content) != "updated default" {
		t.Fatalf("readLocalThemeFile(default) = %q, %v", content, exists)
	}
	if _, _, exists := readLocalThemeFile(DefaultTheme, "../../outside"); exists {
		t.Fatal("readLocalThemeFile accepted a traversal path")
	}
	staleAssetPath := filepath.Join(DataDir, ThemesDir, DefaultTheme, DistDir, "assets", "stale.js")
	if err := os.MkdirAll(filepath.Dir(staleAssetPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staleAssetPath, []byte("stale"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(indexPath); err != nil {
		t.Fatal(err)
	}
	if _, _, exists := readLocalThemeFile(DefaultTheme, filepath.Join(DistDir, "assets", "stale.js")); exists {
		t.Fatal("readLocalThemeFile used an incomplete default overlay")
	}
}

func TestIsValidThemeID(t *testing.T) {
	for _, themeID := range []string{DefaultTheme, "Glassmorphism", "theme-2", "theme_3"} {
		if !isValidThemeID(themeID) {
			t.Errorf("isValidThemeID(%q) = false", themeID)
		}
	}
	for _, themeID := range []string{"", ".theme-backup", "../theme", "theme/name", "theme\\name", "theme name"} {
		if isValidThemeID(themeID) {
			t.Errorf("isValidThemeID(%q) = true", themeID)
		}
	}
}
