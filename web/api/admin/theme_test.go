package admin

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/web/public"
)

func writeThemeArchive(t *testing.T, themeShort string, includeIndex bool) string {
	t.Helper()

	archivePath := filepath.Join(t.TempDir(), "theme.zip")
	archiveFile, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(archiveFile)

	theme := models.Theme{
		Name:        "Updated Theme",
		Short:       themeShort,
		Description: "test theme",
		Version:     "2.0.0",
		Author:      "tester",
		URL:         "https://example.com/theme",
		Preview:     "preview.png",
		Configuration: models.Configuration{
			Type: "managed",
		},
	}
	manifest, err := json.Marshal(theme)
	if err != nil {
		t.Fatal(err)
	}

	files := map[string][]byte{
		"komari-theme.json": manifest,
		"preview.png":       []byte("preview"),
	}
	if includeIndex {
		files["dist/index.html"] = []byte("updated default")
	}
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archiveFile.Close(); err != nil {
		t.Fatal(err)
	}

	return archivePath
}

func withTemporaryWorkingDirectory(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	originalWorkingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalWorkingDirectory) })
	return root
}

// TestIsValidThemeShort_PathTraversal 防止 DeleteTheme/UpdateTheme/SetTheme
// 的路径穿越漏洞：req.Short 直接进入 filepath.Join("./data/theme", short)，
// 若 short 含 "../" 会规范化到 ./data/theme 之外，配合 os.RemoveAll 可删除
// 工作目录外任意目录。isValidThemeShort 必须拒绝所有此类 payload。
func TestIsValidThemeShort_PathTraversal(t *testing.T) {
	// 必须被拒绝：路径穿越 / 绝对路径 / 目录分隔符 / 空值 / default
	deny := []string{
		"",
		"default",
		"..",
		"../",
		"./",
		"../../etc",
		"../..",
		"foo/../bar",
		"/etc/passwd",
		"foo/bar",
		"foo\\bar",
		"a b",
		"a;b",
		"a$(id)",
	}
	for _, in := range deny {
		if isValidThemeShort(in) {
			t.Errorf("isValidThemeShort(%q) = true, want false (路径穿越/非法字符未被拦截)", in)
		}
	}

	// 必须被接受：仅字母数字下划线连字符
	accept := []string{
		"mytheme",
		"my-theme",
		"my_theme",
		"theme123",
		"ABC",
		"a",
	}
	for _, in := range accept {
		if !isValidThemeShort(in) {
			t.Errorf("isValidThemeShort(%q) = false, want true (合法名称被误拒)", in)
		}
	}
}

func TestInstallThemeArchiveAsDefault(t *testing.T) {
	withTemporaryWorkingDirectory(t)

	themeDir := filepath.Join("data", "theme", public.DefaultTheme)
	if err := os.MkdirAll(themeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(themeDir, "obsolete.txt"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	installed, err := installThemeArchiveAs(writeThemeArchive(t, public.DefaultTheme, true), public.DefaultTheme)
	if err != nil {
		t.Fatal(err)
	}
	if installed.Short != public.DefaultTheme {
		t.Fatalf("installed short = %q, want %q", installed.Short, public.DefaultTheme)
	}

	index, err := os.ReadFile(filepath.Join(themeDir, "dist", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if string(index) != "updated default" {
		t.Fatalf("installed index = %q", index)
	}
	if _, err := os.Stat(filepath.Join(themeDir, "obsolete.txt")); !os.IsNotExist(err) {
		t.Fatalf("obsolete file was not removed: %v", err)
	}

	stored, err := loadThemeConfig(filepath.Join(themeDir, "komari-theme.json"))
	if err != nil {
		t.Fatal(err)
	}
	if stored.Short != public.DefaultTheme {
		t.Fatalf("stored short = %q, want %q", stored.Short, public.DefaultTheme)
	}
}

func TestInstallThemeArchiveAsDefaultPreservesPreviousOnInvalidArchive(t *testing.T) {
	withTemporaryWorkingDirectory(t)

	indexPath := filepath.Join("data", "theme", public.DefaultTheme, "dist", "index.html")
	if err := os.MkdirAll(filepath.Dir(indexPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, []byte("previous default"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := installThemeArchiveAs(writeThemeArchive(t, "updated-theme", false), public.DefaultTheme); err == nil {
		t.Fatal("installThemeArchiveAs accepted an archive without dist/index.html")
	}
	content, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "previous default" {
		t.Fatalf("previous default changed to %q", content)
	}
}
