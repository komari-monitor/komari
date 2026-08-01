package plugin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/komari-monitor/komari/database/models"
)

func TestInstallZipExtractsValidPlugin(t *testing.T) {
	withTempDataDir(t)
	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": `{"name":"Demo","short":"demo","version":"1.0.0","komari":">=0.0.1","permissions":{"node":true}}`,
		"script.js":          `function load() {}`,
	})
	info, err := InstallZip(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Short != "demo" {
		t.Fatalf("short = %q", info.Short)
	}
	if !info.Permissions.Node {
		t.Fatal("permissions.node not parsed")
	}
	if _, err := os.Stat(filepath.Join(DataDir, "demo", "script.js")); err != nil {
		t.Fatalf("entry not extracted: %v", err)
	}
}

func TestInstallZipDefaultsEntryToScriptJS(t *testing.T) {
	withTempDataDir(t)
	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": `{"name":"Demo","short":"demo","version":"1.0.0"}`,
		"script.js":          `function load() {}`,
	})
	info, err := InstallZip(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Entry != "script.js" {
		t.Fatalf("entry = %q, want script.js", info.Entry)
	}
}

func TestInstallZipRejectsMissingManifest(t *testing.T) {
	withTempDataDir(t)
	zipPath := writePluginZip(t, map[string]string{"script.js": "function load() {}"})
	if _, err := InstallZip(zipPath); err == nil {
		t.Fatal("expected missing manifest error")
	}
}

func TestInstallZipRejectsTraversal(t *testing.T) {
	withTempDataDir(t)
	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": `{"name":"Demo","short":"demo","version":"1.0.0"}`,
		"../evil.txt":        "boom",
	})
	if _, err := InstallZip(zipPath); err == nil {
		t.Fatal("expected traversal rejection")
	}
	if _, err := os.Stat(filepath.Join(DataDir, "demo")); !os.IsNotExist(err) {
		t.Fatal("plugin directory must be removed after a failed install")
	}
}

func TestInstallZipRejectsKomariVersionMismatch(t *testing.T) {
	withTempDataDir(t)
	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": `{"name":"Demo","short":"demo","version":"1.0.0","komari":">=99.0.0"}`,
		"script.js":          `function load() {}`,
	})
	if _, err := InstallZip(zipPath); err == nil {
		t.Fatal("expected komari version mismatch error")
	}
}

func TestInstallZipRejectsMissingEntry(t *testing.T) {
	withTempDataDir(t)
	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": `{"name":"Demo","short":"demo","version":"1.0.0"}`,
	})
	if _, err := InstallZip(zipPath); err == nil {
		t.Fatal("expected missing entry error")
	}
}
func TestInstallZipValidatesPageTypesAndVisibility(t *testing.T) {
	withTempDataDir(t)

	// iframe page 默认 visibility=admin、type=iframe；合法安装。
	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": `{"name":"Pages","short":"pages","version":"1.0.0","pages":[{"file":"admin.html","title":"Admin"}]}`,
		"script.js":          `function load() {}`,
		"admin.html":         `<h1>admin</h1>`,
	})
	info, err := InstallZip(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Pages) != 1 || info.Pages[0].Type != models.PageTypeIframe || info.Pages[0].Visibility != models.PageVisibilityAdmin {
		t.Fatalf("page defaults = %+v", info.Pages)
	}

	// redirect 页面需要站内路径。
	zipPath = writePluginZip(t, map[string]string{
		"komari-plugin.json": `{"name":"Pages","short":"pages2","version":"1.0.0","pages":[{"title":"Go","type":"redirect","url":"/admin/settings","visibility":"admin"}]}`,
		"script.js":          `function load() {}`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}

	// public iframe 页面合法。
	zipPath = writePluginZip(t, map[string]string{
		"komari-plugin.json": `{"name":"Pages","short":"pages3","version":"1.0.0","pages":[{"file":"pub.html","title":"Pub","visibility":"public"}]}`,
		"script.js":          `function load() {}`,
		"pub.html":           `<h1>pub</h1>`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
}

func TestInstallZipRejectsInvalidPages(t *testing.T) {
	withTempDataDir(t)
	cases := []struct {
		name     string
		manifest string
	}{
		{"bad type", `{"name":"P","short":"p1","version":"1.0.0","pages":[{"file":"a.html","title":"A","type":"popup"}]}`},
		{"redirect without url", `{"name":"P","short":"p2","version":"1.0.0","pages":[{"title":"A","type":"redirect"}]}`},
		{"redirect external url", `{"name":"P","short":"p3","version":"1.0.0","pages":[{"title":"A","type":"redirect","url":"https://evil.example/"}]}`},
		{"redirect protocol-relative", `{"name":"P","short":"p4","version":"1.0.0","pages":[{"title":"A","type":"redirect","url":"//evil.example/"}]}`},
		{"redirect traversal", `{"name":"P","short":"p5","version":"1.0.0","pages":[{"title":"A","type":"redirect","url":"/admin/../secret"}]}`},
		{"bad visibility", `{"name":"P","short":"p6","version":"1.0.0","pages":[{"file":"a.html","title":"A","visibility":"everyone"}]}`},
		{"iframe without file", `{"name":"P","short":"p7","version":"1.0.0","pages":[{"title":"A"}]}`},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			zipPath := writePluginZip(t, map[string]string{
				"komari-plugin.json": tt.manifest,
				"script.js":          `function load() {}`,
			})
			if _, err := InstallZip(zipPath); err == nil {
				t.Fatalf("expected rejection for %s", tt.name)
			}
		})
	}
}
func TestInstallZipRejectsInvalidIcon(t *testing.T) {
	withTempDataDir(t)
	cases := []struct {
		name     string
		manifest string
	}{
		{"plugin icon traversal", `{"name":"I","short":"i1","version":"1.0.0","icon":"../evil.png"}`},
		{"plugin icon absolute", `{"name":"I","short":"i2","version":"1.0.0","icon":"/etc/passwd"}`},
		{"page icon traversal", `{"name":"I","short":"i3","version":"1.0.0","pages":[{"file":"a.html","title":"A","icon":"../evil.png"}]}`},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			zipPath := writePluginZip(t, map[string]string{
				"komari-plugin.json": tt.manifest,
				"script.js":          `function load() {}`,
			})
			if _, err := InstallZip(zipPath); err == nil {
				t.Fatalf("expected rejection for %s", tt.name)
			}
		})
	}
}

func TestInstallZipAcceptsRelativeIcon(t *testing.T) {
	withTempDataDir(t)
	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": `{"name":"I","short":"ic","version":"1.0.0","icon":"icon.svg","pages":[{"file":"a.html","title":"A","icon":"page.svg"}]}`,
		"script.js":          `function load() {}`,
		"a.html":             `<h1>a</h1>`,
		"icon.svg":           `<svg/>`,
		"page.svg":           `<svg/>`,
	})
	info, err := InstallZip(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Icon != "icon.svg" || info.Pages[0].Icon != "page.svg" {
		t.Fatalf("icons = %+v", info)
	}
}
