package plugin

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/rpc"
)

// zipDir 把目录打包成 zip，用于安装工作区根目录下的示例插件。
func zipDir(t *testing.T, dir, zipPath string) {
	t.Helper()
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	base := filepath.Clean(dir)
	err = filepath.WalkDir(base, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		entry, err := w.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, err = entry.Write(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

// fakeStatus 模拟 common:getNodesLatestStatus 的 recordLike 结构（Go 导出形态）。
type fakeStatus struct {
	Client       string
	Time         time.Time
	Cpu          float32
	Ram          int64
	RamTotal     int64
	Load         float32
	Disk         int64
	DiskTotal    int64
	NetIn        int64
	NetOut       int64
	NetTotalUp   int64
	NetTotalDown int64
	Online       bool
	Uptime       int64
}

// registerFakeBoardRPC 注册两个 fake 的节点数据 RPC，让插件走真实
// server.call -> rpc 注册表 -> goja 导出路径。
func registerFakeBoardRPC(t *testing.T) {
	t.Helper()
	if err := rpc.Register("common:getNodes", func(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
		expired := time.Now().Add(30 * 24 * time.Hour)
		// 与真实实现一致：getNodes 返回以 uuid 为键的字典（不是数组）
		return map[string]models.Client{
			"a1": {UUID: "a1", Name: "HK-1", IPv4: "1.2.3.4", Region: "HK", Virtualization: "KVM",
				Price: 5.99, BillingCycle: 30, Currency: "$", ExpiredAt: &expired,
				Weight: 2, Hidden: false, MemTotal: 8 << 30, DiskTotal: 100 << 30},
			"a2": {UUID: "a2", Name: "Tokyo-2", IPv4: "5.6.7.8", Region: "JP", Virtualization: "KVM",
				Price: 0, BillingCycle: -1, Currency: "$",
				Weight: 1, Hidden: false, MemTotal: 4 << 30, DiskTotal: 50 << 30},
			"a3": {UUID: "a3", Name: "hidden-node", IPv4: "9.9.9.9", Region: "XX",
				Weight: 0, Hidden: true}, // 插件应自行过滤 hidden
		}, nil
	}); err != nil {
		t.Fatalf("register getNodes: %v", err)
	}
	t.Cleanup(func() { _ = rpc.Unregister("common:getNodes") })

	if err := rpc.Register("common:getNodesLatestStatus", func(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
		return map[string]fakeStatus{
			"a1": {Client: "a1", Online: true, Uptime: 3 * 86400, Load: 0.42, Cpu: 12.3,
				Ram: 2 << 30, RamTotal: 8 << 30, Disk: 20 << 30, DiskTotal: 100 << 30,
				NetIn: 512 * 1024, NetOut: 128 * 1024},
			"a2": {Client: "a2", Online: false, RamTotal: 4 << 30, DiskTotal: 50 << 30},
		}, nil
	}); err != nil {
		t.Fatalf("register getNodesLatestStatus: %v", err)
	}
	t.Cleanup(func() { _ = rpc.Unregister("common:getNodesLatestStatus") })
}

// TestMjpegPluginNodeBoard 端到端验证示例 mjpeg 插件：
// server.call 取数 -> 纯 JS 渲染 JPEG -> 流式路由输出 MJPEG -> 客户端断开停止。
// 插件目录位于工作区根（mjpeg-plugin），缺失时跳过。
func TestMjpegPluginNodeBoard(t *testing.T) {
	pluginDir := filepath.Join("..", "..", "..", "mjpeg-plugin")
	if _, err := os.Stat(pluginDir); err != nil {
		t.Skipf("example plugin dir %s not found: %v", pluginDir, err)
	}

	withTempDataDir(t)
	registerFakeBoardRPC(t)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	Init(engine)

	// 预装一个带 response hook 的插件，复现真实场景：WrapHandler 会把响应包进
	// bufferedResponseWriter，流式路由必须能穿过它（回归：黑屏问题）。
	hookZip := writePluginZip(t, map[string]string{
		"komari-plugin.json": `{"name":"Hook","short":"hook","version":"1.0.0","permissions":{"timeout":5,"allowHooks":true}}`,
		"script.js": `const server = require("server");
function load() {
  server.hook("response", (req, res) => { res.headers["x-hooked"] = "1"; });
}
`,
	})
	if _, err := InstallZip(hookZip); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("hook", true, true); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = SetEnabled("hook", false, false) }()

	zipPath := filepath.Join(t.TempDir(), "mjpeg-plugin.zip")
	zipDir(t, pluginDir, zipPath)
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("mjpeg", true, true); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = SetEnabled("mjpeg", false, false) }()

	srv := httptest.NewServer(engine)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/plugin/mjpeg/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "multipart/x-mixed-replace") {
		t.Fatalf("content-type = %q", ct)
	}

	// 读取第一帧：JPEG magic + 长度 + 可解码 + 尺寸正确（2 个可见节点）
	reader := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(20 * time.Second)
	var frame []byte
	for time.Now().Before(deadline) {
		line, err := reader.ReadBytes(10)
		if err != nil {
			t.Fatalf("read stream: %v", err)
		}
		if !bytes.Contains(line, []byte("--frame")) {
			continue
		}
		_, _ = reader.ReadBytes(10) // Content-Type
		lengthLine, err := reader.ReadBytes(10)
		if err != nil {
			t.Fatalf("read length line: %v", err)
		}
		length, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(string(lengthLine), "Content-Length:")))
		_, _ = reader.ReadBytes(10) // 空行
		head := make([]byte, 2)
		if _, err := io.ReadFull(reader, head); err != nil {
			t.Fatalf("read jpeg head: %v", err)
		}
		if head[0] != 0xFF || head[1] != 0xD8 {
			t.Fatalf("jpeg magic = % X", head)
		}
		frame = make([]byte, length)
		copy(frame, head)
		if _, err := io.ReadFull(reader, frame[2:]); err != nil {
			t.Fatalf("read jpeg body: %v", err)
		}
		break
	}
	if frame == nil {
		t.Fatal("no frame received within 20s")
	}

	img, err := jpeg.Decode(bytes.NewReader(frame))
	if err != nil {
		t.Fatalf("decode plugin jpeg frame: %v", err)
	}
	// 60 表头 + 36 列头 + 2*36 行 + 50 页脚 = 218
	want := image.Rect(0, 0, 1280, 60+36+36*2+50)
	if img.Bounds() != want {
		t.Fatalf("frame bounds = %v, want %v", img.Bounds(), want)
	}
	if !frameHasColor(t, img, 0, 128, 0, 200) {
		t.Fatal("online status color missing in frame")
	}
	if !frameHasColor(t, img, 198, 40, 40, 120) {
		t.Fatal("offline status color missing in frame")
	}

	// 客户端断开 -> 脚本 isAborted 退出
	cancel()
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if logs := GetLogs("mjpeg"); strings.Contains(logs, "stream closed") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("stream did not stop after disconnect; logs = %q", GetLogs("mjpeg"))
}

// frameHasColor 检查帧内是否存在接近目标颜色的像素（JPEG 有损，容差 24）。
func frameHasColor(t *testing.T, img image.Image, r, g, b, min int) bool {
	t.Helper()
	bounds := img.Bounds()
	count := 0
	for y := bounds.Min.Y; y < bounds.Max.Y && count < min*2; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pr, pg, pb, _ := img.At(x, y).RGBA()
			pr8, pg8, pb8 := int(pr>>8), int(pg>>8), int(pb>>8)
			if abs(pr8-r) <= 24 && abs(pg8-g) <= 24 && abs(pb8-b) <= 24 {
				count++
				if count >= min {
					return true
				}
			}
		}
	}
	return false
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
