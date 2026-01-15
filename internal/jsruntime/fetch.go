package jsruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/eventloop"
)

const (
	defaultFetchMaxBodyBytes = int64(10 << 20) // 10MiB
)

type fetchConfig struct {
	client       *http.Client
	maxBodyBytes int64
}

func injectFetch(vm *goja.Runtime, loop *eventloop.EventLoop, client *http.Client) error {
	if vm == nil {
		return errors.New("vm is nil")
	}
	if loop == nil {
		return errors.New("event loop is nil")
	}
	if client == nil {
		return errors.New("http client is nil")
	}

	cfg := fetchConfig{client: client, maxBodyBytes: defaultFetchMaxBodyBytes}

	return vm.Set("fetch", func(call goja.FunctionCall) goja.Value {
		promise, resolve, reject := vm.NewPromise()

		rawURL := strings.TrimSpace(call.Argument(0).String())
		if rawURL == "" {
			reject(vm.NewTypeError("fetch: url is required"))
			return vm.ToValue(promise)
		}

		parsedURL, err := url.Parse(rawURL)
		if err != nil {
			reject(vm.NewTypeError("fetch: invalid url: %v", err))
			return vm.ToValue(promise)
		}
		if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
			reject(vm.NewTypeError("fetch: unsupported scheme: %s", parsedURL.Scheme))
			return vm.ToValue(promise)
		}

		method := "GET"
		headers := http.Header{}
		var bodyReader io.Reader
		contentTypeAutoSet := false

		if optVal := call.Argument(1); optVal != nil && !goja.IsUndefined(optVal) && !goja.IsNull(optVal) {
			optObj := optVal.ToObject(vm)

			if mVal := optObj.Get("method"); mVal != nil && !goja.IsUndefined(mVal) && !goja.IsNull(mVal) {
				m := strings.TrimSpace(mVal.String())
				if m != "" {
					method = strings.ToUpper(m)
				}
			}

			if hVal := optObj.Get("headers"); hVal != nil && !goja.IsUndefined(hVal) && !goja.IsNull(hVal) {
				if err := exportHeaders(vm, hVal, headers); err != nil {
					reject(vm.NewTypeError("fetch: invalid headers: %v", err))
					return vm.ToValue(promise)
				}
			}

			if bVal := optObj.Get("body"); bVal != nil && !goja.IsUndefined(bVal) && !goja.IsNull(bVal) {
				br, autoCT, err := exportBody(vm, bVal)
				if err != nil {
					reject(vm.NewTypeError("fetch: invalid body: %v", err))
					return vm.ToValue(promise)
				}
				bodyReader = br
				contentTypeAutoSet = autoCT
			}
		}

		if contentTypeAutoSet && headers.Get("Content-Type") == "" {
			headers.Set("Content-Type", "application/json; charset=utf-8")
		}

		// 对 GET/HEAD：若传了 body，仍让 http.NewRequest 处理（与浏览器行为不一致，但不强行报错）
		req, err := http.NewRequest(method, parsedURL.String(), bodyReader)
		if err != nil {
			reject(vm.NewGoError(err))
			return vm.ToValue(promise)
		}
		req.Header = headers

		go func() {
			resp, err := cfg.client.Do(req)
			if err != nil {
				loop.RunOnLoop(func(vm *goja.Runtime) {
					_ = reject(vm.NewGoError(err))
					drainMicrotasks(vm)
				})
				return
			}
			defer resp.Body.Close()

			limited := io.LimitReader(resp.Body, cfg.maxBodyBytes+1)
			bodyBytes, readErr := io.ReadAll(limited)
			if readErr != nil {
				loop.RunOnLoop(func(vm *goja.Runtime) {
					_ = reject(vm.NewGoError(readErr))
					drainMicrotasks(vm)
				})
				return
			}
			if int64(len(bodyBytes)) > cfg.maxBodyBytes {
				loop.RunOnLoop(func(vm *goja.Runtime) {
					_ = reject(vm.NewGoError(fmt.Errorf("fetch: response body too large (>%d bytes)", cfg.maxBodyBytes)))
					drainMicrotasks(vm)
				})
				return
			}

			loop.RunOnLoop(func(vm *goja.Runtime) {
				respObj := newResponseObject(vm, resp, bodyBytes)
				_ = resolve(respObj)
				drainMicrotasks(vm)
			})
		}()

		return vm.ToValue(promise)
	})
}

func exportHeaders(vm *goja.Runtime, val goja.Value, dst http.Header) error {
	if dst == nil {
		return errors.New("dst is nil")
	}

	// 支持简单对象：{ "A": "B" }
	var m map[string]any
	if err := vm.ExportTo(val, &m); err == nil && m != nil {
		for k, v := range m {
			if k == "" {
				continue
			}
			dst.Set(k, fmt.Sprint(v))
		}
		return nil
	}

	// 支持 JS 对象（非 plain object）时，退化为枚举自身属性
	obj := val.ToObject(vm)
	for _, k := range obj.Keys() {
		v := obj.Get(k)
		if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
			continue
		}
		dst.Set(k, v.String())
	}
	return nil
}

// exportBody 将 JS 侧 body 转成 io.Reader。
// 返回值 autoContentType=true 表示 body 来源是对象/数组等 JSON，会建议自动设置 Content-Type。
func exportBody(vm *goja.Runtime, val goja.Value) (io.Reader, bool, error) {
	if val == nil || goja.IsUndefined(val) || goja.IsNull(val) {
		return nil, false, nil
	}

	if s, ok := val.Export().(string); ok {
		return strings.NewReader(s), false, nil
	}

	// goja typed array/ArrayBuffer 常见导出为 []byte / []uint8
	if b, ok := val.Export().([]byte); ok {
		return bytes.NewReader(b), false, nil
	}
	if b, ok := val.Export().([]uint8); ok {
		return bytes.NewReader(b), false, nil
	}

	// 兜底：把对象/数组等转 JSON
	exported := val.Export()
	switch exported.(type) {
	case map[string]any, []any:
		data, err := json.Marshal(exported)
		if err != nil {
			return nil, false, err
		}
		return bytes.NewReader(data), true, nil
	default:
		// 其他类型：按字符串处理
		return strings.NewReader(fmt.Sprint(exported)), false, nil
	}
}

func newResponseObject(vm *goja.Runtime, resp *http.Response, body []byte) *goja.Object {
	obj := vm.NewObject()

	obj.Set("status", resp.StatusCode)
	obj.Set("statusText", resp.Status)
	obj.Set("ok", resp.StatusCode >= 200 && resp.StatusCode < 300)
	if resp.Request != nil && resp.Request.URL != nil {
		obj.Set("url", resp.Request.URL.String())
	} else {
		obj.Set("url", "")
	}

	obj.Set("headers", newHeadersObject(vm, resp.Header))

	obj.Set("text", func(call goja.FunctionCall) goja.Value {
		p, resolve, _ := vm.NewPromise()
		resolve(vm.ToValue(string(body)))
		return vm.ToValue(p)
	})

	obj.Set("json", func(call goja.FunctionCall) goja.Value {
		p, resolve, reject := vm.NewPromise()
		var out any
		if err := json.Unmarshal(body, &out); err != nil {
			reject(vm.NewGoError(err))
			return vm.ToValue(p)
		}
		resolve(vm.ToValue(out))
		return vm.ToValue(p)
	})

	obj.Set("arrayBuffer", func(call goja.FunctionCall) goja.Value {
		p, resolve, _ := vm.NewPromise()
		// goja 的 ArrayBuffer 是拷贝语义，避免外部修改影响内部
		buf := make([]byte, len(body))
		copy(buf, body)
		resolve(vm.ToValue(vm.NewArrayBuffer(buf)))
		return vm.ToValue(p)
	})

	return obj
}

func newHeadersObject(vm *goja.Runtime, h http.Header) *goja.Object {
	obj := vm.NewObject()

	obj.Set("get", func(call goja.FunctionCall) goja.Value {
		name := strings.TrimSpace(call.Argument(0).String())
		if name == "" {
			return goja.Undefined()
		}
		v := h.Get(name)
		if v == "" {
			return goja.Undefined()
		}
		return vm.ToValue(v)
	})

	obj.Set("has", func(call goja.FunctionCall) goja.Value {
		name := strings.TrimSpace(call.Argument(0).String())
		if name == "" {
			return vm.ToValue(false)
		}
		_, ok := h[http.CanonicalHeaderKey(name)]
		return vm.ToValue(ok)
	})

	obj.Set("raw", func(call goja.FunctionCall) goja.Value {
		m := make(map[string]any, len(h))
		for k, vv := range h {
			cp := make([]string, len(vv))
			copy(cp, vv)
			m[k] = cp
		}
		return vm.ToValue(m)
	})

	return obj
}
