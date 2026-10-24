package plugin

import (
	"errors"
	"fmt"
	"math"
	"net"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Calcium-Ion/moejs"
	"github.com/Calcium-Ion/moejs/engine"
	"github.com/gorilla/websocket"
	"github.com/komari-monitor/komari/pkg/jsruntime"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
	"github.com/komari-monitor/komari/web/connection"
)

const (
	routeWSOpen            = 1
	routeWSClosing         = 2
	routeWSClosed          = 3
	routeWSMaxQueuedFrames = 64
	routeWSMaxQueuedBytes  = 16 << 20
	routeWSWriteTimeout    = 5 * time.Second
	routeWSCloseTimeout    = 5 * time.Second
)

type routeWSFrame struct {
	kind int
	data []byte
}

type routeWSCloseEvent struct {
	code    int
	reason  string
	clean   bool
	message string
}

// routeWebSocket is registered before hijacking: runtime shutdown must be able
// to close the network connection even while UpgradeSafeConn waits for a
// synchronous wsConnect hook. No SafeConn operation or hook runs on the VM loop.
// mu protects transport state only; jsObject belongs exclusively to the loop.
type routeWebSocket struct {
	host *jsruntime.Host
	mu   sync.Mutex

	resourceID uint64
	netConn    net.Conn
	conn       *connection.SafeConn
	raw        *websocket.Conn
	state      int
	started    bool
	finished   bool
	notified   bool
	result     routeWSCloseEvent
	closeTimer *time.Timer

	queue       []routeWSFrame
	queuedCount int // includes the writer's in-flight frame
	queuedBytes int
	wake        chan struct{}
	done        chan struct{}

	closeRequested bool
	closeCode      int
	closeReason    string
	closeWritten   bool
	peerClosed     bool
	peerCode       int
	peerReason     string
	pong           string
	pongPending    bool

	jsObject *engine.Object
}

func newRouteWebSocket(host *jsruntime.Host) *routeWebSocket {
	ws := &routeWebSocket{
		host: host, state: routeWSClosed,
		wake: make(chan struct{}, 1), done: make(chan struct{}),
	}
	// AddResource may call abort synchronously, or shutdown may race with its
	// return. Never publish the ID without checking whether teardown already ran.
	id := host.AddResource(ws.abort)
	ws.mu.Lock()
	ws.resourceID = id
	finished := ws.finished
	ws.mu.Unlock()
	if finished {
		host.RemoveResource(id)
	}
	return ws
}

// captureConn is called by the HTTP writer's Hijack method, before the
// upgrader can enter any hooks. It also handles shutdown winning that race.
func (ws *routeWebSocket) captureConn(conn net.Conn) {
	ws.mu.Lock()
	finished := ws.finished
	if !finished {
		ws.netConn = conn
	}
	ws.mu.Unlock()
	if finished && conn != nil {
		_ = conn.Close()
	}
}

// attach runs on the HTTP pump, never the VM loop: GetConn takes SafeConn's
// write mutex. The underlying Gorilla connection is private to these pumps.
func (ws *routeWebSocket) attach(conn *connection.SafeConn) bool {
	if conn == nil {
		return false
	}
	raw := conn.GetConn()
	raw.SetReadLimit(maxWSFrameBytes)
	raw.SetCloseHandler(func(code int, reason string) error {
		ws.mu.Lock()
		if !ws.finished {
			ws.peerClosed, ws.peerCode, ws.peerReason = true, code, reason
			// The peer cannot consume further data. Drop queued frames and
			// prioritize the close reply (the in-flight writer stays counted).
			for _, frame := range ws.queue {
				ws.queuedCount--
				ws.queuedBytes -= len(frame.data)
			}
			ws.queue = nil
			ws.pong, ws.pongPending = "", false
			if !ws.closeRequested {
				ws.requestCloseLocked(code, reason)
			}
		}
		ws.mu.Unlock()
		ws.signal()
		return nil // the writer, not the reader, sends the close reply
	})
	raw.SetPingHandler(func(data string) error {
		ws.mu.Lock()
		if !ws.finished {
			// A ping flood must not allocate an unbounded control queue. RFC 6455
			// allows replying only to the most recently processed ping.
			ws.pong, ws.pongPending = data, true
		}
		ws.mu.Unlock()
		ws.signal()
		return nil
	})

	ws.mu.Lock()
	if ws.finished || ws.conn != nil {
		ws.mu.Unlock()
		_ = raw.Close()
		go func() { _ = conn.Close() }()
		return false
	}
	ws.conn, ws.raw, ws.state = conn, raw, routeWSOpen
	if ws.netConn == nil {
		ws.netConn = raw.UnderlyingConn()
	}
	ws.mu.Unlock()
	return true
}

// abort is a non-waiting resource callback. Close the captured network socket
// first to unblock I/O; SafeConn.Close may wait for same-plugin hooks, so it
// must happen separately, after the socket is already closed.
func (ws *routeWebSocket) abort() {
	ws.finish(routeWSCloseEvent{code: websocket.CloseAbnormalClosure})
}

func (ws *routeWebSocket) finish(result routeWSCloseEvent) {
	ws.mu.Lock()
	if ws.finished {
		ws.mu.Unlock()
		return
	}
	ws.finished, ws.state, ws.result = true, routeWSClosed, result
	ws.queue = nil
	ws.queuedCount, ws.queuedBytes = 0, 0
	if ws.closeTimer != nil {
		ws.closeTimer.Stop()
	}
	netConn, conn, id := ws.netConn, ws.conn, ws.resourceID
	notify := ws.started && !ws.notified
	if notify {
		ws.notified = true
	}
	ws.mu.Unlock()

	if netConn != nil {
		_ = netConn.Close()
	}
	ws.host.RemoveResource(id)
	close(ws.done)
	if conn != nil {
		go func() { _ = conn.Close() }()
	}
	if notify {
		ws.notifyClose(result)
	}
}

// start is called on a later loop turn than promise resolution. This barrier
// lets .then/await reactions install handlers before any data or close event.
func (ws *routeWebSocket) start() {
	ws.mu.Lock()
	if ws.started {
		ws.mu.Unlock()
		return
	}
	ws.started = true
	if ws.finished {
		notify, result := !ws.notified, ws.result
		ws.notified = true
		ws.mu.Unlock()
		if notify {
			ws.notifyClose(result)
		}
		return
	}
	attached := ws.conn != nil
	ws.mu.Unlock()
	if !attached || !ws.host.ResourcesOpen() {
		ws.abort()
		return
	}
	go ws.writePump()
	go ws.readPump()
}

func (ws *routeWebSocket) signal() {
	select {
	case ws.wake <- struct{}{}:
	default:
	}
}

func (ws *routeWebSocket) requestCloseLocked(code int, reason string) {
	ws.state, ws.closeRequested = routeWSClosing, true
	ws.closeCode, ws.closeReason = code, reason
	ws.closeTimer = time.AfterFunc(routeWSCloseTimeout, func() {
		ws.fail(errors.New("WebSocket close handshake timed out"))
	})
}

func (ws *routeWebSocket) fail(err error) {
	ws.finish(routeWSCloseEvent{code: websocket.CloseAbnormalClosure, message: err.Error()})
}

func (ws *routeWebSocket) writePump() {
	for {
		ws.mu.Lock()
		if ws.finished {
			ws.mu.Unlock()
			return
		}
		if ws.closeWritten && ws.peerClosed {
			result := routeWSCloseEvent{code: ws.peerCode, reason: ws.peerReason, clean: true}
			ws.mu.Unlock()
			ws.finish(result)
			return
		}
		var frame routeWSFrame
		control, haveFrame := false, false
		switch {
		case ws.pongPending && !ws.closeWritten:
			frame = routeWSFrame{kind: websocket.PongMessage, data: []byte(ws.pong)}
			ws.pong, ws.pongPending, control, haveFrame = "", false, true, true
		case len(ws.queue) != 0:
			frame, haveFrame = ws.queue[0], true
			ws.queue[0] = routeWSFrame{}
			ws.queue = ws.queue[1:]
		case ws.closeRequested && !ws.closeWritten:
			frame = routeWSFrame{kind: websocket.CloseMessage, data: websocket.FormatCloseMessage(ws.closeCode, ws.closeReason)}
			control, haveFrame = true, true
		}
		conn, raw := ws.conn, ws.raw
		ws.mu.Unlock()
		if !haveFrame {
			select {
			case <-ws.wake:
			case <-ws.done:
				return
			}
			continue
		}

		deadline := time.Now().Add(routeWSWriteTimeout)
		var err error
		if control {
			err = conn.WriteControl(frame.kind, frame.data, deadline)
		} else {
			err = raw.SetWriteDeadline(deadline)
			if err == nil {
				err = conn.WriteMessage(frame.kind, frame.data)
			}
		}
		if err != nil {
			ws.fail(err)
			return
		}
		ws.mu.Lock()
		if !ws.finished {
			if frame.kind == websocket.CloseMessage {
				ws.closeWritten = true
			} else if !control {
				ws.queuedCount--
				ws.queuedBytes -= len(frame.data)
			}
		}
		ws.mu.Unlock()
	}
}

func (ws *routeWebSocket) readPump() {
	for {
		kind, data, err := ws.conn.ReadMessage()
		if err != nil {
			ws.mu.Lock()
			peerClosed := ws.peerClosed
			ws.mu.Unlock()
			if peerClosed {
				ws.signal() // the writer completes the close handshake
			} else {
				ws.fail(err)
			}
			return
		}
		if len(data) > maxWSFrameBytes {
			ws.fail(fmt.Errorf("WebSocket message exceeds %d bytes", maxWSFrameBytes))
			return
		}
		if kind != websocket.TextMessage && kind != websocket.BinaryMessage {
			continue
		}
		if kind == websocket.TextMessage && !utf8.Valid(data) {
			ws.fail(errors.New("WebSocket text message is not valid UTF-8"))
			return
		}
		ack := make(chan struct{})
		if !ws.host.RunOnLoop(func(vm *moejs.Runtime) {
			defer close(ack)
			jobErr := ws.host.RunJob(vm, "plugin WebSocket.onmessage", func() error {
				ws.mu.Lock()
				open := ws.state == routeWSOpen
				ws.mu.Unlock()
				if !open || ws.jsObject == nil {
					return nil
				}
				var value any = string(data)
				if kind == websocket.BinaryMessage {
					buffer, err := vm.Realm().NewArrayBuffer(data)
					if err != nil {
						return err
					}
					value = engine.ObjectValue(buffer)
				}
				event := vm.Realm().NewObject()
				if err := native.Set(vm, event, "data", value); err != nil {
					return err
				}
				return ws.callHandler(vm, "onmessage", event)
			})
			if jobErr != nil {
				ws.fail(jobErr)
			}
		}) {
			ws.abort()
			return
		}
		// Only one inbound payload may wait for JavaScript. Cancellation must
		// also release this reader when queued jobs are discarded on shutdown.
		select {
		case <-ack:
		case <-ws.done:
			return
		}
	}
}

// All methods below that touch jsObject or JS values run only on the host loop.
func (ws *routeWebSocket) object(vm *moejs.Runtime) *engine.Object {
	if ws.jsObject != nil {
		return ws.jsObject
	}
	obj := vm.Realm().NewObject()
	ws.jsObject = obj
	for _, name := range []string{"onmessage", "onerror", "onclose"} {
		_ = native.Set(vm, obj, name, moejs.Null())
	}
	getter := native.Bind(vm, "get readyState", 0, func(_ *moejs.Realm, _ moejs.Value, _ []moejs.Value) (moejs.Value, error) {
		ws.mu.Lock()
		state := ws.state
		ws.mu.Unlock()
		return native.From(vm, state), nil
	})
	desc := engine.AccessorDescriptor(getter.AsObject(), nil, 0)
	desc.SetEnumerable(true)
	if err := obj.DefinePropertyOrThrow(vm.Realm(), vm.Realm().KeyFromGoString("readyState"), desc); err != nil {
		panic(err)
	}
	_ = native.Function(vm, obj, "send", func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		value := moejs.Arg(args, 0)
		kind := websocket.BinaryMessage
		var data []byte
		if value.IsString() {
			kind = websocket.TextMessage
			text := native.String(vm, value)
			if len(text) > maxWSFrameBytes {
				return moejs.Undefined(), native.TypeError(vm, "WebSocket message exceeds %d bytes", maxWSFrameBytes)
			}
			data = []byte(text)
		} else if value.IsObject() {
			var ok bool
			data, ok = value.AsObject().BufferData()
			if !ok {
				return moejs.Undefined(), native.TypeError(vm, "WebSocket.send requires a string, ArrayBuffer or ArrayBuffer view")
			}
		} else {
			return moejs.Undefined(), native.TypeError(vm, "WebSocket.send requires a string, ArrayBuffer or ArrayBuffer view")
		}
		if len(data) > maxWSFrameBytes {
			return moejs.Undefined(), native.TypeError(vm, "WebSocket message exceeds %d bytes", maxWSFrameBytes)
		}
		ws.mu.Lock()
		if ws.state != routeWSOpen {
			ws.mu.Unlock()
			return moejs.Undefined(), native.TypeError(vm, "WebSocket is not open")
		}
		if ws.queuedCount >= routeWSMaxQueuedFrames || len(data) > routeWSMaxQueuedBytes-ws.queuedBytes {
			ws.mu.Unlock()
			return moejs.Undefined(), native.TypeError(vm, "WebSocket send queue is full")
		}
		ws.queue = append(ws.queue, routeWSFrame{kind: kind, data: append([]byte(nil), data...)})
		ws.queuedCount++
		ws.queuedBytes += len(data)
		ws.mu.Unlock()
		ws.signal()
		return moejs.Undefined(), nil
	})
	_ = native.Function(vm, obj, "close", func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		code := websocket.CloseNormalClosure
		if v := moejs.Arg(args, 0); !v.IsUndefined() {
			number := native.Float(vm, v)
			if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number || (number != 1000 && (number < 3000 || number > 4999)) {
				return moejs.Undefined(), native.TypeError(vm, "WebSocket close code must be 1000 or between 3000 and 4999")
			}
			code = int(number)
		}
		reason := ""
		if v := moejs.Arg(args, 1); !v.IsUndefined() {
			reason = native.String(vm, v)
		}
		if len(reason) > 123 || !utf8.ValidString(reason) {
			return moejs.Undefined(), native.TypeError(vm, "WebSocket close reason must be valid UTF-8 and at most 123 bytes")
		}
		ws.mu.Lock()
		if ws.state == routeWSOpen {
			ws.requestCloseLocked(code, reason)
		}
		ws.mu.Unlock()
		ws.signal()
		return moejs.Undefined(), nil
	})
	return obj
}

func (ws *routeWebSocket) callHandler(vm *moejs.Runtime, name string, event *engine.Object) error {
	if ws.jsObject == nil {
		return nil
	}
	callback, err := vm.Get(engine.ObjectValue(ws.jsObject), name)
	if err != nil || !engine.IsCallable(callback) {
		return err
	}
	_, err = vm.CallFunction(callback, engine.ObjectValue(ws.jsObject), engine.ObjectValue(event))
	return err
}

func (ws *routeWebSocket) notifyClose(result routeWSCloseEvent) {
	ws.host.RunOnLoop(func(vm *moejs.Runtime) {
		if ws.jsObject == nil {
			return
		}
		defer func() { ws.jsObject = nil }()
		if result.message != "" {
			_ = ws.host.RunJob(vm, "plugin WebSocket.onerror", func() error {
				event := vm.Realm().NewObject()
				if err := native.Set(vm, event, "message", result.message); err != nil {
					return err
				}
				return ws.callHandler(vm, "onerror", event)
			})
		}
		// A throwing onerror must not suppress the final onclose callback.
		_ = ws.host.RunJob(vm, "plugin WebSocket.onclose", func() error {
			event := vm.Realm().NewObject()
			_ = native.Set(vm, event, "code", result.code)
			_ = native.Set(vm, event, "reason", result.reason)
			_ = native.Set(vm, event, "wasClean", result.clean)
			return ws.callHandler(vm, "onclose", event)
		})
	})
}
