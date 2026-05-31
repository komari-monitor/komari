package rpc

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
)

// JsonRpcRequest 表示 JSON-RPC 2.0 请求（单个或批量中的一个元素）
// 说明：为了能区分 Notification（无 id），这里将 ID 定义为 any 并打上 omitempty。
// 你可以通过 req.HasID() 判断是否为普通请求（需要返回）还是通知。
type JsonRpcRequest struct {
	Version string `json:"jsonrpc"`          // 必须 === "2.0"
	Method  string `json:"method"`           // 方法名；以 "rpc." 前缀的为保留内部方法
	Params  any    `json:"params,omitempty"` // 参数(位置数组或命名对象)
	ID      any    `json:"id,omitempty"`     // 字符串 / 数值 / null；Notification 时省略
}

// NewRequest 创建一个普通请求（带 id）
func NewRequest(id any, method string, params any) *JsonRpcRequest {
	return &JsonRpcRequest{Version: RPC_VERSION, Method: method, Params: params, ID: id}
}

// NewNotification 创建一个 Notification（无 id，不会收到返回）
func NewNotification(method string, params any) *JsonRpcRequest {
	return &JsonRpcRequest{Version: RPC_VERSION, Method: method, Params: params}
}

// HasID 判断是否包含 id（Notification 没有 id，不需要返回）
func (r *JsonRpcRequest) HasID() bool { return r != nil && r.ID != nil }

// Validate 校验请求格式合法性（不校验方法是否存在）
func (r *JsonRpcRequest) Validate() *JsonRpcError {
	if r == nil {
		return &JsonRpcError{Code: InvalidRequest, Message: "invalid request: null"}
	}
	if r.Version != RPC_VERSION {
		return &JsonRpcError{Code: InvalidRequest, Message: "invalid jsonrpc version"}
	}
	if strings.TrimSpace(r.Method) == "" {
		return &JsonRpcError{Code: InvalidRequest, Message: "method required"}
	}
	return nil
}

// GetParams(兼容旧接口)：按名称获取参数（仅 map Object 情况），没有则 target 不变
func (r *JsonRpcRequest) GetParams(name string, target *any) {
	if r == nil || r.Params == nil || target == nil {
		return
	}
	if m, ok := r.Params.(map[string]any); ok {
		*target = m[name]
	}
}

// GetParamAs 获取具名参数并尝试转换为类型 T
func GetParamAs[T any](req *JsonRpcRequest, name string) (val T, ok bool) {
	if req == nil || req.Params == nil {
		return
	}
	if m, isMap := req.Params.(map[string]any); isMap {
		raw, exists := m[name]
		if !exists {
			return
		}
		if v, good := raw.(T); good {
			return v, true
		}
		b, err := json.Marshal(raw)
		if err != nil {
			return
		}
		var t T
		if err = json.Unmarshal(b, &t); err != nil {
			return
		}
		return t, true
	}
	return
}

// GetPositionalParamAs 获取位置参数 idx 的值为 T
func GetPositionalParamAs[T any](req *JsonRpcRequest, idx int) (val T, ok bool) {
	if req == nil || req.Params == nil {
		return
	}
	if arr, isArr := req.Params.([]any); isArr {
		if idx < 0 || idx >= len(arr) {
			return
		}
		raw := arr[idx]
		if v, good := raw.(T); good {
			return v, true
		}
		b, err := json.Marshal(raw)
		if err != nil {
			return
		}
		var t T
		if err = json.Unmarshal(b, &t); err != nil {
			return
		}
		return t, true
	}
	return
}

// BindParams 将 Params 绑定到给定结构体指针。
// 支持：
//  1. object(map) -> 按字段名反序列化（标准 encoding/json 行为，大小写不敏感）
//  2. array([]any) -> 若 target 是结构体指针，按导出字段声明顺序依次填充；
//     数组更短: 剩余字段保持零值；数组更长: 忽略多余元素。
//     非结构体指针则退回原逻辑整体反序列化。
//  3. 单一标量 -> 若 target 是结构体指针，则赋值给第一个导出字段；否则整体反序列化。
//  4. 其它类型 -> 直接整体反序列化。
func (r *JsonRpcRequest) BindParams(target any) error {
	if r == nil {
		return errors.New("nil request")
	}
	if target == nil || reflect.ValueOf(target).Kind() != reflect.Ptr {
		return errors.New("target must be pointer")
	}
	if r.Params == nil {
		return nil
	}
	switch p := r.Params.(type) {
	case map[string]any:
		b, err := json.Marshal(p)
		if err != nil {
			return err
		}
		return json.Unmarshal(b, target)
	case []any:
		// 特殊处理：struct 指针时按字段顺序映射
		rv := reflect.ValueOf(target).Elem()
		if rv.Kind() == reflect.Struct {
			rt := rv.Type()
			ai := 0
			for i := 0; i < rt.NumField() && ai < len(p); i++ {
				f := rt.Field(i)
				if f.PkgPath != "" { // 非导出字段跳过（不占位置）
					continue
				}
				fv := rv.Field(i)
				raw := p[ai]
				ai++
				// 快速路径：可直接赋值
				if raw != nil {
					val := reflect.ValueOf(raw)
					if val.IsValid() {
						if val.Type().AssignableTo(fv.Type()) {
							fv.Set(val)
							continue
						}
						if val.Type().ConvertibleTo(fv.Type()) {
							fv.Set(val.Convert(fv.Type()))
							continue
						}
					}
				}
				// 回退：通过 JSON 做一次精确转换（处理数字 float64 -> int 等）
				b, err := json.Marshal(raw)
				if err != nil {
					return err
				}
				tmp := reflect.New(fv.Type())
				if err = json.Unmarshal(b, tmp.Interface()); err != nil {
					return err
				}
				fv.Set(tmp.Elem())
			}
			return nil
		}
		// 非 struct 情况，退回整体解码
		b, err := json.Marshal(p)
		if err != nil {
			return err
		}
		return json.Unmarshal(b, target)
	default:
		// 单一标量到结构体首个导出字段
		rv := reflect.ValueOf(target).Elem()
		if rv.Kind() == reflect.Struct {
			rt := rv.Type()
			for i := 0; i < rt.NumField(); i++ {
				f := rt.Field(i)
				if f.PkgPath != "" { // 非导出
					continue
				}
				fv := rv.Field(i)
				raw := p
				if raw != nil {
					val := reflect.ValueOf(raw)
					if val.IsValid() {
						if val.Type().AssignableTo(fv.Type()) {
							fv.Set(val)
							return nil
						}
						if val.Type().ConvertibleTo(fv.Type()) {
							fv.Set(val.Convert(fv.Type()))
							return nil
						}
					}
				}
				// fallback json 转换
				b, err := json.Marshal(raw)
				if err != nil {
					return err
				}
				tmp := reflect.New(fv.Type())
				if err = json.Unmarshal(b, tmp.Interface()); err != nil {
					return err
				}
				fv.Set(tmp.Elem())
				return nil
			}
			// 无导出字段
			return nil
		}
		b, err := json.Marshal(p)
		if err != nil {
			return err
		}
		return json.Unmarshal(b, target)
	}
}
