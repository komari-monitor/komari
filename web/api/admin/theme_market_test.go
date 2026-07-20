package admin

import (
	"archive/zip"
	"testing"
)

func TestParseThemeMarketCatalogShapes(t *testing.T) {
	theme := `{"name":"Test","short":"Test","version":"1.0.0","author":"Author","download":"https://example.com/theme.zip","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`
	tests := []string{
		theme,
		`[` + theme + `]`,
		`{"schema":1,"themes":[` + theme + `]}`,
	}
	for _, input := range tests {
		themes, err := parseThemeMarketCatalog([]byte(input))
		if err != nil {
			t.Fatalf("parseThemeMarketCatalog() error = %v", err)
		}
		if len(themes) != 1 || themes[0].Short != "Test" {
			t.Fatalf("parseThemeMarketCatalog() = %#v", themes)
		}
	}
}

func TestValidateThemeArchiveLimits(t *testing.T) {
	files := make([]*zip.File, maxThemeArchiveFiles+1)
	for i := range files {
		files[i] = &zip.File{}
	}
	if err := validateThemeArchive(files); err == nil {
		t.Fatal("validateThemeArchive() accepted too many files")
	}

	large := &zip.File{FileHeader: zip.FileHeader{UncompressedSize64: maxThemeFileSize + 1}}
	if err := validateThemeArchive([]*zip.File{large}); err == nil {
		t.Fatal("validateThemeArchive() accepted an oversized file")
	}
}

func TestValidateThemeMarketThemeChecksum(t *testing.T) {
	valid := ThemeMarketTheme{
		Name: "Test", Short: "Test", Version: "1.0.0", Author: "Author",
		URL:      "https://example.com/theme",
		Download: "https://example.com/theme.zip",
		SHA256:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	if err := validateThemeMarketTheme(valid); err != nil {
		t.Fatalf("validateThemeMarketTheme() error = %v", err)
	}
	valid.SHA256 = "xxxxxx"
	if err := validateThemeMarketTheme(valid); err == nil {
		t.Fatal("validateThemeMarketTheme() accepted an invalid checksum")
	}
}

func TestThemeMarketI18nTextAndSourceOnlyEntry(t *testing.T) {
	theme := ThemeMarketTheme{
		Name:    map[string]any{"zh-CN": "测试", "en": "Test"},
		Short:   "source-only",
		Version: "source",
		Author:  map[string]any{"en": "Author"},
		URL:     "https://example.com/theme",
	}
	if err := validateThemeMarketTheme(theme); err != nil {
		t.Fatalf("validateThemeMarketTheme() error = %v", err)
	}
}
