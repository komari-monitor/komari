package plugin

import (
	"time"

	"github.com/Calcium-Ion/moejs"
	"github.com/Calcium-Ion/moejs/engine"
	"github.com/gorilla/websocket"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
	"github.com/komari-monitor/komari/web/connection"
)

// WebSocket hook kinds. wsConnect runs right after the upgrade and may deny
// the connection; wsMessage/wsSend see every inbound/outbound frame;
// wsClose fires once when the connection ends. Path filtering reuses the
// HTTP matcher syntax without the method prefix, since every upgrade is a
// GET. Like the HTTP kinds, the input is lowercased, so any casing works
// from JS.
const (
	hookWSConnect hookKind = "wsconnect"
	hookWSMessage hookKind = "wsmessage"
	hookWSSend    hookKind = "wssend"
	hookWSClose   hookKind = "wsclose"
)

// wsFrameHookTimeout bounds one frame-level callback. The agent read pumps
// run on the agent read deadline; a frame hook must never stall
// them, so the wait is cut short and the frame passes through untouched.
const wsFrameHookTimeout = time.Second

// maxWSFrameBytes caps the payload copied into the plugin event loop; larger
// frames pass through without invoking the hooks.
const maxWSFrameBytes = 8 << 20

// isWSHookKind reports whether kind is one of the WebSocket hook kinds.
func isWSHookKind(kind string) bool {
	switch kind {
	case string(hookWSConnect), string(hookWSMessage), string(hookWSSend), string(hookWSClose):
		return true
	default:
		return false
	}
}

// OnConnect implements connection.FrameInterceptor. It runs the wsConnect
// hooks registered for the connection path; the first denial rejects the
// connection.
func (m *Manager) OnConnect(info *connection.ConnInfo) (deny bool, reason string) {
	for _, h := range m.wsHooksFor(hookWSConnect, info.Path) {
		var denied bool
		var why string
		queued, timedOut := runHookTurn(h.host, "plugin wsConnect hook "+h.short, func(vm *moejs.Runtime) {
			_ = h.host.RunJob(vm, "plugin wsConnect hook "+h.short, func() error {
				res, err := vm.CallFunction(h.fn, moejs.Undefined(), engine.ObjectValue(wsHookContext(vm, info)))
				if err != nil {
					return err
				}
				if res.IsUndefined() || res.IsNull() {
					return nil
				}
				obj := native.Object(vm, res)
				if v := native.Get(vm, obj, "deny"); native.Bool(v) {
					denied = true
				}
				if v := native.Get(vm, obj, "reason"); !v.IsUndefined() {
					why = native.String(vm, v)
				}
				return nil
			})
		})
		if !queued {
			continue // runtime closed; later plugins still get a chance
		}
		if timedOut {
			m.logWSHookTimeout(h)
			continue
		}
		if denied {
			return true, why
		}
	}
	return false, ""
}

// OnMessage implements connection.FrameInterceptor for inbound frames.
func (m *Manager) OnMessage(info *connection.ConnInfo, frameType int, data []byte) (int, []byte, bool) {
	return m.runFrameHooks(hookWSMessage, info, frameType, data)
}

// OnSend implements connection.FrameInterceptor for outbound frames.
func (m *Manager) OnSend(info *connection.ConnInfo, frameType int, data []byte) (int, []byte, bool) {
	return m.runFrameHooks(hookWSSend, info, frameType, data)
}

// runFrameHooks runs the wsMessage/wsSend chain for one frame in
// registration order; each hook sees the previous hook's replacement. A drop
// wins over later hooks. Timeouts, errors and oversized frames pass the
// frame through.
func (m *Manager) runFrameHooks(kind hookKind, info *connection.ConnInfo, frameType int, data []byte) (int, []byte, bool) {
	hooks := m.wsHooksFor(kind, info.Path)
	if len(hooks) == 0 || len(data) > maxWSFrameBytes {
		return frameType, data, false
	}
	next := data
	for _, h := range hooks {
		var drop bool
		var hookErr error
		queued, timedOut := runHookTurnTimeout(h.host, "plugin "+string(kind)+" hook "+h.short, wsFrameHookTimeout, func(vm *moejs.Runtime) {
			msg := wsFrameObject(vm, info, frameType, next)
			hookErr = h.host.RunJob(vm, "plugin "+string(kind)+" hook "+h.short, func() error {
				res, err := vm.CallFunction(h.fn, moejs.Undefined(), engine.ObjectValue(wsHookContext(vm, info)), engine.ObjectValue(msg))
				if err != nil {
					return err
				}
				if res.IsUndefined() || res.IsNull() {
					return nil
				}
				obj := native.Object(vm, res)
				if v := native.Get(vm, obj, "drop"); native.Bool(v) {
					drop = true
					return nil
				}
				if v := native.Get(vm, obj, "type"); !v.IsUndefined() {
					frameType = int(native.Int(vm, v))
				}
				if v := native.Get(vm, obj, "data"); !v.IsUndefined() && !v.IsNull() {
					if data := wsFrameBytes(vm, v); data != nil {
						next = data
					}
				}
				return nil
			})
		})
		if !queued {
			return frameType, next, false // runtime closed: pass the frame on
		}
		if timedOut {
			m.logWSHookTimeout(h)
			continue
		}
		if hookErr != nil {
			m.logHookError(h, hookErr)
			continue
		}
		if drop {
			return frameType, nil, true
		}
	}
	return frameType, next, false
}

// OnClose implements connection.FrameInterceptor for connection teardown.
func (m *Manager) OnClose(info *connection.ConnInfo) {
	for _, h := range m.wsHooksFor(hookWSClose, info.Path) {
		var hookErr error
		queued, timedOut := runHookTurn(h.host, "plugin wsClose hook "+h.short, func(vm *moejs.Runtime) {
			hookErr = h.host.RunJob(vm, "plugin wsClose hook "+h.short, func() error {
				_, err := vm.CallFunction(h.fn, moejs.Undefined(), engine.ObjectValue(wsHookContext(vm, info)))
				return err
			})
		})
		if !queued {
			return
		}
		if timedOut {
			m.logWSHookTimeout(h)
			continue
		}
		if hookErr != nil {
			m.logHookError(h, hookErr)
		}
	}
}

// wsHooksFor returns the hooks of one ws kind whose matcher matches the
// connection path, in registration order.
func (m *Manager) wsHooksFor(kind hookKind, path string) []*hookEntry {
	all := m.hooksOf(kind)
	kept := all[:0]
	for _, h := range all {
		if h.matcher == nil || h.matcher.matchesPath(path) {
			kept = append(kept, h)
		}
	}
	return kept
}

// wsHookContext builds the immutable per-connection context object.
func wsHookContext(vm *moejs.Runtime, info *connection.ConnInfo) *engine.Object {
	ctx := vm.Realm().NewObject()
	_ = native.Set(vm, ctx, "path", info.Path)
	_ = native.Set(vm, ctx, "connId", info.ID)
	_ = native.Set(vm, ctx, "remoteIp", info.RemoteIP)
	_ = native.Set(vm, ctx, "userAgent", info.UserAgent)
	if info.ClientUUID != "" {
		_ = native.Set(vm, ctx, "clientUuid", info.ClientUUID)
	}
	return ctx
}

// wsFrameObject builds the frame object handed to wsMessage/wsSend hooks.
// Text frames arrive as strings, binary frames as ArrayBuffers.
func wsFrameObject(vm *moejs.Runtime, info *connection.ConnInfo, frameType int, data []byte) *engine.Object {
	msg := vm.Realm().NewObject()
	_ = native.Set(vm, msg, "type", frameType)
	if frameType == websocket.BinaryMessage {
		_ = native.Set(vm, msg, "data", newFrameBuffer(vm, data))
	} else {
		_ = native.Set(vm, msg, "data", string(data))
	}
	_ = native.Set(vm, msg, "connId", info.ID)
	_ = native.Set(vm, msg, "path", info.Path)
	return msg
}

// wsFrameBytes converts a hook-returned payload (string or ArrayBuffer) back
// to bytes; anything else reports nil and leaves the frame untouched.
func wsFrameBytes(vm *moejs.Runtime, v moejs.Value) []byte {
	if v.IsString() {
		return []byte(native.String(vm, v))
	}
	var data []byte
	if err := native.ExportTo(vm, v, &data); err == nil {
		return data
	}
	return nil
}

// logWSHookTimeout records a timed-out ws hook for the plugin console.
func (m *Manager) logWSHookTimeout(h *hookEntry) {
	_, _ = m.logStore(h.short).Write([]byte("[plugin] " + string(h.kind) + " hook timed out; the message passed through unmodified\n"))
}

// The Manager is the process-wide WebSocket frame interceptor.
var _ connection.FrameInterceptor = (*Manager)(nil)

func newFrameBuffer(vm *moejs.Runtime, data []byte) moejs.Value {
	obj, err := vm.Realm().NewArrayBuffer(append([]byte(nil), data...))
	if err != nil {
		panic(err)
	}
	return engine.ObjectValue(obj)
}
