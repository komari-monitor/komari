package jsonrpc

import "testing"

// TestPrivateSiteLoginWhitelist 守卫 issue #567:私有站点模式下,登录页渲染所需的
// 元信息接口必须始终在白名单中,否则匿名用户无法看到登录框。
func TestPrivateSiteLoginWhitelist(t *testing.T) {
	required := []string{
		"public:getMe",
		"public:getPublicSettings",
		"public:getVersion",
		"public:recordVisitorEvent",
	}
	for _, m := range required {
		if !privateSiteLoginWhitelist[m] {
			t.Errorf("method %q must be in privateSiteLoginWhitelist for login page to render under private site (issue #567)", m)
		}
	}

	// 节点列表等数据接口不应在白名单(应被私有站点拦截)。
	mustBlocked := []string{
		"public:getNodesInformation",
		"public:getRecordsByUUID",
		"public:getPingRecords",
	}
	for _, m := range mustBlocked {
		if privateSiteLoginWhitelist[m] {
			t.Errorf("data method %q must NOT be in privateSiteLoginWhitelist (would leak data under private site)", m)
		}
	}
}
