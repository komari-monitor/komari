package rpc

import "context"

// Invoke 便捷调用：构造请求并执行已注册方法，返回 result 或 *JsonRpcError。
// 不生成 JsonRpcResponse。
//
// @ref Call
func Invoke(method string, params any) (any, *JsonRpcError) {
	req := &JsonRpcRequest{Version: RPC_VERSION, Method: method, Params: params}
	if e := req.Validate(); e != nil {
		return nil, e
	}
	muHandlers.RLock()
	h, ok := handlers[method]
	muHandlers.RUnlock()
	if !ok {
		return nil, &JsonRpcError{Code: MethodNotFound, Message: "method not found", Data: method}
	}
	return h(context.Background(), req)
}

// Call 执行方法并直接返回完整的 JSON-RPC Response。
// 适用于对外暴露：始终返回结构（包括错误）。
// ctx: 执行上下文；id: 请求 id；method/params: 方法与参数。
func Call(id any, method string, params any) *JsonRpcResponse {
	return CallWithContext(context.Background(), id, method, params)
}

func CallWithContext(ctx context.Context, id any, method string, params any) *JsonRpcResponse {
	if ctx == nil {
		ctx = context.Background()
	}
	req := &JsonRpcRequest{Version: RPC_VERSION, Method: method, Params: params, ID: id}
	if e := req.Validate(); e != nil {
		return ErrorResponse(id, e.Code, e.Message, e.Data)
	}
	muHandlers.RLock()
	h, ok := handlers[method]
	muHandlers.RUnlock()
	if !ok {
		return ErrorResponse(id, MethodNotFound, "method not found", method)
	}
	result, jerr := h(ctx, req)
	if jerr != nil {
		return ErrorResponse(id, jerr.Code, jerr.Message, jerr.Data)
	}
	return SuccessResponse(id, result)
}
