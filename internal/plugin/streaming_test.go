package plugin

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func encodeTestJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestBuiltinStyleStreamingWithResponseHooks 模拟普通 Gin handler 风格的流式
// 路由（直接写 c.Writer + Flush）在存在 response hook 时仍然能把数据推给客户端。
// 回归：bufferedResponseWriter 此前会吞掉所有 Write，直到 handler 返回（流式
// handler 永不返回），客户端因此收不到任何数据（黑屏）。
func TestBuiltinStyleStreamingWithResponseHooks(t *testing.T) {
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	Init(engine)

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

	frame := encodeTestJPEG(t)
	engine.GET("/live", func(c *gin.Context) {
		c.Header("Content-Type", "multipart/x-mixed-replace; boundary=frame")
		for i := 0; i < 5; i++ {
			_, _ = c.Writer.WriteString("--frame\r\nContent-Type: image/jpeg\r\n\r\n")
			_, _ = c.Writer.Write(frame)
			_, _ = c.Writer.WriteString("\r\n")
			c.Writer.Flush()
			time.Sleep(20 * time.Millisecond)
		}
	})

	wrapped := WrapHandler(engine)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1/live", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	body := rec.Body.Bytes()

	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "multipart/x-mixed-replace") {
		t.Fatalf("content-type = %q, want multipart stream", ct)
	}
	if !bytes.Contains(body, []byte("--frame")) || !bytes.Contains(body, []byte{0xFF, 0xD8}) {
		t.Fatalf("streamed body missing frames: len=%d", len(body))
	}
	// 流式响应不能被 response hook 改写，也不应带上 hook 头
	if got := rec.Header().Get("X-Hooked"); got != "" {
		t.Fatalf("streaming response unexpectedly rewritten by response hook: x-hooked=%q", got)
	}
}

// TestRouteStreamingMJPEG 用本地“摄像头”（单张 JPEG）验证流式路由：
// multipart 帧即时写出、客户端断开后脚本通过 isAborted 退出循环。
func TestRouteStreamingMJPEG(t *testing.T) {
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	Init(engine)

	frame := encodeTestJPEG(t)
	camera := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(frame)
	}))
	defer camera.Close()

	script := fmt.Sprintf(`
		const server = require("server");
		function load() {
			server.route("GET", "/stream", async (req, res) => {
				res.streaming = true;
				res.setHeader("Content-Type", "multipart/x-mixed-replace; boundary=frame");
				res.setHeader("Cache-Control", "no-cache");
				while (!res.isAborted()) {
					try {
						const resp = await fetch("%s");
						const data = await resp.arrayBuffer();
						res.write("--frame\r\nContent-Type: image/jpeg\r\nContent-Length: " + data.byteLength + "\r\n\r\n");
						res.write(data);
						res.write("\r\n");
					} catch (e) {
						console.log("frame error: " + e.message);
					}
					await new Promise((resolve) => setTimeout(resolve, 50));
				}
				console.log("stream ended");
			});
		}
	`, camera.URL)
	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": `{"name":"Mjpeg","short":"mjpeg","version":"1.0.0","permissions":{"node":true,"timeout":5,"allowRoutes":true}}`,
		"script.js":          script,
	})
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
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "multipart/x-mixed-replace") {
		t.Fatalf("content-type = %q", ct)
	}

	// 读取第一个完整帧并校验 JPEG magic 与长度
	reader := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(5 * time.Second)
	found := false
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
		rest := make([]byte, length-2)
		if _, err := io.ReadFull(reader, rest); err != nil {
			t.Fatalf("read jpeg body: %v", err)
		}
		found = true
		break
	}
	if !found {
		t.Fatal("no frame received within 5s")
	}

	// 客户端断开 → 脚本 isAborted 退出循环
	cancel()
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if logs := GetLogs("mjpeg"); strings.Contains(logs, "stream ended") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("stream did not stop after disconnect; logs = %q", GetLogs("mjpeg"))
}
