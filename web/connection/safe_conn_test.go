package connection

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// recordingInterceptor captures calls and applies simple rewrites.
type recordingInterceptor struct {
	mu             sync.Mutex
	connects       []*ConnInfo
	lastInfo       *ConnInfo
	messages       []string
	sends          []string
	closes         int
	dropMessage    bool
	dropSend       bool
	rewriteMessage bool
}

func (r *recordingInterceptor) OnConnect(info *ConnInfo) (bool, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.connects = append(r.connects, info)
	return false, ""
}

func (r *recordingInterceptor) OnMessage(info *ConnInfo, frameType int, data []byte) (int, []byte, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastInfo = info
	r.messages = append(r.messages, string(data))
	if r.dropMessage {
		return frameType, nil, true
	}
	if r.rewriteMessage {
		return frameType, []byte("rewritten:" + string(data)), false
	}
	return frameType, data, false
}

func (r *recordingInterceptor) OnSend(info *ConnInfo, frameType int, data []byte) (int, []byte, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastInfo = info
	r.sends = append(r.sends, string(data))
	if r.dropSend {
		return frameType, nil, true
	}
	return frameType, data, false
}

func (r *recordingInterceptor) OnClose(info *ConnInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closes++
}

func (r *recordingInterceptor) calls() (info *ConnInfo, messages []string, sends []string, closes int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastInfo, append([]string(nil), r.messages...), append([]string(nil), r.sends...), r.closes
}

// wsEchoServer upgrades every request and echoes each frame back.
func wsEchoServer(t *testing.T) *httptest.Server {
	t.Helper()
	var upgrader websocket.Upgrader
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			mt, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(mt, data); err != nil {
				return
			}
		}
	}))
}

func dialEcho(t *testing.T, server *httptest.Server) *SafeConn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return NewSafeConn(conn)
}

// TestSafeConnInterceptorWired 验证读写帧都经过拦截器，且连接信息与 ID 一致。
func TestSafeConnInterceptorWired(t *testing.T) {
	server := wsEchoServer(t)
	defer server.Close()
	sc := dialEcho(t, server)
	defer sc.Close()

	rec := &recordingInterceptor{}
	info := &ConnInfo{ID: sc.ID, Path: "/api/clients/v2/rpc", RemoteIP: "203.0.113.9"}
	sc.SetInterceptor(info, rec)

	if err := sc.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	mt, data, err := sc.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if mt != websocket.TextMessage || string(data) != "ping" {
		t.Fatalf("echo = type %d data %q", mt, data)
	}
	info, messages, sends, _ := rec.calls()
	if len(messages) != 1 || len(sends) != 1 || messages[0] != "ping" || sends[0] != "ping" {
		t.Fatalf("interceptor calls = messages %v sends %v", messages, sends)
	}
	// 帧回调收到的连接信息与附加时一致（OnConnect 由升级层调用，这里不做断言）。
	if info == nil || info.ID != sc.ID || info.Path != "/api/clients/v2/rpc" {
		t.Fatalf("frame info = %+v", info)
	}
}

func TestSafeConnPingHandlerWritesPongThroughInterceptor(t *testing.T) {
	pong := make(chan string, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetPongHandler(func(data string) error {
			pong <- data
			return nil
		})
		if err := conn.WriteControl(websocket.PingMessage, []byte("heartbeat"), time.Now().Add(time.Second)); err != nil {
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	sc := dialEcho(t, server)
	defer sc.Close()
	rec := &recordingInterceptor{}
	sc.SetInterceptor(&ConnInfo{ID: sc.ID, Path: "/x"}, rec)
	sc.SetPingHandler(func(data string) error {
		return sc.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Second))
	})
	if err := sc.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	readDone := make(chan error, 1)
	go func() {
		_, _, err := sc.ReadMessage()
		readDone <- err
	}()

	select {
	case data := <-pong:
		if data != "heartbeat" {
			t.Fatalf("pong data = %q, want heartbeat", data)
		}
	case <-time.After(time.Second):
		t.Fatal("did not receive pong")
	}
	_, _, sends, _ := rec.calls()
	if len(sends) != 1 || sends[0] != "heartbeat" {
		t.Fatalf("interceptor sends = %v, want heartbeat pong", sends)
	}

	_ = sc.Close()
	<-readDone
}

// TestSafeConnRewriteAndWriteJSON 验证帧改写（读侧）与 WriteJSON 走同一拦截通道。
func TestSafeConnRewriteAndWriteJSON(t *testing.T) {
	server := wsEchoServer(t)
	defer server.Close()
	sc := dialEcho(t, server)
	defer sc.Close()

	rec := &recordingInterceptor{rewriteMessage: true}
	sc.SetInterceptor(&ConnInfo{ID: sc.ID, Path: "/x"}, rec)

	if err := sc.WriteJSON(map[string]any{"a": 1}); err != nil {
		t.Fatal(err)
	}
	mt, data, err := sc.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if mt != websocket.TextMessage || !strings.HasPrefix(string(data), "rewritten:") {
		t.Fatalf("read = type %d data %q", mt, data)
	}
	// WriteJSON 编码后的 JSON 文本也经过 wsSend 钩子。
	_, sends, _, _ := rec.calls()
	if len(sends) != 1 || !strings.Contains(sends[0], "\"a\"") {
		t.Fatalf("sends = %v", sends)
	}
}

// TestSafeConnDropSkipsFrame 验证被丢弃的帧被透明跳过，读侧只看到后续帧。
func TestSafeConnDropSkipsFrame(t *testing.T) {
	server := wsEchoServer(t)
	defer server.Close()
	sc := dialEcho(t, server)
	defer sc.Close()

	rec := &recordingInterceptor{dropMessage: true}
	sc.SetInterceptor(&ConnInfo{ID: sc.ID, Path: "/x"}, rec)

	// 发三帧：全部被丢弃；读侧应在连续丢弃达到上限后收到错误，而不是空数据。
	if err := sc.WriteMessage(websocket.TextMessage, []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := sc.WriteMessage(websocket.TextMessage, []byte("b")); err != nil {
		t.Fatal(err)
	}
	if err := sc.WriteMessage(websocket.TextMessage, []byte("c")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxConsecutiveDroppedFrames-3; i++ {
		if err := sc.WriteMessage(websocket.TextMessage, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := sc.ReadMessage(); !errors.Is(err, errTooManyDroppedFrames) {
		t.Fatalf("expected errTooManyDroppedFrames, got %v", err)
	}
}

// TestSafeConnDropSend 验证写侧丢弃：被丢弃的帧不会发到对端，后续帧不受影响。
func TestSafeConnDropSend(t *testing.T) {
	server := wsEchoServer(t)
	defer server.Close()
	sc := dialEcho(t, server)
	defer sc.Close()

	rec := &recordingInterceptor{dropSend: true}
	sc.SetInterceptor(&ConnInfo{ID: sc.ID, Path: "/x"}, rec)

	if err := sc.WriteMessage(websocket.TextMessage, []byte("hidden")); err != nil {
		t.Fatal(err)
	}
	// 关闭丢弃后发送的帧应该正常到达服务端并回显。
	rec.mu.Lock()
	rec.dropSend = false
	rec.mu.Unlock()
	if err := sc.WriteMessage(websocket.TextMessage, []byte("visible")); err != nil {
		t.Fatal(err)
	}
	mt, data, err := sc.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "visible" || mt != websocket.TextMessage {
		t.Fatalf("echo = %q (dropped frame must not reach the peer)", data)
	}
}

// TestSafeConnNoInterceptorZeroCost 验证未挂拦截器时读写路径原样工作。
func TestSafeConnNoInterceptorZeroCost(t *testing.T) {
	server := wsEchoServer(t)
	defer server.Close()
	sc := dialEcho(t, server)
	defer sc.Close()

	if err := sc.WriteMessage(websocket.TextMessage, []byte("plain")); err != nil {
		t.Fatal(err)
	}
	_, data, err := sc.ReadMessage()
	if err != nil || string(data) != "plain" {
		t.Fatalf("read = %q err %v", data, err)
	}
}

// TestSafeConnCloseDoesNotWaitForWriteLock 是半开连接自死锁的回归测试：
// writeFrame 持有 sc.mu 且阻塞在内核写中（这里用直接持锁模拟）时，Close
// 必须仍能立刻关闭底层 socket 打断阻塞写，而不是等锁把自己拖死。
func TestSafeConnCloseDoesNotWaitForWriteLock(t *testing.T) {
	server := wsEchoServer(t)
	defer server.Close()
	sc := dialEcho(t, server)

	// 模拟一个卡在半开对端 write(2) 里的 writer：它持着 sc.mu 永不释放。
	sc.mu.Lock()
	closed := make(chan error, 1)
	go func() { closed <- sc.Close() }()
	select {
	case <-closed:
		// Close 立即返回，不等待写锁。
	case <-time.After(time.Second):
		sc.mu.Unlock()
		t.Fatal("Close blocked behind the write mutex — half-open deadlock regression")
	}
	sc.mu.Unlock()
}

// nonReadingPeerServer upgrades a connection and intentionally never reads
// from it, so a writer can fill the peer's TCP receive/send-buffer pipeline.
func nonReadingPeerServer(t *testing.T) *httptest.Server {
	t.Helper()
	var upgrader websocket.Upgrader
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		if tcp, ok := conn.UnderlyingConn().(*net.TCPConn); ok {
			_ = tcp.SetReadBuffer(4096)
		}
		defer conn.Close()
		<-done
	}))
}

// TestSafeConnWriteControlDeadlineAndCloseUnderTCPBackpressure uses a real
// non-reading peer with shrunk TCP buffers and one very large data frame. The
// frame blocks in the kernel rather than behind a test-controlled mutex. The
// test pins the MF-1 contract end to end: WriteControl obeys its caller
// deadline, no hidden force-close is armed by slow data writes, and an
// explicit Close immediately interrupts the half-open writer.
func TestSafeConnWriteControlDeadlineAndCloseUnderTCPBackpressure(t *testing.T) {
	server := nonReadingPeerServer(t)
	defer server.Close()
	sc := dialEcho(t, server)
	if tcp, ok := sc.GetConn().UnderlyingConn().(*net.TCPConn); ok {
		_ = tcp.SetWriteBuffer(4096)
	}

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- sc.WriteMessage(websocket.BinaryMessage, make([]byte, 16<<20))
	}()

	// Give the writer scheduler time to enter write(2), then verify that the
	// large frame is genuinely still in flight. Completing a 16 MiB frame to a
	// non-reading peer with 4 KiB buffers would itself indicate a test setup
	// problem, not the production behavior being validated.
	time.Sleep(30 * time.Millisecond)
	select {
	case err := <-writeDone:
		t.Fatalf("large frame unexpectedly completed before backpressure test (err=%v)", err)
	default:
	}

	controlStarted := time.Now()
	_ = sc.WriteControl(websocket.PingMessage, nil, time.Now().Add(60*time.Millisecond))
	controlElapsed := time.Since(controlStarted)
	if controlElapsed > 250*time.Millisecond {
		t.Fatalf("WriteControl took %s with a 60ms deadline under TCP backpressure", controlElapsed)
	}

	select {
	case <-sc.closeDone:
		t.Fatal("connection was force-closed without an explicit Close")
	default:
	}

	closeStarted := time.Now()
	if err := sc.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if elapsed := time.Since(closeStarted); elapsed > 150*time.Millisecond {
		t.Fatalf("Close took %s behind a half-open write", elapsed)
	}
	select {
	case <-writeDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Close did not interrupt the backpressured data writer")
	}
}

// TestSafeConnOnCloseOnce 验证 Close 只触发一次 OnClose。
func TestSafeConnOnCloseOnce(t *testing.T) {
	server := wsEchoServer(t)
	defer server.Close()
	sc := dialEcho(t, server)

	rec := &recordingInterceptor{}
	sc.SetInterceptor(&ConnInfo{ID: sc.ID, Path: "/x"}, rec)

	if err := sc.Close(); err != nil {
		t.Fatal(err)
	}
	// 第二次关闭连接已关闭：返回错误是预期行为，OnClose 只应触发一次。
	_ = sc.Close()
	_, _, _, closes := rec.calls()
	if closes != 1 {
		t.Fatalf("OnClose fired %d times", closes)
	}
}
