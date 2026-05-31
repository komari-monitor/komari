package rpc

// meta.go
// 为已注册的 RPC 方法维护可选的帮助/描述信息，供 rpc.help 使用。
// 不影响原有 Register 行为；未显式附加元数据时会自动生成仅包含名称的占位。

import (
	"sort"
	"strings"
	"sync"
)

// ParamMeta 描述单个参数。
type ParamMeta struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
}

// MethodMeta 描述方法的帮助信息。
type MethodMeta struct {
	Name        string      `json:"name"`
	Summary     string      `json:"summary,omitempty"`
	Description string      `json:"description,omitempty"`
	Params      []ParamMeta `json:"params,omitempty"`
	Returns     string      `json:"returns,omitempty"`
	Example     any         `json:"example,omitempty"`
}

var (
	muMetas     sync.RWMutex
	methodMetas = map[string]*MethodMeta{}
)

// getMetaUnsafe 内部使用：调用方需已持有 handlers 的读/写锁或自行同步（此处独立锁保障并发）。
func getMetaUnsafe(name string) *MethodMeta {
	muMetas.RLock()
	m := methodMetas[name]
	muMetas.RUnlock()
	return m
}

// ensureMeta 确保存在基本元数据（最少 Name）。
func ensureMeta(name string) {
	if name == "" {
		return
	}
	muMetas.Lock()
	if _, ok := methodMetas[name]; !ok {
		methodMetas[name] = &MethodMeta{Name: name}
	}
	muMetas.Unlock()
}

// RegisterMeta 为已注册方法附加/覆盖元数据（Name 字段若为空自动填充）。
func RegisterMeta(name string, meta *MethodMeta) {
	if name == "" || meta == nil {
		return
	}
	if meta.Name == "" {
		meta.Name = name
	}
	muMetas.Lock()
	methodMetas[name] = meta
	muMetas.Unlock()
}

// RegisterWithMeta 同时注册方法与元数据；若注册失败返回错误。
func RegisterWithMeta(method string, h Handler, meta *MethodMeta) error {
	if err := Register(method, h); err != nil {
		return err
	}
	if meta != nil {
		RegisterMeta(method, meta)
	} else {
		ensureMeta(method)
	}
	return nil
}

// listMetas 获取所有方法（按给定过滤器）简要元数据的副本。
func listMetas(includeInternal bool) []*MethodMeta {
	muHandlers.RLock()
	names := make([]string, 0, len(handlers))
	for n := range handlers {
		names = append(names, n)
	}
	muHandlers.RUnlock()
	out := make([]*MethodMeta, 0, len(names))
	for _, n := range names {
		if !includeInternal && strings.HasPrefix(n, "rpc.") {
			continue
		}
		if m := getMetaUnsafe(n); m != nil {
			out = append(out, &MethodMeta{ // 复制简要字段
				Name:    m.Name,
				Summary: m.Summary,
			})
		} else {
			out = append(out, &MethodMeta{Name: n})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
