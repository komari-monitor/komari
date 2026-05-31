package rpc

import (
	"context"
	"testing"
)

// helper to fetch handler (test only)
func getHandler(name string) Handler {
	muHandlers.RLock()
	defer muHandlers.RUnlock()
	return handlers[name]
}

// 重复注册
func TestRegisterDuplicateAndReserved(t *testing.T) {
	name := "sample.method"
	// first register should succeed
	if err := Register(name, func(ctx context.Context, req *JsonRpcRequest) (any, *JsonRpcError) { return "ok", nil }); err != nil {
		t.Fatalf("first Register failed: %v", err)
	}
	// duplicate register should error
	if err := Register(name, func(ctx context.Context, req *JsonRpcRequest) (any, *JsonRpcError) { return "ok", nil }); err == nil {
		t.Fatalf("expected duplicate register error")
	}
	// reserved prefix
	if err := Register("rpc.test", func(ctx context.Context, req *JsonRpcRequest) (any, *JsonRpcError) { return nil, nil }); err == nil {
		t.Fatalf("expected reserved prefix error")
	}
}

// 内部函数
func TestInternalMethods(t *testing.T) {
	res, err := Invoke("rpc.ping", nil)
	if err != nil {
		t.Fatalf("rpc.ping returned error: %+v", err)
	}
	if res != "pong" {
		t.Fatalf("expected pong got %v", res)
	}
	// version
	res, err = Invoke("rpc.version", nil)
	if err != nil {
		t.Fatalf("rpc.version returned error: %+v", err)
	}
	if res != RPC_VERSION {
		t.Fatalf("expected version %s got %v", RPC_VERSION, res)
	}
}

// Test rpc.help functionality
func TestRpcHelp(t *testing.T) {
	// Register a method with metadata
	method := "demo.echo"
	RegisterWithMeta(method, func(ctx context.Context, req *JsonRpcRequest) (any, *JsonRpcError) {
		v, _ := GetParamAs[string](req, "v")
		return v, nil
	}, &MethodMeta{Summary: "echo back string", Description: "Returns the provided string parameter v", Params: []ParamMeta{{Name: "v", Type: "string", Required: false, Description: "value to echo"}}, Returns: "string"})

	// Query single method
	resp := Call(1, "rpc.help", map[string]any{"method": method})
	if resp.Error != nil {
		t.Fatalf("rpc.help single error: %+v", resp.Error)
	}

	resp = Call(2, "rpc.help", "rpc.help")
	if resp.Error != nil {
		t.Fatalf("rpc.help multiple error: %+v", resp.Error)
	}
	resp = Call(3, "rpc.help", nil)
	if resp.Error != nil {
		t.Fatalf("rpc.help nil params error: %+v", resp.Error)
	}
}

// Test BindParams positional array -> struct mapping
func TestBindParamsPositionalStruct(t *testing.T) {
	type Pair struct {
		First  int
		Second int
	}
	req := &JsonRpcRequest{Version: RPC_VERSION, Method: "x", Params: []any{1, 2}}
	var p Pair
	if err := req.BindParams(&p); err != nil {
		t.Fatalf("BindParams failed: %v", err)
	}
	if p.First != 1 || p.Second != 2 {
		t.Fatalf("unexpected struct values: %+v", p)
	}
	// shorter array
	req2 := &JsonRpcRequest{Version: RPC_VERSION, Method: "x", Params: []any{7}}
	p = Pair{}
	if err := req2.BindParams(&p); err != nil {
		t.Fatalf("BindParams short failed: %v", err)
	}
	if p.First != 7 || p.Second != 0 {
		t.Fatalf("expected (7,0) got %+v", p)
	}
	// longer array (extra ignored)
	req3 := &JsonRpcRequest{Version: RPC_VERSION, Method: "x", Params: []any{9, 8, 100}}
	p = Pair{}
	if err := req3.BindParams(&p); err != nil {
		t.Fatalf("BindParams long failed: %v", err)
	}
	if p.First != 9 || p.Second != 8 {
		t.Fatalf("expected (9,8) got %+v", p)
	}
}
