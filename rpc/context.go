package rpc

// context.go
// 定义 RPC 执行时可附带的上下文元信息。用于在 handler 中获取当前请求的鉴权/主体信息。
// 注意：避免出现循环依赖，这里仅引用 models.User（database/models 未引用本包）。

import (
	"context"

	"github.com/komari-monitor/komari/database/models"
)

// ContextMeta 保存一次 RPC 调用可用的鉴权/身份元数据。
// 为了便于扩展，字段保持冗余：既保存结构体，也保存对应 UUID / Token。
// 未来如需添加字段（如 IP、UserAgent、TraceID 等）直接在此结构体上扩展即可。
type ContextMeta struct {
	// Permission 当前权限分组 guest/client/admin
	Permission string
	// User 登录的管理员用户（仅 admin 会话存在）
	User *models.User
	// UserUUID 方便无需解引用就能快速判断
	UserUUID string
	// ClientToken 来自客户端的私钥 / 鉴权 token（仅 client 链接可能存在）
	ClientToken string
	// ClientUUID 解析出的客户端 UUID（若 token 合法）
	ClientUUID string
	// RemoteIP 请求来源 IP（可选）
	RemoteIP string
	// UserAgent 请求 UA（可选）
	UserAgent string
}

// 私有类型做 key，避免外部冲突
type ctxMetaKey struct{}

// NewContextWithMeta 将 meta 写入 context
func NewContextWithMeta(parent context.Context, meta *ContextMeta) context.Context {
	if parent == nil {
		parent = context.Background()
	}
	if meta == nil {
		return parent
	}
	return context.WithValue(parent, ctxMetaKey{}, meta)
}

// MetaFromContext 读取 meta；不存在返回 nil
func MetaFromContext(ctx context.Context) *ContextMeta {
	if ctx == nil {
		return nil
	}
	if v := ctx.Value(ctxMetaKey{}); v != nil {
		if m, ok := v.(*ContextMeta); ok {
			return m
		}
	}
	return nil
}
