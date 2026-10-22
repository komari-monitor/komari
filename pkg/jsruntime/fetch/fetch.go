package fetch

import (
	"github.com/Calcium-Ion/moejs/engine"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
)
import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/Calcium-Ion/moejs"
	"github.com/komari-monitor/komari/pkg/jsruntime/httpbody"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/bridge"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

type abortSignalState struct {
	aborted  bool
	requests map[int64]context.CancelFunc
}
type nativeRequest struct {
	method   string
	url      string
	headers  [][2]string
	body     []byte
	signalID int64
	redirect string
}
type httpResult struct {
	statusCode int
	statusText string
	headers    http.Header
	body       []byte
	url        string
	redirected bool
}
type formEntry struct {
	Name     string
	Value    string
	Bytes    []byte
	Filename string
	Type     string
	File     bool
}
type Module struct {
	runtime      *bridge.Runtime
	vm           *moejs.Runtime
	client       *http.Client
	maxBodyBytes int64
	abortMu      sync.Mutex
	abortIDs     int64
	abortState   map[int64]*abortSignalState
}

func New(runtime *bridge.Runtime, client *http.Client, maxBodyBytes int64) *Module {
	return &Module{runtime: runtime, client: client, maxBodyBytes: maxBodyBytes, abortState: make(map[int64]*abortSignalState)}
}
func (m *Module) Inject(vm *moejs.Runtime) error {
	m.vm = vm
	if err := vm.SetGlobal("__komariBodyBuffer", native.Bind(vm, "__komariBodyBuffer", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return m.bodyBuffer(args), nil
	})); err != nil {
		return err
	}
	if err := vm.SetGlobal("__komariBodyText", native.Bind(vm, "__komariBodyText", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return m.bodyText(args), nil
	})); err != nil {
		return err
	}
	if err := vm.SetGlobal("__komariEncodeFormData", native.Bind(vm, "__komariEncodeFormData", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return m.encodeFormData(args), nil
	})); err != nil {
		return err
	}
	if err := vm.SetGlobal("__komariParseFormData", native.Bind(vm, "__komariParseFormData", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return m.parseFormData(args), nil
	})); err != nil {
		return err
	}
	if err := vm.SetGlobal("__komariNewAbortSignal", native.Bind(vm, "__komariNewAbortSignal", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return m.newAbortSignal(args), nil
	})); err != nil {
		return err
	}
	if err := vm.SetGlobal("__komariAbortSignal", native.Bind(vm, "__komariAbortSignal", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return m.abortSignal(args), nil
	})); err != nil {
		return err
	}
	if err := vm.SetGlobal("__komariFetch", native.Bind(vm, "__komariFetch", 1, m.createFetchFunction(false))); err != nil {
		return err
	}
	if err := vm.SetGlobal("__komariFetchSync", native.Bind(vm, "__komariFetchSync", 1, m.createFetchFunction(true))); err != nil {
		return err
	}
	if _, err := native.Run(vm, fetchAPISource); err != nil {
		return fmt.Errorf("inject Fetch API: %w", err)
	}
	return nil
}
func (m *Module) createFetchFunction(syncRequest bool) moejs.NativeFunc {
	vm := m.vm
	return func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		request, err := m.exportNativeRequest(moejs.Arg(args, 0))
		if err != nil {
			panic(native.TypeError(m.vm, "%s", err))
		}
		ctx, releaseSignal := m.requestContext(request.signalID)
		ctx, cancelTimeout := context.WithTimeout(ctx, m.runtime.Timeout())
		release := func() {
			cancelTimeout()
			releaseSignal()
		}
		if syncRequest {
			defer release()
			result, requestErr := m.doHTTPRequest(ctx, request)
			if requestErr != nil {
				panic(webAPIError(m.vm, "TypeError", fmt.Sprintf("fetch failed: %v", requestErr)))
			}
			return native.From(vm, m.httpResultValue(m.vm, result)), nil
		}
		promise, resolve, reject := m.vm.NewPromise()
		go func() {
			result, requestErr := m.doHTTPRequest(ctx, request)
			if !m.runtime.RunOnLoop(func(vm *moejs.Runtime) {
				defer release()
				m.runtime.RunJob(vm, "fetch", func() error {
					if ctx.Err() != nil {
						return reject(webAPIError(vm, "AbortError", fmt.Sprintf("fetch failed: %v", ctx.Err())))
					}
					if requestErr != nil {
						name := "TypeError"
						if errors.Is(requestErr, context.Canceled) {
							name = "AbortError"
						}
						return reject(webAPIError(vm, name, fmt.Sprintf("fetch failed: %v", requestErr)))
					}
					return resolve(m.httpResultValue(vm, result))
				})
			}) {
				release()
			}
		}()
		return native.From(vm, native.From(m.vm, promise)), nil
	}
}
func (m *Module) exportNativeRequest(value moejs.Value) (nativeRequest, error) {
	vm := m.vm
	if native.IsNil(value) || value.IsUndefined() || value.IsNull() {
		return nativeRequest{}, errors.New("missing request data")
	}
	object := native.Object(m.vm, value)
	request := nativeRequest{method: native.String(vm, native.Get(vm, object, "method")), url: native.String(vm, native.Get(vm, object, "url")), signalID: native.Int(vm, native.Get(vm, object, "signalId")), redirect: native.String(vm, native.Get(vm, object, "redirect"))}
	if request.method == "" {
		request.method = http.MethodGet
	}
	if request.redirect == "" {
		request.redirect = "follow"
	}
	if value := native.Get(vm, object, "headers"); !native.IsNil(value) && !value.IsUndefined() {
		var headers [][]string
		if err := native.ExportTo(m.vm, value, &headers); err != nil {
			return nativeRequest{}, fmt.Errorf("invalid request headers: %w", err)
		}
		for _, header := range headers {
			if len(header) != 2 {
				return nativeRequest{}, errors.New("invalid request header pair")
			}
			request.headers = append(request.headers, [2]string{header[0], header[1]})
		}
	}
	if value := native.Get(vm, object, "body"); !native.IsNil(value) && !value.IsUndefined() && !value.IsNull() {
		body, err := exportBytes(m.vm, value)
		if err != nil {
			return nativeRequest{}, fmt.Errorf("invalid request body: %w", err)
		}
		request.body = body
	}
	return request, nil
}
func (m *Module) doHTTPRequest(ctx context.Context, request nativeRequest) (httpResult, error) {
	httpRequest, err := http.NewRequestWithContext(ctx, request.method, request.url, bytes.NewReader(request.body))
	if err != nil {
		return httpResult{}, fmt.Errorf("create request: %w", err)
	}
	for _, header := range request.headers {
		if strings.EqualFold(header[0], "Host") {
			httpRequest.Host = header[1]
			continue
		}
		httpRequest.Header.Add(header[0], header[1])
	}
	client := *m.client
	switch request.redirect {
	case "manual":
		client.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
	case "error":
		client.CheckRedirect = func(*http.Request, []*http.Request) error {
			return errors.New("redirect mode is set to error")
		}
	case "follow":
	default:
		return httpResult{}, fmt.Errorf("unsupported redirect mode %q", request.redirect)
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return httpResult{}, err
	}
	defer response.Body.Close()
	body, err := httpbody.ReadAll(response.Body, m.maxBodyBytes)
	if err != nil {
		return httpResult{}, fmt.Errorf("read response: %w", err)
	}
	responseURL := request.url
	if response.Request != nil && response.Request.URL != nil {
		responseURL = response.Request.URL.String()
	}
	statusText := strings.TrimSpace(strings.TrimPrefix(response.Status, strconv.Itoa(response.StatusCode)))
	return httpResult{statusCode: response.StatusCode, statusText: statusText, headers: response.Header.Clone(), body: body, url: responseURL, redirected: responseURL != request.url}, nil
}
func (m *Module) httpResultValue(vm *moejs.Runtime, result httpResult) moejs.Value {
	object := vm.Realm().NewObject()
	_ = native.Set(vm, object, "status", result.statusCode)
	_ = native.Set(vm, object, "statusText", result.statusText)
	_ = native.Set(vm, object, "url", result.url)
	_ = native.Set(vm, object, "redirected", result.redirected)
	_ = native.Set(vm, object, "body", native.From(vm, append([]byte(nil), result.body...)))
	headers := make([][2]string, 0, len(result.headers))
	for name, values := range result.headers {
		for _, value := range values {
			headers = append(headers, [2]string{name, value})
		}
	}
	_ = native.Set(vm, object, "headers", headers)
	return engine.ObjectValue(object)
}
func (m *Module) bodyBuffer(args []moejs.Value) moejs.Value {
	data, err := exportBytes(m.vm, moejs.Arg(args, 0))
	if err != nil {
		panic(native.TypeError(m.vm, "unsupported body value: %v", err))
	}
	return native.From(m.vm, native.From(m.vm, data))
}
func (m *Module) bodyText(args []moejs.Value) moejs.Value {
	data, err := exportBytes(m.vm, moejs.Arg(args, 0))
	if err != nil {
		panic(native.TypeError(m.vm, "invalid body buffer: %v", err))
	}
	return native.From(m.vm, strings.ToValidUTF8(string(data), "\uFFFD"))
}
func exportBytes(vm *moejs.Runtime, value moejs.Value) ([]byte, error) {
	if native.IsNil(value) {
		return nil, nil
	}
	if value.IsString() {
		return []byte(native.String(vm, value)), nil
	}
	var data []byte
	if err := native.ExportTo(vm, value, &data); err != nil {
		return nil, err
	}
	return append([]byte(nil), data...), nil
}
func (m *Module) newAbortSignal(args []moejs.Value) moejs.Value {
	m.abortMu.Lock()
	defer m.abortMu.Unlock()
	m.abortIDs++
	return native.From(m.vm, m.abortIDs)
}
func (m *Module) abortSignal(args []moejs.Value) moejs.Value {
	vm := m.vm
	id := native.Int(vm, moejs.Arg(args, 0))
	m.abortMu.Lock()
	state := m.abortState[id]
	if state == nil {
		state = &abortSignalState{requests: make(map[int64]context.CancelFunc)}
		m.abortState[id] = state
	}
	state.aborted = true
	for _, cancel := range state.requests {
		cancel()
	}
	m.abortMu.Unlock()
	return moejs.Undefined()
}
func (m *Module) requestContext(signalID int64) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	if signalID <= 0 {
		return ctx, cancel
	}
	m.abortMu.Lock()
	state := m.abortState[signalID]
	if state != nil && state.aborted {
		m.abortMu.Unlock()
		cancel()
		return ctx, cancel
	}
	if state == nil {
		state = &abortSignalState{requests: make(map[int64]context.CancelFunc)}
		m.abortState[signalID] = state
	}
	m.abortIDs++
	requestID := m.abortIDs
	state.requests[requestID] = cancel
	m.abortMu.Unlock()
	return ctx, func() {
		m.abortMu.Lock()
		if current := m.abortState[signalID]; current != nil {
			delete(current.requests, requestID)
			if !current.aborted && len(current.requests) == 0 {
				delete(m.abortState, signalID)
			}
		}
		m.abortMu.Unlock()
		cancel()
	}
}
func (m *Module) Close() {
	m.abortMu.Lock()
	defer m.abortMu.Unlock()
	for _, state := range m.abortState {
		state.aborted = true
		for _, cancel := range state.requests {
			cancel()
		}
	}
}
func webAPIError(vm *moejs.Runtime, name, message string) moejs.Value {
	object := native.GoError(vm, errors.New(message))
	_ = native.Set(vm, native.Object(vm, object), "name", name)
	return object
}
func (m *Module) encodeFormData(args []moejs.Value) moejs.Value {
	vm := m.vm
	var entries []formEntry
	array := native.Object(m.vm, moejs.Arg(args, 0))
	length := int(native.Int(vm, native.Get(vm, array, "length")))
	for index := 0; index < length; index++ {
		entryObject := native.Object(m.vm, native.Get(vm, array, strconv.Itoa(index)))
		entry := formEntry{Name: native.String(vm, native.Get(vm, entryObject, "name"))}
		value := native.Get(vm, entryObject, "value")
		if native.Bool(native.Get(vm, entryObject, "file")) {
			entry.File = true
			entry.Filename = native.String(vm, native.Get(vm, entryObject, "filename"))
			entry.Type = native.String(vm, native.Get(vm, entryObject, "type"))
			data, err := exportBytes(m.vm, value)
			if err != nil {
				panic(native.TypeError(m.vm, "invalid FormData file: %v", err))
			}
			entry.Bytes = data
		} else {
			entry.Value = native.String(vm, value)
		}
		entries = append(entries, entry)
	}
	var output bytes.Buffer
	writer := multipart.NewWriter(&output)
	for _, entry := range entries {
		if !entry.File {
			_ = writer.WriteField(entry.Name, entry.Value)
			continue
		}
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, entry.Name, entry.Filename))
		if entry.Type != "" {
			header.Set("Content-Type", entry.Type)
		}
		part, err := writer.CreatePart(header)
		if err != nil {
			panic(native.GoError(m.vm, err))
		}
		_, _ = part.Write(entry.Bytes)
	}
	if err := writer.Close(); err != nil {
		panic(native.GoError(m.vm, err))
	}
	result := m.vm.Realm().NewObject()
	_ = native.Set(vm, result, "body", native.From(m.vm, output.Bytes()))
	_ = native.Set(vm, result, "contentType", writer.FormDataContentType())
	return native.From(vm, result)
}
func (m *Module) parseFormData(args []moejs.Value) moejs.Value {
	vm := m.vm
	data, err := exportBytes(m.vm, moejs.Arg(args, 0))
	if err != nil {
		panic(native.TypeError(m.vm, "invalid FormData body: %v", err))
	}
	mediaType, parameters, err := mime.ParseMediaType(native.String(vm, moejs.Arg(args, 1)))
	if err != nil {
		panic(native.TypeError(m.vm, "invalid form content type: %v", err))
	}
	entries := make([]map[string]any, 0)
	switch mediaType {
	case "application/x-www-form-urlencoded":
		for _, field := range strings.Split(string(data), "&") {
			if field == "" {
				continue
			}
			nameValue := strings.SplitN(field, "=", 2)
			name, parseErr := url.QueryUnescape(nameValue[0])
			if parseErr != nil {
				panic(native.TypeError(m.vm, "invalid form field name: %v", parseErr))
			}
			value := ""
			if len(nameValue) == 2 {
				value, parseErr = url.QueryUnescape(nameValue[1])
				if parseErr != nil {
					panic(native.TypeError(m.vm, "invalid form field value: %v", parseErr))
				}
			}
			entries = append(entries, map[string]any{"name": name, "value": value})
		}
	case "multipart/form-data":
		boundary := parameters["boundary"]
		if boundary == "" {
			panic(native.TypeError(m.vm, "multipart form data has no boundary"))
		}
		reader := multipart.NewReader(bytes.NewReader(data), boundary)
		for {
			part, partErr := reader.NextPart()
			if errors.Is(partErr, io.EOF) {
				break
			}
			if partErr != nil {
				panic(native.TypeError(m.vm, "invalid multipart body: %v", partErr))
			}
			partBytes, readErr := io.ReadAll(part)
			if readErr != nil {
				panic(native.TypeError(m.vm, "read multipart body: %v", readErr))
			}
			entry := map[string]any{"name": part.FormName()}
			if part.FileName() == "" {
				entry["value"] = strings.ToValidUTF8(string(partBytes), "\uFFFD")
			} else {
				entry["file"] = true
				entry["filename"] = part.FileName()
				entry["type"] = part.Header.Get("Content-Type")
				entry["value"] = native.From(m.vm, partBytes)
			}
			entries = append(entries, entry)
		}
	default:
		panic(native.TypeError(m.vm, "unsupported form content type %q", mediaType))
	}
	return native.From(m.vm, entries)
}
func callable(v moejs.Value) (moejs.Value, bool) { return v, engine.IsCallable(v) }
func asObject(v moejs.Value) (*engine.Object, bool) {
	if !v.IsObject() {
		return nil, false
	}
	return v.AsObject(), true
}
