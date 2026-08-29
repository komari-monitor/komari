package plugin

import (
	"time"

	"github.com/dop251/goja"
	"github.com/gorilla/websocket"
	"github.com/komari-monitor/komari/web/connection"
)

// WebSocket hook kinds. wsConnect runs right after the upgrade and may deny
// the connection; wsMessage/wsSend see every inbound/outbound frame;
// wsClose fires once when the connection ends. Path filtering reuses the
// HTTP matcher syntax without the method prefix, since every upgrade is a
// GET. Like the HTTP kinds, the input is lowercased, so any casing works
// from JS.
const (
	hookWSConnect  hookKind = "wsconnect"
	hookWSMessage  hookKind = "wsmessage"
	hookWSSend     hookKind = "wssend"
	hookWSClose    hookKind = "wsclose"
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
		queued, timedOut := runHookTurn(h.host, "plugin wsConnect hook "+h.short, func(vm *goja.Runtime) {
			_ = h.host.RunJob(vm, "plugin wsConnect hook "+h.short, func() error {
				res, err := h.fn(goja.Undefined(), wsHookContext(vm, info))
				if err != nil {
					return err
				}
				if goja.IsUndefined(res) || goja.IsNull(res) {
					return nil
				}
				obj := res.ToObject(vm)
				if v := obj.Get("deny"); v != nil && v.ToBoolean() {
					denied = true
				}
				if v := obj.Get("reason"); v != nil && !goja.IsUndefined(v) {
					why = v.String()
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
		queued, timedOut := runHookTurnTimeout(h.host, "plugin "+string(kind)+" hook "+h.short, wsFrameHookTimeout, func(vm *goja.Runtime) {
			msg := wsFrameObject(vm, info, frameType, next)
			hookErr = h.host.RunJob(vm, "plugin "+string(kind)+" hook "+h.short, func() error {
				res, err := h.fn(goja.Undefined(), wsHookContext(vm, info), msg)
				if err != nil {
					return err
				}
				if goja.IsUndefined(res) || goja.IsNull(res) {
					return nil
				}
				obj := res.ToObject(vm)
				if v := obj.Get("drop"); v != nil && v.ToBoolean() {
					drop = true
					return nil
				}
				if v := obj.Get("type"); v != nil && !goja.IsUndefined(v) {
					frameType = int(v.ToInteger())
				}
				if v := obj.Get("data"); v != nil && !goja.IsUndefined(v) && !goja.IsNull(v) {
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
		queued, timedOut := runHookTurn(h.host, "plugin wsClose hook "+h.short, func(vm *goja.Runtime) {
			hookErr = h.host.RunJob(vm, "plugin wsClose hook "+h.short, func() error {
				_, err := h.fn(goja.Undefined(), wsHookContext(vm, info))
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
func wsHookContext(vm *goja.Runtime, info *connection.ConnInfo) *goja.Object {
	ctx := vm.NewObject()
	_ = ctx.Set("path", info.Path)
	_ = ctx.Set("connId", info.ID)
	_ = ctx.Set("remoteIp", info.RemoteIP)
	_ = ctx.Set("userAgent", info.UserAgent)
	if info.ClientUUID != "" {
		_ = ctx.Set("clientUuid", info.ClientUUID)
	}
	return ctx
}

// wsFrameObject builds the frame object handed to wsMessage/wsSend hooks.
// Text frames arrive as strings, binary frames as ArrayBuffers.
func wsFrameObject(vm *goja.Runtime, info *connection.ConnInfo, frameType int, data []byte) *goja.Object {
	msg := vm.NewObject()
	_ = msg.Set("type", frameType)
	if frameType == websocket.BinaryMessage {
		_ = msg.Set("data", vm.NewArrayBuffer(append([]byte(nil), data...)))
	} else {
		_ = msg.Set("data", string(data))
	}
	_ = msg.Set("connId", info.ID)
	_ = msg.Set("path", info.Path)
	return msg
}

// wsFrameBytes converts a hook-returned payload (string or ArrayBuffer) back
// to bytes; anything else reports nil and leaves the frame untouched.
func wsFrameBytes(vm *goja.Runtime, v goja.Value) []byte {
	if goja.IsString(v) {
		return []byte(v.String())
	}
	var data []byte
	if err := vm.ExportTo(v, &data); err == nil {
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
