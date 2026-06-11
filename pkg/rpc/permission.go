package rpc

// permission.go
// 声明式 ACL 权限模型。权限由一组规则 (pattern, minRole) 声明，pattern 支持 "*" 通配符，
// 匹配完整方法名（如 "admin:addClient"、"rpc.ping"）。判权时在所有匹配规则中取
// 特异性（specificity）最高者：精确匹配 > 字面前缀更长的通配 > 全局 "*"。
//
// 该模型面向插件扩展：插件可用 Allow 声明任意粒度的规则（命名空间级 "ns:*" 或方法级），
// 无需修改核心代码。
//
// 角色采用分级语义：guest < client < admin，规则声明所需最低角色。

import (
	"strings"
	"sync"
)

// 角色常量。与 web/api 中的角色保持一致（guest/client/admin）。
const (
	RoleGuest  = "guest"
	RoleClient = "client"
	RoleAdmin  = "admin"
)

// roleLevel 定义角色的权限等级。数值越大权限越高。未知角色按 guest（最低）处理。
var roleLevel = map[string]int{
	RoleGuest:  0,
	RoleClient: 1,
	RoleAdmin:  2,
}

// DefaultNamespace 是方法名不含 ":" 时归入的命名空间。
const DefaultNamespace = "common"

// aclRule 一条声明式 ACL 规则。
type aclRule struct {
	pattern string // 方法名匹配模式，支持 "*" 通配
	minRole string // 匹配时所需的最低角色
	// 预计算的特异性：精确匹配（无通配）给高位 bonus，其余按字面字符数。
	specificity int
	hasWildcard bool
}

var (
	muACL   sync.RWMutex
	aclList []aclRule
)

func init() {
	// 默认规则覆盖内置命名空间语义。"*" 兜底要求 admin，确保未声明的方法默认最严格。
	Allow("*", RoleAdmin)
	Allow("common:*", RoleGuest)
	Allow("guest:*", RoleGuest)
	Allow("rpc.*", RoleGuest) // 内部方法 rpc.ping/rpc.help 等（用 "." 分隔）
	Allow("rpc:*", RoleGuest)
	Allow("client:*", RoleClient)
	Allow("admin:*", RoleAdmin)
}

// levelOf 返回角色的权限等级，未知角色视为 guest。
func levelOf(role string) int {
	if lv, ok := roleLevel[role]; ok {
		return lv
	}
	return roleLevel[RoleGuest]
}

// NamespaceOf 解析方法名的命名空间。"ns:method" 返回 "ns"；无 ":" 返回 DefaultNamespace。
func NamespaceOf(method string) string {
	if i := strings.IndexByte(method, ':'); i >= 0 {
		return method[:i]
	}
	return DefaultNamespace
}

// computeSpecificity 计算 pattern 的特异性。精确匹配（不含通配符）给一个大 bonus
// 以保证优先级最高；含通配符的按字面（非 "*"）字符数排序，前缀越长越具体。
func computeSpecificity(pattern string) (spec int, hasWildcard bool) {
	literal := 0
	for _, r := range pattern {
		if r != '*' {
			literal++
		} else {
			hasWildcard = true
		}
	}
	if !hasWildcard {
		return 1 << 20, false // 精确匹配最高优先
	}
	return literal, true
}

// Allow 声明一条 ACL 规则：匹配 pattern 的方法允许 minRole 及以上等级的角色调用。
// 同一 pattern 重复声明时覆盖原规则。供插件声明自定义权限。
func Allow(pattern, minRole string) {
	spec, hasWildcard := computeSpecificity(pattern)
	rule := aclRule{pattern: pattern, minRole: minRole, specificity: spec, hasWildcard: hasWildcard}
	muACL.Lock()
	defer muACL.Unlock()
	for i := range aclList {
		if aclList[i].pattern == pattern {
			aclList[i] = rule
			return
		}
	}
	aclList = append(aclList, rule)
}

// RegisterNamespace 便捷封装：为整个命名空间声明所需最低角色（等价于 Allow("ns:*", role)）。
func RegisterNamespace(namespace, requiredRole string) {
	Allow(namespace+":*", requiredRole)
}

// wildcardMatch 判断 s 是否匹配只含 "*" 通配符的 pattern。"*" 匹配任意（含空）字符序列。
func wildcardMatch(pattern, s string) bool {
	// 双指针 + 回溯，O(len(pattern)+len(s))。
	var (
		p, str       = 0, 0
		star         = -1
		strBackup    = 0
		lenP, lenStr = len(pattern), len(s)
	)
	for str < lenStr {
		if p < lenP && (pattern[p] == s[str]) {
			p++
			str++
		} else if p < lenP && pattern[p] == '*' {
			star = p
			strBackup = str
			p++
		} else if star != -1 {
			p = star + 1
			strBackup++
			str = strBackup
		} else {
			return false
		}
	}
	for p < lenP && pattern[p] == '*' {
		p++
	}
	return p == lenP
}

// resolveMinRole 返回 method 适用规则中特异性最高者的所需角色。无匹配规则时默认 admin。
func resolveMinRole(method string) string {
	// 归一化：无命名空间分隔符（既无 ":" 也无 "rpc." 内部前缀）的裸方法名归入默认命名空间，
	// 以便被 "common:*" 等规则匹配，保持与历史行为一致。
	if !strings.ContainsAny(method, ":") && !strings.HasPrefix(method, "rpc.") {
		method = DefaultNamespace + ":" + method
	}
	muACL.RLock()
	defer muACL.RUnlock()
	bestSpec := -1
	bestRole := RoleAdmin
	matched := false
	for i := range aclList {
		r := &aclList[i]
		if !wildcardMatch(r.pattern, method) {
			continue
		}
		// 特异性更高者胜出；相同特异性时取更严格（等级更高）的角色，偏向安全。
		if r.specificity > bestSpec || (r.specificity == bestSpec && levelOf(r.minRole) > levelOf(bestRole)) {
			bestSpec = r.specificity
			bestRole = r.minRole
			matched = true
		}
	}
	if !matched {
		return RoleAdmin
	}
	return bestRole
}

// RequiredRole 返回调用 method 所需的最低角色。
func RequiredRole(method string) string {
	return resolveMinRole(method)
}

// CheckPermission 判定 group 角色是否有权调用 method。
func CheckPermission(group, method string) bool {
	return levelOf(group) >= levelOf(resolveMinRole(method))
}
