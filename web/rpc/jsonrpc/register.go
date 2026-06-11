package jsonrpc

import "github.com/komari-monitor/komari/pkg/rpc"

// Register 以默认分组 "common" 注册方法。
func Register(name string, cb rpc.Handler) error {
	return RegisterWithGroupAndMeta(name, "common", cb, &rpc.MethodMeta{
		Name:        name,
		Summary:     "This method does not provide a summary",
		Description: "This method does not provide a description",
	})
}

// RegisterWithGroupAndMeta 将回调按分组注册为 "group:name"，并附加元数据。
// group 为空时使用默认分组 "common"。
func RegisterWithGroupAndMeta(name, group string, cb rpc.Handler, meta *rpc.MethodMeta) error {
	if group == "" {
		group = "common"
	}
	return rpc.RegisterWithMeta(group+":"+name, cb, meta)
}
