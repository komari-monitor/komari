package rpc

// JsonRpcResponse JSON-RPC 2.0 响应
// 成功时包含 result，失败时包含 error；二者互斥。
// 在 Notification 情况下服务器不会发送任何响应。
type JsonRpcResponse struct {
	Version string        `json:"jsonrpc"`
	ID      any           `json:"id,omitempty"`
	Result  any           `json:"result,omitempty"`
	Error   *JsonRpcError `json:"error,omitempty"`
}

// SuccessResponse 构造成功响应
func SuccessResponse(id any, result any) *JsonRpcResponse {
	return &JsonRpcResponse{Version: RPC_VERSION, ID: id, Result: result}
}

// ErrorResponse 构造失败响应
func ErrorResponse(id any, code int, msg string, data any) *JsonRpcResponse {
	return &JsonRpcResponse{Version: RPC_VERSION, ID: id, Error: &JsonRpcError{Code: code, Message: msg, Data: data}}
}

// InternalErrorResponse 统一内部错误
func InternalErrorResponse(id any, err error) *JsonRpcResponse {
	msg := "internal error"
	if err != nil {
		msg = err.Error()
	}
	return ErrorResponse(id, InternalError, msg, nil)
}
