package rpc

import (
	"context"
	"testing"
)

func TestNamespaceOf(t *testing.T) {
	cases := map[string]string{
		"getNodes":        DefaultNamespace,
		"admin:addClient": "admin",
		"client:report":   "client",
		"rpc.ping":        DefaultNamespace, // 无 ":"，归入默认命名空间
		":bare":           "",
	}
	for method, want := range cases {
		if got := NamespaceOf(method); got != want {
			t.Errorf("NamespaceOf(%q) = %q, want %q", method, got, want)
		}
	}
}

func TestCheckPermission(t *testing.T) {
	cases := []struct {
		group  string
		method string
		want   bool
	}{
		{RoleGuest, "common:getNodes", true},
		{RoleGuest, "getNodes", true},
		{RoleGuest, "admin:addClient", false},
		{RoleGuest, "client:report", false},
		{RoleClient, "client:report", true},
		{RoleClient, "admin:addClient", false},
		{RoleAdmin, "admin:addClient", true},
		{RoleAdmin, "client:report", true},
		{RoleAdmin, "common:getNodes", true},
		// 未知命名空间默认要求 admin
		{RoleGuest, "plugin:foo", false},
		{RoleAdmin, "plugin:foo", true},
	}
	for _, c := range cases {
		if got := CheckPermission(c.group, c.method); got != c.want {
			t.Errorf("CheckPermission(%q, %q) = %v, want %v", c.group, c.method, got, c.want)
		}
	}
}

func TestRegisterNamespace(t *testing.T) {
	RegisterNamespace("myplugin", RoleClient)
	if !CheckPermission(RoleClient, "myplugin:doThing") {
		t.Error("client should be allowed in myplugin namespace after registration")
	}
	if CheckPermission(RoleGuest, "myplugin:doThing") {
		t.Error("guest should not be allowed in myplugin namespace")
	}
}

func TestAllowWildcardSpecificity(t *testing.T) {
	// 命名空间默认 admin，但更具体的方法级规则可放宽。
	RegisterNamespace("acltest", RoleAdmin)
	Allow("acltest:public*", RoleGuest) // 前缀通配，比 "acltest:*" 更具体

	if !CheckPermission(RoleGuest, "acltest:publicInfo") {
		t.Error("guest should be allowed: acltest:public* is more specific than acltest:*")
	}
	if CheckPermission(RoleGuest, "acltest:secret") {
		t.Error("guest should be denied acltest:secret (falls back to acltest:* = admin)")
	}

	// 精确规则优先于任何通配。
	Allow("acltest:secret", RoleClient)
	if !CheckPermission(RoleClient, "acltest:secret") {
		t.Error("client should be allowed by exact rule acltest:secret")
	}
	if CheckPermission(RoleGuest, "acltest:secret") {
		t.Error("guest still denied acltest:secret (exact rule requires client)")
	}
}

func TestAllowOverride(t *testing.T) {
	Allow("override:x", RoleAdmin)
	if CheckPermission(RoleGuest, "override:x") {
		t.Fatal("precondition: guest denied")
	}
	Allow("override:x", RoleGuest) // 覆盖同一 pattern
	if !CheckPermission(RoleGuest, "override:x") {
		t.Error("override should relax override:x to guest")
	}
}

func TestInternalMethodsPermission(t *testing.T) {
	// 内部 rpc.* 方法对 guest 开放。
	if !CheckPermission(RoleGuest, "rpc.ping") {
		t.Error("guest should be allowed to call rpc.ping")
	}
	// 裸方法名归入 common，对 guest 开放。
	if !CheckPermission(RoleGuest, "getNodes") {
		t.Error("guest should be allowed to call bare common method getNodes")
	}
}

func TestUnregister(t *testing.T) {
	method := "unreg.sample"
	MustRegister(method, func(ctx context.Context, req *JsonRpcRequest) (any, *JsonRpcError) {
		return "ok", nil
	})
	if getHandler(method) == nil {
		t.Fatal("method should be registered")
	}
	if !Unregister(method) {
		t.Fatal("Unregister should return true for existing method")
	}
	if getHandler(method) != nil {
		t.Fatal("method should be removed after Unregister")
	}
	// 重复注销返回 false
	if Unregister(method) {
		t.Fatal("Unregister should return false for missing method")
	}
	// 保留前缀禁止注销
	if Unregister("rpc.ping") {
		t.Fatal("Unregister must refuse rpc.* reserved methods")
	}
	if getHandler("rpc.ping") == nil {
		t.Fatal("rpc.ping must still be registered")
	}
}
