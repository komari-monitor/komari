package plugin

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
)

// injectEntry is one plugin's registered HTML fragments. head is inserted
// before </head>, body before </body>, in every text/html response.
type injectEntry struct {
	short string
	head  string
	body  string
}

// htmlInjectHandler installs the HTML injection wrapper around the
// application handler. WebSocket upgrades pass through untouched because the
// response cannot be buffered.
func (m *Manager) htmlInjectHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isUpgradeRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		fragments := m.injectsSnapshot()
		if len(fragments) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		iw := newHTMLInjectWriter(w)
		next.ServeHTTP(iw, r)
		iw.flushTo(fragments)
	})
}

// injectHTMLContent inserts the plugin head/body fragments into an HTML
// document: head before </head>, body before </body> with fallbacks to
// </html> and the document end. Tag matching is case-insensitive.
func injectHTMLContent(body []byte, fragments []*injectEntry) []byte {
	if len(fragments) == 0 {
		return body
	}
	var head, tail strings.Builder
	for _, fragment := range fragments {
		head.WriteString(fragment.head)
		tail.WriteString(fragment.body)
	}
	if head.Len() == 0 && tail.Len() == 0 {
		return body
	}
	text := string(body)
	headHTML, tailHTML := head.String(), tail.String()
	if headHTML != "" {
		if idx := indexFold(text, "</head>"); idx >= 0 {
			text = text[:idx] + headHTML + text[idx:]
		} else {
			text = headHTML + text
		}
	}
	if tailHTML != "" {
		switch {
		case indexFold(text, "</body>") >= 0:
			text = text[:indexFold(text, "</body>")] + tailHTML + text[indexFold(text, "</body>"):]
		case indexFold(text, "</html>") >= 0:
			text = text[:indexFold(text, "</html>")] + tailHTML + text[indexFold(text, "</html>"):]
		default:
			text += tailHTML
		}
	}
	return []byte(text)
}

func indexFold(text, needle string) int {
	return strings.Index(strings.ToLower(text), needle)
}

// htmlInjectWriter captures only text/html responses so the registered
// plugin fragments can be injected before flushTo. Every other response is
// forwarded to the real writer unmodified as soon as its content type is
// known, so JSON, assets and streams are never buffered.
//
// The content type is read from the response header when set; otherwise the
// first bytes are sniffed with http.DetectContentType. Streaming responses
// (SSE / MJPEG / chunked feeds) switch to passthrough on the first Flush(),
// and responses larger than the buffer limit are forwarded unmodified.
type htmlInjectWriter struct {
	mu          sync.Mutex
	header      http.Header
	statusCode  int
	wroteHeader bool
	body        bytes.Buffer
	html        bool // decided: the response is text/html and buffered
	streaming   bool // switched to passthrough
	hijacked    bool
	limit       int
	underlying  http.ResponseWriter
}

func newHTMLInjectWriter(w http.ResponseWriter) *htmlInjectWriter {
	return &htmlInjectWriter{header: make(http.Header), limit: maxHTMLInjectBufferBytes, underlying: w}
}

func (w *htmlInjectWriter) Header() http.Header { return w.header }

func (w *htmlInjectWriter) WriteHeader(code int) {
	w.mu.Lock()
	if w.streaming {
		w.mu.Unlock()
		return
	}
	if !w.wroteHeader {
		w.statusCode = code
		w.wroteHeader = true
	}
	ct := w.header.Get("Content-Type")
	w.mu.Unlock()
	if ct != "" && !isHTMLContentType(ct) {
		w.passthrough()
	}
}

func (w *htmlInjectWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	if w.streaming {
		w.mu.Unlock()
		return w.underlying.Write(p)
	}
	if !w.wroteHeader {
		w.statusCode = http.StatusOK
		w.wroteHeader = true
	}
	if !w.html {
		ct := w.header.Get("Content-Type")
		if ct == "" {
			ct = sniffContentType(w.body.Bytes(), p)
			if ct != "" {
				w.header.Set("Content-Type", ct)
			}
		}
		if ct != "" {
			if !isHTMLContentType(ct) {
				w.mu.Unlock()
				w.passthrough()
				return w.underlying.Write(p)
			}
			w.html = true
		}
	}
	if w.body.Len()+len(p) > w.limit {
		w.mu.Unlock()
		w.passthrough()
		return w.underlying.Write(p)
	}
	n, err := w.body.Write(p)
	w.mu.Unlock()
	return n, err
}

// passthrough forwards the buffered status/headers/body to the real writer
// and switches every later write to direct passthrough. The injection is
// skipped for the response.
func (w *htmlInjectWriter) passthrough() {
	w.mu.Lock()
	if w.streaming || w.hijacked {
		w.mu.Unlock()
		return
	}
	w.streaming = true
	status, header, body := w.statusCode, w.header.Clone(), append([]byte(nil), w.body.Bytes()...)
	w.mu.Unlock()
	w.writeThrough(status, header)
	_, _ = w.underlying.Write(body)
}

// flushTo finalizes a fully buffered response. HTML responses get the
// registered fragments injected, everything else is written through
// unmodified.
func (w *htmlInjectWriter) flushTo(fragments []*injectEntry) {
	w.mu.Lock()
	if w.streaming || w.hijacked {
		w.mu.Unlock()
		return
	}
	w.streaming = true
	status, header, body := w.statusCode, w.header.Clone(), append([]byte(nil), w.body.Bytes()...)
	w.mu.Unlock()

	ct := header.Get("Content-Type")
	if ct == "" {
		ct = http.DetectContentType(body)
		if isHTMLContentType(ct) {
			header.Set("Content-Type", ct)
		}
	}
	if isHTMLContentType(ct) {
		body = injectHTMLContent(body, fragments)
		header.Del("Content-Length")
	}
	w.writeThrough(status, header)
	_, _ = w.underlying.Write(body)
}

func (w *htmlInjectWriter) writeThrough(status int, header http.Header) {
	for name, values := range header {
		for _, value := range values {
			w.underlying.Header().Add(name, value)
		}
	}
	w.underlying.WriteHeader(status)
}

// isHTMLContentType reports whether the MIME type is HTML, with or without
// parameters such as charset.
func isHTMLContentType(ct string) bool {
	return strings.Contains(strings.ToLower(ct), "text/html")
}

// sniffContentType decides the content type of a response from the header
// value when available, otherwise from the first bytes via
// http.DetectContentType. An empty result means the type is not decidable
// yet; buffering continues.
func sniffContentType(buffered, next []byte) string {
	const sniffLength = 512
	total := buffered
	if len(total) < sniffLength {
		room := sniffLength - len(total)
		if len(next) > room {
			next = next[:room]
		}
		total = append(append([]byte(nil), total...), next...)
	}
	return http.DetectContentType(total)
}

func (w *htmlInjectWriter) ReadFrom(r io.Reader) (int64, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return io.Copy(w, r)
}

func (w *htmlInjectWriter) Flush() {
	w.passthrough()
	if flusher, ok := w.underlying.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *htmlInjectWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.mu.Lock()
	w.hijacked = true
	w.mu.Unlock()
	hijacker, ok := w.underlying.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("plugin HTML injector does not support hijacking")
	}
	return hijacker.Hijack()
}

func (w *htmlInjectWriter) CloseNotify() <-chan bool {
	if notifier, ok := w.underlying.(interface{ CloseNotify() <-chan bool }); ok {
		return notifier.CloseNotify()
	}
	return nil
}

var (
	_ http.ResponseWriter = (*htmlInjectWriter)(nil)
	_ http.Hijacker       = (*htmlInjectWriter)(nil)
	_ http.Flusher        = (*htmlInjectWriter)(nil)
)
