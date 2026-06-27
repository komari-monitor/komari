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
