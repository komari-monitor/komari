package jsruntime

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/eventloop"
)

type xhrConfig struct {
	client *http.Client
}

func injectXHR(vm *goja.Runtime, loop *eventloop.EventLoop, client *http.Client) error {
	if vm == nil {
		return errors.New("vm is nil")
	}
	if loop == nil {
		return errors.New("event loop is nil")
	}
	if client == nil {
		return errors.New("http client is nil")
	}

	cfg := xhrConfig{client: client}
	return vm.Set("XMLHttpRequest", newXHRConstructor(vm, loop, cfg))
}

func newXHRConstructor(vm *goja.Runtime, loop *eventloop.EventLoop, cfg xhrConfig) func(goja.ConstructorCall) *goja.Object {
	return func(call goja.ConstructorCall) *goja.Object {
		xhr := call.This

		// 内部状态
		var method, rawURL string
		headers := make(map[string]string)
		var requestBody []byte
		async := true
		var responseHeaders http.Header

		// readyState: 0 UNSENT
		xhr.Set("readyState", 0)
		xhr.Set("status", 0)
		xhr.Set("statusText", "")
		xhr.Set("responseText", "")
		xhr.Set("response", "")

		// 事件处理器
		xhr.Set("onreadystatechange", goja.Null())
		xhr.Set("onload", goja.Null())
		xhr.Set("onerror", goja.Null())

		// open(method, url, async=true)
		xhr.Set("open", func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) < 2 {
				panic(vm.NewTypeError("open requires at least 2 arguments"))
			}
			method = strings.ToUpper(strings.TrimSpace(call.Argument(0).String()))
			rawURL = strings.TrimSpace(call.Argument(1).String())
			if len(call.Arguments) > 2 {
				async = call.Argument(2).ToBoolean()
			} else {
				async = true
			}
			// 1 OPENED
			xhr.Set("readyState", 1)
			xhrCallHandler(vm, xhr, "onreadystatechange")
			return goja.Undefined()
		})

		xhr.Set("setRequestHeader", func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) < 2 {
				panic(vm.NewTypeError("setRequestHeader requires 2 arguments"))
			}
			key := strings.TrimSpace(call.Argument(0).String())
			value := call.Argument(1).String()
			if key != "" {
				headers[key] = value
			}
			return goja.Undefined()
		})

		xhr.Set("send", func(call goja.FunctionCall) goja.Value {
			if rawURL == "" {
				panic(vm.NewTypeError("send called before open"))
			}

			if len(call.Arguments) > 0 && !goja.IsUndefined(call.Argument(0)) && !goja.IsNull(call.Argument(0)) {
				requestBody = []byte(call.Argument(0).String())
			} else {
				requestBody = nil
			}

			parsed, err := url.Parse(rawURL)
			if err != nil {
				xhr.Set("readyState", 4)
				xhr.Set("status", 0)
				xhr.Set("statusText", err.Error())
				xhrCallHandler(vm, xhr, "onerror")
				xhrCallHandler(vm, xhr, "onreadystatechange")
				return goja.Undefined()
			}
			if parsed.Scheme != "http" && parsed.Scheme != "https" {
				xhr.Set("readyState", 4)
				xhr.Set("status", 0)
				xhr.Set("statusText", "unsupported scheme")
				xhrCallHandler(vm, xhr, "onerror")
				xhrCallHandler(vm, xhr, "onreadystatechange")
				return goja.Undefined()
			}

			var body io.Reader
			if len(requestBody) > 0 {
				body = bytes.NewReader(requestBody)
			}
			req, err := http.NewRequest(method, rawURL, body)
			if err != nil {
				xhr.Set("readyState", 4)
				xhr.Set("status", 0)
				xhr.Set("statusText", err.Error())
				xhrCallHandler(vm, xhr, "onerror")
				xhrCallHandler(vm, xhr, "onreadystatechange")
				return goja.Undefined()
			}
			for k, v := range headers {
				req.Header.Set(k, v)
			}

			// 2 HEADERS_RECEIVED (请求已发送)
			xhr.Set("readyState", 2)
			xhrCallHandler(vm, xhr, "onreadystatechange")

			if async {
				go func(req *http.Request) {
					resp, err := cfg.client.Do(req)
					if err != nil {
						loop.RunOnLoop(func(vm *goja.Runtime) {
							xhr.Set("readyState", 4)
							xhr.Set("status", 0)
							xhr.Set("statusText", err.Error())
							xhrCallHandler(vm, xhr, "onerror")
							xhrCallHandler(vm, xhr, "onreadystatechange")
						})
						return
					}
					defer resp.Body.Close()
					bodyBytes, readErr := io.ReadAll(resp.Body)
					loop.RunOnLoop(func(vm *goja.Runtime) {
						responseHeaders = resp.Header.Clone()
						// 3 LOADING
						xhr.Set("readyState", 3)
						xhrCallHandler(vm, xhr, "onreadystatechange")
						if readErr != nil {
							xhr.Set("readyState", 4)
							xhr.Set("status", resp.StatusCode)
							xhr.Set("statusText", readErr.Error())
							xhrCallHandler(vm, xhr, "onerror")
							xhrCallHandler(vm, xhr, "onreadystatechange")
							return
						}
						// 4 DONE
						xhr.Set("readyState", 4)
						xhr.Set("status", resp.StatusCode)
						xhr.Set("statusText", resp.Status)
						xhr.Set("responseText", string(bodyBytes))
						xhr.Set("response", string(bodyBytes))
						xhrCallHandler(vm, xhr, "onreadystatechange")
						xhrCallHandler(vm, xhr, "onload")
					})
				}(req)
				return goja.Undefined()
			}

			// sync: 在 Loop 线程内阻塞执行
			resp, err := cfg.client.Do(req)
			if err != nil {
				xhr.Set("readyState", 4)
				xhr.Set("status", 0)
				xhr.Set("statusText", err.Error())
				xhrCallHandler(vm, xhr, "onerror")
				xhrCallHandler(vm, xhr, "onreadystatechange")
				return goja.Undefined()
			}
			defer resp.Body.Close()
			responseHeaders = resp.Header.Clone()
			// 3 LOADING
			xhr.Set("readyState", 3)
			xhrCallHandler(vm, xhr, "onreadystatechange")
			bodyBytes, err := io.ReadAll(resp.Body)
			if err != nil {
				xhr.Set("readyState", 4)
				xhr.Set("status", resp.StatusCode)
				xhr.Set("statusText", err.Error())
				xhrCallHandler(vm, xhr, "onerror")
				xhrCallHandler(vm, xhr, "onreadystatechange")
				return goja.Undefined()
			}
			// 4 DONE
			xhr.Set("readyState", 4)
			xhr.Set("status", resp.StatusCode)
			xhr.Set("statusText", resp.Status)
			xhr.Set("responseText", string(bodyBytes))
			xhr.Set("response", string(bodyBytes))
			xhrCallHandler(vm, xhr, "onreadystatechange")
			xhrCallHandler(vm, xhr, "onload")

			return goja.Undefined()
		})

		xhr.Set("getAllResponseHeaders", func(call goja.FunctionCall) goja.Value {
			if responseHeaders == nil {
				return vm.ToValue("")
			}
			var b strings.Builder
			for k, vv := range responseHeaders {
				for _, v := range vv {
					b.WriteString(k)
					b.WriteString(": ")
					b.WriteString(v)
					b.WriteString("\r\n")
				}
			}
			return vm.ToValue(b.String())
		})

		xhr.Set("getResponseHeader", func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) < 1 || responseHeaders == nil {
				return goja.Null()
			}
			name := strings.TrimSpace(call.Argument(0).String())
			if name == "" {
				return goja.Null()
			}
			v := responseHeaders.Get(name)
			if v == "" {
				return goja.Null()
			}
			return vm.ToValue(v)
		})

		return nil
	}
}

func xhrCallHandler(vm *goja.Runtime, obj *goja.Object, handlerName string) {
	handler := obj.Get(handlerName)
	if handler == nil || goja.IsUndefined(handler) || goja.IsNull(handler) {
		return
	}
	if fn, ok := goja.AssertFunction(handler); ok {
		_, _ = fn(obj)
	}
}
