package rpc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Handler 方法签名：返回 result (成功) 或 *JsonRpcError (失败)
type Handler func(ctx context.Context, req *JsonRpcRequest) (any, *JsonRpcError)

var (
	muHandlers sync.RWMutex
	handlers   = map[string]Handler{}
)

// Register 注册方法。重复注册返回错误。保留前缀 "rpc." 禁止外部注册。
func Register(method string, h Handler) error {
	method = strings.TrimSpace(method)
	if method == "" {
		return errors.New("method empty")
	}
	if strings.HasPrefix(method, "rpc.") {
		return errors.New("method prefix 'rpc.' is reserved")
	}
	muHandlers.Lock()
	defer muHandlers.Unlock()
	if _, exists := handlers[method]; exists {
		return fmt.Errorf("method already registered: %s", method)
	}
	handlers[method] = h
	return nil
}

// MustRegister 便捷注册（panic on error）
func MustRegister(method string, h Handler) {
	if err := Register(method, h); err != nil {
		panic(err)
	}
}

// ListMethods 列出当前已注册的方法名（副本）
func ListMethods() []string {
	muHandlers.RLock()
	defer muHandlers.RUnlock()
	res := make([]string, 0, len(handlers))
	for k := range handlers {
		res = append(res, k)
	}
	return res
}
