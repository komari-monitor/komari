package fetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/dop251/goja"
	"github.com/komari-monitor/komari/pkg/jsruntime/httpbody"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/bridge"
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
	vm           *goja.Runtime
	client       *http.Client
	maxBodyBytes int64
	abortMu      sync.Mutex
	abortIDs     int64
	abortState   map[int64]*abortSignalState
}

func New(runtime *bridge.Runtime, client *http.Client, maxBodyBytes int64) *Module {
	return &Module{
		runtime:      runtime,
		client:       client,
		maxBodyBytes: maxBodyBytes,
		abortState:   make(map[int64]*abortSignalState),
	}
}

func (m *Module) Inject(vm *goja.Runtime) error {
	m.vm = vm
	if err := vm.Set("__komariBodyBuffer", m.bodyBuffer); err != nil {
		return err
	}
	if err := vm.Set("__komariBodyText", m.bodyText); err != nil {
		return err
	}
	if err := vm.Set("__komariEncodeFormData", m.encodeFormData); err != nil {
		return err
	}
	if err := vm.Set("__komariParseFormData", m.parseFormData); err != nil {
		return err
	}
	if err := vm.Set("__komariNewAbortSignal", m.newAbortSignal); err != nil {
		return err
	}
	if err := vm.Set("__komariAbortSignal", m.abortSignal); err != nil {
		return err
	}
	if err := vm.Set("__komariFetch", m.createFetchFunction(false)); err != nil {
		return err
	}
	if err := vm.Set("__komariFetchSync", m.createFetchFunction(true)); err != nil {
		return err
	}
	if _, err := vm.RunString(fetchAPISource); err != nil {
		return fmt.Errorf("inject Fetch API: %w", err)
	}
	return nil
}

func (m *Module) createFetchFunction(syncRequest bool) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		request, err := m.exportNativeRequest(call.Argument(0))
		if err != nil {
			panic(m.vm.NewTypeError("%s", err))
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
			return m.httpResultValue(m.vm, result)
		}

		promise, resolve, reject := m.vm.NewPromise()
		go func() {
			result, requestErr := m.doHTTPRequest(ctx, request)
			if !m.runtime.RunOnLoop(func(vm *goja.Runtime) {
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
		return m.vm.ToValue(promise)
	}
}

func (m *Module) exportNativeRequest(value goja.Value) (nativeRequest, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nativeRequest{}, errors.New("missing request data")
	}
	object := value.ToObject(m.vm)
	request := nativeRequest{
		method:   object.Get("method").String(),
		url:      object.Get("url").String(),
		signalID: object.Get("signalId").ToInteger(),
		redirect: object.Get("redirect").String(),
	}
	if request.method == "" {
		request.method = http.MethodGet
	}
	if request.redirect == "" {
		request.redirect = "follow"
	}
	if value := object.Get("headers"); value != nil && !goja.IsUndefined(value) {
		var headers [][]string
		if err := m.vm.ExportTo(value, &headers); err != nil {
			return nativeRequest{}, fmt.Errorf("invalid request headers: %w", err)
		}
		for _, header := range headers {
			if len(header) != 2 {
				return nativeRequest{}, errors.New("invalid request header pair")
			}
			request.headers = append(request.headers, [2]string{header[0], header[1]})
		}
	}
	if value := object.Get("body"); value != nil && !goja.IsUndefined(value) && !goja.IsNull(value) {
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
	return httpResult{
		statusCode: response.StatusCode,
		statusText: statusText,
		headers:    response.Header.Clone(),
		body:       body,
		url:        responseURL,
		redirected: responseURL != request.url,
	}, nil
}

func (m *Module) httpResultValue(vm *goja.Runtime, result httpResult) goja.Value {
	object := vm.NewObject()
	_ = object.Set("status", result.statusCode)
	_ = object.Set("statusText", result.statusText)
	_ = object.Set("url", result.url)
	_ = object.Set("redirected", result.redirected)
	_ = object.Set("body", vm.NewArrayBuffer(append([]byte(nil), result.body...)))
	headers := make([][2]string, 0, len(result.headers))
	for name, values := range result.headers {
		for _, value := range values {
			headers = append(headers, [2]string{name, value})
		}
	}
	_ = object.Set("headers", headers)
	return object
}

func (m *Module) bodyBuffer(call goja.FunctionCall) goja.Value {
	data, err := exportBytes(m.vm, call.Argument(0))
	if err != nil {
		panic(m.vm.NewTypeError("unsupported body value: %v", err))
	}
	return m.vm.ToValue(m.vm.NewArrayBuffer(data))
}

func (m *Module) bodyText(call goja.FunctionCall) goja.Value {
	data, err := exportBytes(m.vm, call.Argument(0))
	if err != nil {
		panic(m.vm.NewTypeError("invalid body buffer: %v", err))
	}
	return m.vm.ToValue(strings.ToValidUTF8(string(data), "\uFFFD"))
}

func exportBytes(vm *goja.Runtime, value goja.Value) ([]byte, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil, nil
	}
	if stringValue, ok := value.Export().(string); ok {
		return []byte(stringValue), nil
	}
	if buffer, ok := value.Export().(goja.ArrayBuffer); ok {
		return append([]byte(nil), buffer.Bytes()...), nil
	}
	var bytesValue []byte
	if err := vm.ExportTo(value, &bytesValue); err != nil {
		return nil, err
	}
	return append([]byte(nil), bytesValue...), nil
}

func (m *Module) newAbortSignal(goja.FunctionCall) goja.Value {
	m.abortMu.Lock()
	defer m.abortMu.Unlock()
	m.abortIDs++
	return m.vm.ToValue(m.abortIDs)
}

func (m *Module) abortSignal(call goja.FunctionCall) goja.Value {
	id := call.Argument(0).ToInteger()
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
	return goja.Undefined()
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

func webAPIError(vm *goja.Runtime, name, message string) *goja.Object {
	object := vm.NewGoError(errors.New(message))
	_ = object.Set("name", name)
	return object
}

func (m *Module) encodeFormData(call goja.FunctionCall) goja.Value {
	var entries []formEntry
	array := call.Argument(0).ToObject(m.vm)
	length := int(array.Get("length").ToInteger())
	for index := 0; index < length; index++ {
		entryObject := array.Get(strconv.Itoa(index)).ToObject(m.vm)
		entry := formEntry{Name: entryObject.Get("name").String()}
		value := entryObject.Get("value")
		if entryObject.Get("file").ToBoolean() {
			entry.File = true
			entry.Filename = entryObject.Get("filename").String()
			entry.Type = entryObject.Get("type").String()
			data, err := exportBytes(m.vm, value)
			if err != nil {
				panic(m.vm.NewTypeError("invalid FormData file: %v", err))
			}
			entry.Bytes = data
		} else {
			entry.Value = value.String()
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
			panic(m.vm.NewGoError(err))
		}
		_, _ = part.Write(entry.Bytes)
	}
	if err := writer.Close(); err != nil {
		panic(m.vm.NewGoError(err))
	}
	result := m.vm.NewObject()
	_ = result.Set("body", m.vm.NewArrayBuffer(output.Bytes()))
	_ = result.Set("contentType", writer.FormDataContentType())
	return result
}

func (m *Module) parseFormData(call goja.FunctionCall) goja.Value {
	data, err := exportBytes(m.vm, call.Argument(0))
	if err != nil {
		panic(m.vm.NewTypeError("invalid FormData body: %v", err))
	}
	mediaType, parameters, err := mime.ParseMediaType(call.Argument(1).String())
	if err != nil {
		panic(m.vm.NewTypeError("invalid form content type: %v", err))
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
				panic(m.vm.NewTypeError("invalid form field name: %v", parseErr))
			}
			value := ""
			if len(nameValue) == 2 {
				value, parseErr = url.QueryUnescape(nameValue[1])
				if parseErr != nil {
					panic(m.vm.NewTypeError("invalid form field value: %v", parseErr))
				}
			}
			entries = append(entries, map[string]any{"name": name, "value": value})
		}
	case "multipart/form-data":
		boundary := parameters["boundary"]
		if boundary == "" {
			panic(m.vm.NewTypeError("multipart form data has no boundary"))
		}
		reader := multipart.NewReader(bytes.NewReader(data), boundary)
		for {
			part, partErr := reader.NextPart()
			if errors.Is(partErr, io.EOF) {
				break
			}
			if partErr != nil {
				panic(m.vm.NewTypeError("invalid multipart body: %v", partErr))
			}
			partBytes, readErr := io.ReadAll(part)
			if readErr != nil {
				panic(m.vm.NewTypeError("read multipart body: %v", readErr))
			}
			entry := map[string]any{"name": part.FormName()}
			if part.FileName() == "" {
				entry["value"] = strings.ToValidUTF8(string(partBytes), "\uFFFD")
			} else {
				entry["file"] = true
				entry["filename"] = part.FileName()
				entry["type"] = part.Header.Get("Content-Type")
				entry["value"] = m.vm.NewArrayBuffer(partBytes)
			}
			entries = append(entries, entry)
		}
	default:
		panic(m.vm.NewTypeError("unsupported form content type %q", mediaType))
	}
	return m.vm.ToValue(entries)
}
