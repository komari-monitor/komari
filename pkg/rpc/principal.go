package rpc

// principal.go
// 调用主体(Principal)定义。区分不同主体类型(匿名/agent/用户/API Key),
// 替代原先的单一 group string,使身份信息更结构化、便于后续能力模型扩展。

// PrincipalType 调用主体类型
type PrincipalType int

const (
	// PrincipalAnonymous 匿名访客(未认证)
	PrincipalAnonymous PrincipalType = iota
	// PrincipalAgent 通过 client token 认证的 agent 客户端
	PrincipalAgent
	// PrincipalUser 通过 session cookie 认证的管理员用户
	PrincipalUser
	// PrincipalAPIKey 通过 API Key 认证的调用方
	PrincipalAPIKey
)

// Principal 调用主体,携带身份信息和能力。
type Principal struct {
	// Type 主体类型
	Type PrincipalType
	// UserUUID 用户 UUID(PrincipalUser 时存在)
	UserUUID string
	// ClientUUID agent 客户端 UUID(PrincipalAgent 时存在)
	ClientUUID string
	// IsAPIKey 是否为 API Key 调用(快速判定,等价于 Type==PrincipalAPIKey)
	IsAPIKey bool
	// Roles 角色/能力集。默认由 Type 推导:
	//   - PrincipalAnonymous → [RoleGuest]
	//   - PrincipalAgent → [RoleClient]
	//   - PrincipalUser / PrincipalAPIKey → [RoleAdmin]
	// 未来可扩展为多角色(只读 admin / API Key scope 等)。
	Roles []string
}

// NewAnonymousPrincipal 创建匿名访客主体
func NewAnonymousPrincipal() *Principal {
	return &Principal{
		Type:  PrincipalAnonymous,
		Roles: []string{RoleGuest},
	}
}

// NewAgentPrincipal 创建 agent 客户端主体
func NewAgentPrincipal(clientUUID string) *Principal {
	return &Principal{
		Type:       PrincipalAgent,
		ClientUUID: clientUUID,
		Roles:      []string{RoleClient},
	}
}

// NewUserPrincipal 创建管理员用户主体
func NewUserPrincipal(userUUID string) *Principal {
	return &Principal{
		Type:     PrincipalUser,
		UserUUID: userUUID,
		Roles:    []string{RoleAdmin},
	}
}

// NewAPIKeyPrincipal 创建 API Key 调用主体
func NewAPIKeyPrincipal() *Principal {
	return &Principal{
		Type:     PrincipalAPIKey,
		IsAPIKey: true,
		Roles:    []string{RoleAdmin},
	}
}

// PrimaryRole 返回主体的主要角色(兼容现有单角色模型)。
// 多角色场景下返回权限等级最高的那个。
func (p *Principal) PrimaryRole() string {
	if p == nil || len(p.Roles) == 0 {
		return RoleGuest
	}
	bestRole := p.Roles[0]
	bestLevel := levelOf(bestRole)
	for _, r := range p.Roles[1:] {
		if lv := levelOf(r); lv > bestLevel {
			bestLevel = lv
			bestRole = r
		}
	}
	return bestRole
}

// HasRole 判断主体是否拥有指定角色
func (p *Principal) HasRole(role string) bool {
	if p == nil {
		return false
	}
	for _, r := range p.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// PrincipalFromRole 按角色构造一个最小主体,用于内部调用(OnInternalRequest)等
// 仅知道角色、无具体身份信息的场景。Type 按角色合理推断:
//   guest → Anonymous, client → Agent, admin → User。
// 注意:此构造不携带 UUID/token,仅用于权限判定与兜底,不应据此做审计 actor 归属。
func PrincipalFromRole(role string) *Principal {
	switch role {
	case RoleClient:
		return &Principal{Type: PrincipalAgent, Roles: []string{RoleClient}}
	case RoleAdmin:
		return &Principal{Type: PrincipalUser, Roles: []string{RoleAdmin}}
	default:
		return NewAnonymousPrincipal()
	}
}

