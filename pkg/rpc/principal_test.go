package rpc

import "testing"

func TestPrincipalConstructors(t *testing.T) {
	cases := []struct {
		name     string
		p        *Principal
		wantType PrincipalType
		wantRole string
		wantKey  bool
	}{
		{"anonymous", NewAnonymousPrincipal(), PrincipalAnonymous, RoleGuest, false},
		{"agent", NewAgentPrincipal("c-uuid"), PrincipalAgent, RoleClient, false},
		{"user", NewUserPrincipal("u-uuid"), PrincipalUser, RoleAdmin, false},
		{"apikey", NewAPIKeyPrincipal(), PrincipalAPIKey, RoleAdmin, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.p.Type != tc.wantType {
				t.Errorf("Type = %v, want %v", tc.p.Type, tc.wantType)
			}
			if got := tc.p.PrimaryRole(); got != tc.wantRole {
				t.Errorf("PrimaryRole() = %q, want %q", got, tc.wantRole)
			}
			if tc.p.IsAPIKey != tc.wantKey {
				t.Errorf("IsAPIKey = %v, want %v", tc.p.IsAPIKey, tc.wantKey)
			}
		})
	}
}

func TestPrincipalConstructorsFields(t *testing.T) {
	if p := NewAgentPrincipal("c-uuid"); p.ClientUUID != "c-uuid" {
		t.Errorf("agent ClientUUID = %q, want %q", p.ClientUUID, "c-uuid")
	}
	if p := NewUserPrincipal("u-uuid"); p.UserUUID != "u-uuid" {
		t.Errorf("user UserUUID = %q, want %q", p.UserUUID, "u-uuid")
	}
}

func TestPrimaryRolePicksHighest(t *testing.T) {
	p := &Principal{Roles: []string{RoleGuest, RoleAdmin, RoleClient}}
	if got := p.PrimaryRole(); got != RoleAdmin {
		t.Errorf("PrimaryRole() = %q, want %q (highest level)", got, RoleAdmin)
	}
}

func TestPrimaryRoleEmptyOrNil(t *testing.T) {
	if got := (&Principal{}).PrimaryRole(); got != RoleGuest {
		t.Errorf("empty Roles PrimaryRole() = %q, want %q", got, RoleGuest)
	}
	var p *Principal
	if got := p.PrimaryRole(); got != RoleGuest {
		t.Errorf("nil principal PrimaryRole() = %q, want %q", got, RoleGuest)
	}
}

func TestHasRole(t *testing.T) {
	p := NewUserPrincipal("u")
	if !p.HasRole(RoleAdmin) {
		t.Error("user principal should have admin role")
	}
	if p.HasRole(RoleClient) {
		t.Error("user principal should not have client role")
	}
	var nilP *Principal
	if nilP.HasRole(RoleGuest) {
		t.Error("nil principal should not have any role")
	}
}

func TestCheckPrincipal(t *testing.T) {
	cases := []struct {
		name   string
		p      *Principal
		method string
		want   bool
	}{
		// agent 主体:可调 client 与 common,不可调 admin
		{"agent->client", NewAgentPrincipal("c1"), "client:report", true},
		{"agent->common", NewAgentPrincipal("c1"), "common:getNodes", true},
		{"agent->admin", NewAgentPrincipal("c1"), "admin:addClient", false},
		// user 主体:可调 admin 与 common,不可调 client(正交,堵住冒充)
		{"user->admin", NewUserPrincipal("u1"), "admin:addClient", true},
		{"user->common", NewUserPrincipal("u1"), "common:getNodes", true},
		{"user->client", NewUserPrincipal("u1"), "client:report", false},
		// api key 主体:等同 admin 能力
		{"apikey->admin", NewAPIKeyPrincipal(), "admin:addClient", true},
		{"apikey->client", NewAPIKeyPrincipal(), "client:report", false},
		// 匿名主体:仅公共方法
		{"anon->common", NewAnonymousPrincipal(), "common:getNodes", true},
		{"anon->admin", NewAnonymousPrincipal(), "admin:addClient", false},
		{"anon->client", NewAnonymousPrincipal(), "client:report", false},
		// nil 主体按匿名处理
		{"nil->common", nil, "common:getNodes", true},
		{"nil->admin", nil, "admin:addClient", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CheckPrincipal(c.p, c.method); got != c.want {
				t.Errorf("CheckPrincipal(%s, %q) = %v, want %v", c.name, c.method, got, c.want)
			}
		})
	}
}

