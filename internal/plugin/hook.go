package plugin

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/komari-monitor/komari/pkg/jsruntime"
	"github.com/komari-monitor/komari/pkg/jsruntime/httpbody"
)

// hookKind identifies the HTTP hook phase.
type hookKind string

const (
	hookRequest  hookKind = "request"
	hookResponse hookKind = "response"
)

// hookEntry is one registered JavaScript hook.
type hookEntry struct {
	short     string
	kind      hookKind
	fn        goja.Callable
	host      *jsruntime.Host
	matcher   *hookMatcher // nil matches every request
	bodyLimit int64        // plugin-declared maxHTTPBodyBytes (0 = default)
}

// hookMatcher optionally restricts a hook to matching requests. A nil
// matcher matches every request.
type hookMatcher struct {
	method string // optional HTTP method; "" matches any
	path   string // cleaned request path, lowercase
	prefix bool   // subtree match when the pattern ended with "*"
}

// parseHookMatcher parses a server.hook path filter: "POST /api/foo",
// "/api/foo" or "/api/foo/*". The optional leading HTTP method restricts the
// hook to one request method; a trailing "*" matches the path and its whole
// subtree; any other pattern is an exact path match.
func parseHookMatcher(pattern string) (*hookMatcher, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, errors.New("hook path must not be empty")
	}
	m := &hookMatcher{}
	if method, rest, ok := strings.Cut(pattern, " "); ok {
		method = strings.ToUpper(strings.TrimSpace(method))
		if isHTTPMethod(method) {
			m.method = method
			pattern = strings.TrimSpace(rest)
		}
	}
	if !strings.HasPrefix(pattern, "/") {
		return nil, fmt.Errorf("hook path %q must start with /", pattern)
	}
	if strings.HasSuffix(pattern, "*") {
		m.prefix = true
		pattern = strings.TrimSuffix(pattern, "*")
	}
	pattern = strings.TrimRight(pattern, "/")
	if pattern == "" {
		if m.prefix {
			return m, nil // "/*" matches every path
		}
		return nil, fmt.Errorf("hook path %q is invalid", pattern)
	}
	if !filepath.IsLocal(strings.TrimPrefix(pattern, "/")) {
		return nil, fmt.Errorf("hook path %q contains invalid segments", pattern)
	}
	m.path = strings.ToLower(pattern)
	return m, nil
}

// matchesPath reports whether a request path matches the filter. A nil
// matcher matches everything.
func (m *hookMatcher) matchesPath(path string) bool {
	if m == nil {
		return true
	}
	if path == "" {
		path = "/"
	}
	path = strings.ToLower(path)
	if m.prefix {
		return path == m.path || strings.HasPrefix(path, m.path+"/")
	}
	return path == m.path
}

// matches reports whether the request matches the filter. A nil matcher
// matches everything.
func (m *hookMatcher) matches(r *http.Request) bool {
	if m == nil {
		return true
	}
	if m.method != "" && r.Method != m.method {
		return false
	}
	return m.matchesPath(r.URL.Path)
}

// isHTTPMethod reports whether s is a known HTTP request method.
func isHTTPMethod(s string) bool {
	switch s {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return true
	default:
		return false
	}
}

// matches reports whether the hook applies to the request: no matcher, or a
// matching filter.
func (h *hookEntry) matches(r *http.Request) bool {
	return h.matcher == nil || h.matcher.matches(r)
}

// filterHooks drops the hooks whose matcher does not match the request.
func filterHooks(hooks []*hookEntry, r *http.Request) []*hookEntry {
	kept := hooks[:0]
	for _, hook := range hooks {
		if hook.matches(r) {
			kept = append(kept, hook)
		}
	}
	return kept
}

// maxHookBodyLimit returns the body read limit for the request hooks that
// will run: the largest plugin-declared maxHTTPBodyBytes, or the default
// when no hook declares a limit.
func maxHookBodyLimit(hooks []*hookEntry) int64 {
	var limit int64
	for _, hook := range hooks {
		if hook.bodyLimit > limit {
			limit = hook.bodyLimit
		}
	}
	if limit < 1 {
		return defaultMaxHTTPBodyBytes
	}
	return limit
}

// wrapHandler installs the hook chain around the application handler. WebSocket
// upgrades pass through untouched because the response cannot be buffered.
// Hooks with a path/method filter only run for matching requests; unfiltered
// requests skip both the hook chain and its body buffering.
func (m *Manager) wrapHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isUpgradeRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		reqHooks := filterHooks(m.hooksOf(hookRequest), r)
		respHooks := filterHooks(m.hooksOf(hookResponse), r)
		if len(reqHooks) == 0 && len(respHooks) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		req := r
		if len(reqHooks) > 0 {
			body, err := httpbody.ReadAll(r.Body, maxHookBodyLimit(reqHooks))
			if err != nil {
				http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
				return
			}
			for _, hook := range reqHooks {
				body, err = m.runRequestHook(req, body, hook)
				if err != nil {
					m.logHookError(hook, err)
					http.Error(w, "plugin request hook failed", http.StatusInternalServerError)
					return
				}
			}
			req = requestWithBody(req, body)
		}

		if len(respHooks) == 0 {
			next.ServeHTTP(w, req)
			return
		}
		bw := newBufferedResponseWriter(w)
		next.ServeHTTP(bw, req)
		for _, hook := range respHooks {
			if err := m.runResponseHook(req, bw, hook); err != nil {
				m.logHookError(hook, err)
			}
		}
		bw.flushTo(w)
	})
}

// runRequestHook runs one request hook on its plugin event loop, applies the
// JS-side modifications to the request, and returns the (possibly replaced)
// request body.
func (m *Manager) runRequestHook(r *http.Request, body []byte, h *hookEntry) ([]byte, error) {
	nextBody := body
	var hookErr error
	queued, timedOut := runHookTurn(h.host, "plugin request hook "+h.short, func(vm *goja.Runtime) {
		reqObj := hookRequestObject(vm, r, nextBody)
		hookErr = h.host.RunJob(vm, "plugin request hook "+h.short, func() error {
			_, err := h.fn(goja.Undefined(), reqObj)
			return err
		})
		if hookErr != nil {
			return
		}
		if value := reqObj.Get("method"); value != nil && !goja.IsUndefined(value) {
			if method := strings.ToUpper(strings.TrimSpace(value.String())); method != "" {
				r.Method = method
			}
		}
		if value := reqObj.Get("url"); value != nil && !goja.IsUndefined(value) {
			if raw := strings.TrimSpace(value.String()); raw != "" {
				if parsed, err := url.Parse(raw); err == nil {
					r.URL = parsed
					r.RequestURI = raw
				}
			}
		}
		if value := reqObj.Get("headers"); value != nil && !goja.IsUndefined(value) {
			var raw map[string]any
			if err := vm.ExportTo(value, &raw); err == nil {
				r.Header = normalizeHeader(raw)
			}
		}
		if value := reqObj.Get("body"); value != nil && !goja.IsUndefined(value) {
			nextBody = []byte(value.String())
		}
	})
	if !queued {
		return body, errors.New("plugin runtime is closed")
	}
	if timedOut {
		return body, errors.New("plugin request hook timed out")
	}
	return nextBody, hookErr
}

// runResponseHook runs one response hook on its plugin event loop and applies
// the JS-side modifications to the captured response.
func (m *Manager) runResponseHook(r *http.Request, bw *bufferedResponseWriter, h *hookEntry) error {
	var hookErr error
	queued, timedOut := runHookTurn(h.host, "plugin response hook "+h.short, func(vm *goja.Runtime) {
		resObj := hookResponseObject(vm, bw)
		hookErr = h.host.RunJob(vm, "plugin response hook "+h.short, func() error {
			_, err := h.fn(goja.Undefined(), hookRequestObject(vm, r, nil), resObj)
			return err
		})
		if hookErr != nil {
			return
		}
		status := bw.status()
		if value := resObj.Get("statusCode"); value != nil && !goja.IsUndefined(value) {
			status = int(value.ToInteger())
		}
		header := bw.Header().Clone()
		if value := resObj.Get("headers"); value != nil && !goja.IsUndefined(value) {
			var raw map[string]any
			if err := vm.ExportTo(value, &raw); err == nil {
				header = normalizeHeader(raw)
			}
		}
		responseBody := bw.bodyBytes()
		if value := resObj.Get("body"); value != nil && !goja.IsUndefined(value) {
			responseBody = []byte(value.String())
		}
		bw.apply(status, header, responseBody)
	})
	if !queued {
		return errors.New("plugin runtime is closed")
	}
	if timedOut {
		return errors.New("plugin response hook timed out")
	}
	if bw.passedThrough() {
		_, _ = m.logStore(h.short).Write([]byte(
			fmt.Sprintf("[plugin] response exceeded the %d-byte hook buffer limit and was sent to the client unmodified\n", maxHookBufferBytes)))
	}
	return hookErr
}

// runHookTurn queues one hook callback on the plugin event loop and waits
// for it with the runtime timeout. It reports whether the job was queued and
// whether it timed out.
func runHookTurn(host *jsruntime.Host, name string, job func(vm *goja.Runtime)) (queued, timedOut bool) {
	return runHookTurnTimeout(host, name, host.Timeout(), job)
}

// runHookTurnTimeout is runHookTurn with an explicit wait budget. The
// callback keeps running on the plugin loop after a timeout; the waiter gives
// up and the caller falls back to passing the message through.
func runHookTurnTimeout(host *jsruntime.Host, name string, timeout time.Duration, job func(vm *goja.Runtime)) (queued, timedOut bool) {
	done := make(chan struct{})
	if !host.RunOnLoop(func(vm *goja.Runtime) {
		defer close(done)
		job(vm)
	}) {
		return false, false
	}
	select {
	case <-done:
		return true, false
	case <-time.After(timeout):
		return true, true
	}
}

func (m *Manager) logHookError(h *hookEntry, err error) {
	_, _ = m.logStore(h.short).Write([]byte("[plugin] hook error: " + err.Error() + "\n"))
}

// hookRequestObject builds the mutable JS request object.
func hookRequestObject(vm *goja.Runtime, r *http.Request, body []byte) *goja.Object {
	req := vm.NewObject()
	_ = req.Set("method", r.Method)
	_ = req.Set("url", r.URL.RequestURI())
	_ = req.Set("headers", headerToMap(r.Header))
	query := make(map[string]string, len(r.URL.Query()))
	for name, values := range r.URL.Query() {
		query[name] = strings.Join(values, ",")
	}
	_ = req.Set("query", query)
	_ = req.Set("body", string(body))
	_ = req.Set("context", hookRequestContext(vm, r))
	return req
}

// hookResponseObject builds the mutable JS response object from the captured
// response.
func hookResponseObject(vm *goja.Runtime, bw *bufferedResponseWriter) *goja.Object {
	res := vm.NewObject()
	_ = res.Set("statusCode", bw.status())
	_ = res.Set("statusMessage", "")
	_ = res.Set("headers", headerToMap(bw.Header()))
	_ = res.Set("body", string(bw.bodyBytes()))
	return res
}

func requestWithBody(r *http.Request, body []byte) *http.Request {
	clone := r.Clone(r.Context())
	clone.Body = io.NopCloser(bytes.NewReader(body))
	clone.ContentLength = int64(len(body))
	return clone
}

func normalizeHeader(raw map[string]any) http.Header {
	header := make(http.Header, len(raw))
	for name, value := range raw {
		switch values := value.(type) {
		case string:
			header.Set(name, values)
		case []any:
			for _, item := range values {
				header.Add(name, fmt.Sprint(item))
			}
		case []string:
			for _, item := range values {
				header.Add(name, item)
			}
		default:
			header.Set(name, fmt.Sprint(value))
		}
	}
	return header
}

func headerToMap(header http.Header) map[string]any {
	out := make(map[string]any, len(header))
	for name, values := range header {
		if len(values) == 1 {
			out[strings.ToLower(name)] = values[0]
		} else {
			out[strings.ToLower(name)] = values
		}
	}
	return out
}

func isUpgradeRequest(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("Connection")), "upgrade") ||
		strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// bufferedResponseWriter captures status/headers/body so response hooks can
// modify them before the real writer receives the final bytes.
//
// Streaming responses (SSE / MJPEG / chunked feeds) must not be buffered:
// their handlers never return, so buffering would hold the client empty
// forever and grow memory without bound. The first Flush() switches the
// writer to passthrough mode: buffered status/headers/body are forwarded to
// the real writer immediately, every later Write/WriteHeader goes straight
// through, and the response hooks can no longer rewrite the stream.
type bufferedResponseWriter struct {
	mu          sync.Mutex
	header      http.Header
	statusCode  int
	body        bytes.Buffer
	wroteHeader bool
	streaming   bool // switched to passthrough after the first Flush()
	forwarded   bool // status/headers already sent to the underlying writer
	hijacked    bool
	limit       int  // maximum buffered body bytes before passthrough
	passthrough bool // buffering abandoned because the body exceeded limit
	underlying  http.ResponseWriter
}

func newBufferedResponseWriter(w http.ResponseWriter) *bufferedResponseWriter {
	return &bufferedResponseWriter{header: make(http.Header), limit: maxHookBufferBytes, underlying: w}
}

func (b *bufferedResponseWriter) passedThrough() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.passthrough
}

func (b *bufferedResponseWriter) Header() http.Header { return b.header }

func (b *bufferedResponseWriter) WriteHeader(code int) {
	b.mu.Lock()
	if b.streaming {
		if b.forwarded || b.hijacked {
			b.mu.Unlock()
			return
		}
		b.forwarded = true
		status, header := code, b.header.Clone()
		b.mu.Unlock()
		b.writeThrough(status, header)
		return
	}
	if !b.wroteHeader {
		b.statusCode = code
		b.wroteHeader = true
	}
	b.mu.Unlock()
}

func (b *bufferedResponseWriter) Write(p []byte) (int, error) {
	b.mu.Lock()
	if b.streaming {
		b.mu.Unlock()
		return b.underlying.Write(p)
	}
	if !b.wroteHeader {
		b.statusCode = http.StatusOK
		b.wroteHeader = true
	}
	if b.body.Len()+len(p) > b.limit {
		// Response is too large to rewrite: switch to passthrough mode and
		// forward status/headers and the buffered body to the real writer
		// immediately. Response hooks can no longer modify this response.
		status, header, buffered := b.statusCode, b.header.Clone(), b.body.Bytes()
		b.streaming = true
		b.forwarded = true
		b.passthrough = true
		b.mu.Unlock()
		b.writeThrough(status, header)
		_, _ = b.underlying.Write(buffered)
		_, _ = b.underlying.Write(p)
		return len(p), nil
	}
	_, err := b.body.Write(p)
	b.mu.Unlock()
	return len(p), err
}

func (b *bufferedResponseWriter) status() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.wroteHeader {
		return http.StatusOK
	}
	return b.statusCode
}

func (b *bufferedResponseWriter) bodyBytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.body.Bytes()...)
}

func (b *bufferedResponseWriter) apply(status int, header http.Header, body []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.streaming {
		return // already streamed to the client; hooks cannot rewrite it
	}
	bodyChanged := !bytes.Equal(body, b.body.Bytes())
	b.statusCode = status
	b.wroteHeader = true
	b.header = header
	b.body.Reset()
	_, _ = b.body.Write(body)
	if bodyChanged {
		// The rewritten body differs from the original response; a
		// Content-Length copied from it is now stale and would truncate or
		// hang the client. Drop it so Go recomputes the length (or falls
		// back to chunked encoding).
		b.header.Del("Content-Length")
	}
}

func (b *bufferedResponseWriter) flushTo(w http.ResponseWriter) {
	b.mu.Lock()
	if b.streaming || b.hijacked {
		b.mu.Unlock()
		return // passthrough already forwarded everything
	}
	status, body, header := b.statusCode, append([]byte(nil), b.body.Bytes()...), b.header.Clone()
	b.mu.Unlock()
	for name, values := range header {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func (b *bufferedResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	b.mu.Lock()
	b.hijacked = true
	b.mu.Unlock()
	hijacker, ok := b.underlying.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("plugin response buffer does not support hijacking")
	}
	return hijacker.Hijack()
}

func (b *bufferedResponseWriter) Flush() {
	b.mu.Lock()
	if !b.streaming {
		// First Flush: switch to passthrough mode and forward the buffered
		// response to the real writer immediately.
		b.streaming = true
		status, body, header := b.statusCode, append([]byte(nil), b.body.Bytes()...), b.header.Clone()
		b.mu.Unlock()
		b.writeThrough(status, header)
		_, _ = b.underlying.Write(body)
	} else {
		b.mu.Unlock()
	}
	if flusher, ok := b.underlying.(http.Flusher); ok {
		flusher.Flush()
	}
}

// writeThrough forwards status and headers to the underlying writer once.
func (b *bufferedResponseWriter) writeThrough(status int, header http.Header) {
	for name, values := range header {
		for _, value := range values {
			b.underlying.Header().Add(name, value)
		}
	}
	b.underlying.WriteHeader(status)
}

func (b *bufferedResponseWriter) ReadFrom(r io.Reader) (int64, error) {
	if !b.wroteHeader {
		b.WriteHeader(http.StatusOK)
	}
	return io.Copy(b, r)
}

func (b *bufferedResponseWriter) CloseNotify() <-chan bool {
	if notifier, ok := b.underlying.(interface{ CloseNotify() <-chan bool }); ok {
		return notifier.CloseNotify()
	}
	return nil
}

var (
	_ http.ResponseWriter = (*bufferedResponseWriter)(nil)
	_ http.Hijacker       = (*bufferedResponseWriter)(nil)
	_ http.Flusher        = (*bufferedResponseWriter)(nil)
)
