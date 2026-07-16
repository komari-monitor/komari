package admin

import (
	"testing"

	"github.com/komari-monitor/komari/database/models"
)

func TestNormalizeEmbeddedDefaultTheme(t *testing.T) {
	theme := normalizeEmbeddedDefaultTheme(models.Theme{
		Name:    "Komari Glassmorphism",
		Short:   "Glassmorphism",
		Version: "3.1.9",
	})

	if theme.Short != "default" {
		t.Fatalf("normalizeEmbeddedDefaultTheme().Short = %q, want default", theme.Short)
	}
	if theme.Name != "Komari Glassmorphism" || theme.Version != "3.1.9" {
		t.Fatalf("normalizeEmbeddedDefaultTheme() changed theme metadata: %#v", theme)
	}
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
