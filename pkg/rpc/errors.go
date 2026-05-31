package rpc

import "fmt"

// JsonRpcError 错误对象
// Code 按 JSON-RPC 规范使用 -32768 ~ -32000 的预留范围，其它业务自定义可使用正数或自定义区间。
// Message 为简短描述，Data 可携带扩展信息（结构化或字符串）。
type JsonRpcError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// 预定义错误码	JSON-RPC 2.0 标准
const (
	ParseError     = -32700
	InvalidRequest = -32600
	MethodNotFound = -32601
	InvalidParams  = -32602
	InternalError  = -32603
)

// Komari 定义错误码
const (
	NotFound         = -32044
	AlreadyExists    = -32045
	PermissionDenied = -32041
	Unauthenticated  = -32040 // 未登录/无身份
	Cancelled        = -32010 // 主动取消
	DeadlineExceeded = -32011 // 超时
	Aborted          = -32021 // 并发冲突/事务中断
	OutOfRange       = -32022 // 数值/索引越界
	Unimplemented    = -32050 // 尚未实现
	Unavailable      = -32051 // 依赖服务暂不可用
	DataLoss         = -32052 // 不可恢复的数据丢失
)

// MakeError 便捷创建错误对象
func MakeError(code int, msg string, data any) *JsonRpcError {
	return &JsonRpcError{Code: code, Message: msg, Data: data}
}

func (e *JsonRpcError) Error() string {
	return fmt.Sprintf("JSON-RPC Error %d: %s", e.Code, e.Message)
}

func (e *JsonRpcError) Response() *JsonRpcResponse {
	return e.ResponseWithID(nil)
}
func (e *JsonRpcError) ResponseWithID(id any) *JsonRpcResponse {
	return &JsonRpcResponse{
		Version: RPC_VERSION,
		Error:   e,
		ID:      id,
	}
}
