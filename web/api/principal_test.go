package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/internal/config"
	"github.com/komari-monitor/komari/pkg/rpc"
)

// TestMain wires a shared in-memory SQLite database for identity tests.
func TestMain(m *testing.M) {
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:komari_web_api_test?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	m.Run()
}

// resetIdentityState clears every table IdentifyPrincipal reads from so each
// test observes only the credentials it creates.
func resetIdentityState(t *testing.T) {
	t.Helper()
	db := dbcore.GetDBInstance()
	for _, model := range []any{
		&models.User{},
		&models.Session{},
		&models.Client{},
	} {
		if err := db.Where("1 = 1").Delete(model).Error; err != nil {
			t.Fatalf("reset %T: %v", model, err)
		}
	}
	if err := config.Set(config.ApiKeyKey, ""); err != nil {
		t.Fatalf("reset api key: %v", err)
	}
	t.Cleanup(func() {
		db := dbcore.GetDBInstance()
		for _, model := range []any{&models.User{}, &models.Session{}, &models.Client{}} {
			_ = db.Where("1 = 1").Delete(model).Error
		}
	})
}

const testAPIKey = "0123456789abcdef"

func newIdentityRequest(t *testing.T, mutate func(*http.Request)) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Host = "komari.example"
	if mutate != nil {
		mutate(req)
	}
	c.Request = req
	return c
}

func createUserWithSession(t *testing.T, username string) (userUUID, session string) {
	t.Helper()
	user, err := accounts.CreateAccount(username, "Strong-password1")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	session, err = accounts.CreateSession(user.UUID, 3600, "test-agent", "127.0.0.1", "password")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return user.UUID, session
}

func createClientWithToken(t *testing.T, uuid, token string) {
	t.Helper()
	if err := dbcore.GetDBInstance().Create(&models.Client{UUID: uuid, Token: token}).Error; err != nil {
		t.Fatalf("create client: %v", err)
	}
}

// TestIdentifyPrincipalPrecedence pins the identity precedence documented in
// principal.go: API Key > Session > Client Token > Anonymous. Getting this
// order wrong is a privilege-escalation bug, so every pair is exercised.
func TestIdentifyPrincipalPrecedence(t *testing.T) {
	resetIdentityState(t)
	if err := config.Set(config.ApiKeyKey, testAPIKey); err != nil {
		t.Fatalf("set api key: %v", err)
	}
	userUUID, session := createUserWithSession(t, "admin-user")
	createClientWithToken(t, "client-uuid", "agent-token")

	tests := []struct {
		name      string
		mutate    func(*http.Request)
		wantType  rpc.PrincipalType
		wantRole  string
		wantUUID  string
		wantNotID string
	}{
		{
			name:     "anonymous",
			wantType: rpc.PrincipalAnonymous,
			wantRole: rpc.RoleGuest,
		},
		{
			name: "api key only",
			mutate: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+testAPIKey)
			},
			wantType: rpc.PrincipalAPIKey,
			wantRole: rpc.RoleAdmin,
		},
		{
			name: "session only",
			mutate: func(r *http.Request) {
				r.AddCookie(&http.Cookie{Name: "session_token", Value: session})
			},
			wantType: rpc.PrincipalUser,
			wantRole: rpc.RoleAdmin,
			wantUUID: userUUID,
		},
		{
			name: "client token via query only",
			mutate: func(r *http.Request) {
				q := r.URL.Query()
				q.Set("token", "agent-token")
				r.URL.RawQuery = q.Encode()
			},
			wantType: rpc.PrincipalAgent,
			wantRole: rpc.RoleClient,
			wantUUID: "client-uuid",
		},
		{
			name: "api key outranks session and client token",
			mutate: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+testAPIKey)
				r.AddCookie(&http.Cookie{Name: "session_token", Value: session})
				q := r.URL.Query()
				q.Set("token", "agent-token")
				r.URL.RawQuery = q.Encode()
			},
			wantType: rpc.PrincipalAPIKey,
			wantRole: rpc.RoleAdmin,
		},
		{
			name: "session outranks client token",
			mutate: func(r *http.Request) {
				r.AddCookie(&http.Cookie{Name: "session_token", Value: session})
				q := r.URL.Query()
				q.Set("token", "agent-token")
				r.URL.RawQuery = q.Encode()
			},
			wantType: rpc.PrincipalUser,
			wantRole: rpc.RoleAdmin,
			wantUUID: userUUID,
		},
		{
			name: "invalid session falls through to client token",
			mutate: func(r *http.Request) {
				r.AddCookie(&http.Cookie{Name: "session_token", Value: "forged-session"})
				q := r.URL.Query()
				q.Set("token", "agent-token")
				r.URL.RawQuery = q.Encode()
			},
			wantType: rpc.PrincipalAgent,
			wantRole: rpc.RoleClient,
			wantUUID: "client-uuid",
		},
		{
			name: "invalid api key does not authenticate",
			mutate: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer wrong-key-value")
			},
			wantType: rpc.PrincipalAnonymous,
			wantRole: rpc.RoleGuest,
		},
		{
			name: "unknown client token is anonymous",
			mutate: func(r *http.Request) {
				q := r.URL.Query()
				q.Set("token", "unknown-token")
				r.URL.RawQuery = q.Encode()
			},
			wantType: rpc.PrincipalAnonymous,
			wantRole: rpc.RoleGuest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newIdentityRequest(t, tt.mutate)
			p := IdentifyPrincipal(c)

			if p.Type != tt.wantType {
				t.Fatalf("principal type = %v, want %v", p.Type, tt.wantType)
			}
			if got := p.PrimaryRole(); got != tt.wantRole {
				t.Fatalf("role = %q, want %q", got, tt.wantRole)
			}
			if tt.wantUUID != "" && p.UserUUID+p.ClientUUID != tt.wantUUID {
				t.Fatalf("principal uuid = %q/%q, want %q", p.UserUUID, p.ClientUUID, tt.wantUUID)
			}
		})
	}
}

// TestIdentifyPrincipalRejectsShortAPIKeyConfig guards the minimum-length rule
// in isApiKeyValid: a short configured key must never authenticate.
func TestIdentifyPrincipalRejectsShortAPIKeyConfig(t *testing.T) {
	resetIdentityState(t)
	if err := config.Set(config.ApiKeyKey, "short"); err != nil {
		t.Fatalf("set api key: %v", err)
	}
	c := newIdentityRequest(t, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer short")
	})
	if p := IdentifyPrincipal(c); p.Type != rpc.PrincipalAnonymous {
		t.Fatalf("short api key authenticated as %v, want anonymous", p.Type)
	}
}

// TestIdentifyPrincipalDoesNotAcceptBareAPIKey ensures the Authorization
// header must carry the "Bearer " prefix.
func TestIdentifyPrincipalDoesNotAcceptBareAPIKey(t *testing.T) {
	resetIdentityState(t)
	if err := config.Set(config.ApiKeyKey, testAPIKey); err != nil {
		t.Fatalf("set api key: %v", err)
	}
	c := newIdentityRequest(t, func(r *http.Request) {
		r.Header.Set("Authorization", testAPIKey)
	})
	if p := IdentifyPrincipal(c); p.Type != rpc.PrincipalAnonymous {
		t.Fatalf("bare api key authenticated as %v, want anonymous", p.Type)
	}
}

// TestExtractClientTokenSources documents every place a client token may
// arrive. The body path must restore the request body for later handlers.
func TestExtractClientTokenSources(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*http.Request)
		want   string
	}{
		{
			name: "query token",
			mutate: func(r *http.Request) {
				q := r.URL.Query()
				q.Set("token", "from-query")
				r.URL.RawQuery = q.Encode()
			},
			want: "from-query",
		},
		{
			name: "query Authorization (rpc2 convention)",
			mutate: func(r *http.Request) {
				q := r.URL.Query()
				q.Set("Authorization", "from-auth-query")
				r.URL.RawQuery = q.Encode()
			},
			want: "from-auth-query",
		},
		{
			name: "json body on POST",
			mutate: func(r *http.Request) {
				r.Method = http.MethodPost
				r.Body = io.NopCloser(strings.NewReader(`{"token":"from-body"}`))
			},
			want: "from-body",
		},
		{
			name: "GET ignores body",
			mutate: func(r *http.Request) {
				r.Body = io.NopCloser(strings.NewReader(`{"token":"ignored"}`))
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newIdentityRequest(t, tt.mutate)
			if got := extractClientToken(c); got != tt.want {
				t.Fatalf("extractClientToken = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestExtractClientTokenRestoresBody verifies a handler downstream can still
// read the body after identity extraction consumed it.
func TestExtractClientTokenRestoresBody(t *testing.T) {
	const body = `{"token":"from-body","other":"kept"}`
	c := newIdentityRequest(t, func(r *http.Request) {
		r.Method = http.MethodPost
		r.Body = io.NopCloser(strings.NewReader(body))
	})
	if got := extractClientToken(c); got != "from-body" {
		t.Fatalf("token = %q", got)
	}
	restored, err := io.ReadAll(c.Request.Body)
	if err != nil {
		t.Fatalf("read restored body: %v", err)
	}
	if string(restored) != body {
		t.Fatalf("restored body = %q, want %q", restored, body)
	}
}

// TestGetPrincipalRoundTrip covers the context helpers used by downstream
// handlers and the RPC boundary.
func TestGetPrincipalRoundTrip(t *testing.T) {
	c := newIdentityRequest(t, nil)
	if p := GetPrincipal(c); p != nil {
		t.Fatalf("principal before SetPrincipal = %v, want nil", p)
	}
	want := rpc.NewUserPrincipal("user-uuid")
	SetPrincipal(c, want)
	if got := GetPrincipal(c); got != want {
		t.Fatalf("principal = %v, want %v", got, want)
	}
}

// TestRequireRoleRejectsAndAllows pins the declarative role gate.
func TestRequireRoleRejectsAndAllows(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		name       string
		role       string
		allowed    []string
		wantStatus int
	}{
		{"admin allowed", RoleAdmin, []string{RoleAdmin}, http.StatusOK},
		{"guest rejected from admin", RoleGuest, []string{RoleAdmin}, http.StatusUnauthorized},
		{"missing role defaults to guest", "", []string{RoleAdmin}, http.StatusUnauthorized},
		{"empty role defaults to guest and is allowed", "", []string{RoleGuest}, http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) {
				if tt.role != "" {
					c.Set("role", tt.role)
				}
				c.Next()
			})
			router.Use(RequireRole(tt.allowed...))
			router.GET("/admin/thing", func(c *gin.Context) { c.Status(http.StatusOK) })

			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/thing", nil))
			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
		})
	}
}
