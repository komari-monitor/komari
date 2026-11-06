package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
)

// create2FAUser inserts a user with an optional TOTP secret.
func create2FAUser(t *testing.T, username, secret string) string {
	t.Helper()
	user, err := accounts.CreateAccount(username, "Strong-password1")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	if secret != "" {
		if err := dbcore.GetDBInstance().
			Model(&models.User{}).
			Where("uuid = ?", user.UUID).
			Update("two_factor", secret).Error; err != nil {
			t.Fatalf("set two factor: %v", err)
		}
	}
	return user.UUID
}

// TestVerifySensitive2FACore pins the second-factor gate that protects remote
// command execution. Each branch is a distinct security decision.
func TestVerifySensitive2FACore(t *testing.T) {
	resetIdentityState(t)

	noFactorUUID := create2FAUser(t, "user-no-factor", "")
	factorUUID := create2FAUser(t, "user-with-factor", "JBSWY3DPEHPK3PXP")

	tests := []struct {
		name    string
		uuid    string
		code    string
		isAPI   bool
		wantErr string
	}{
		{name: "api key bypasses the second factor", isAPI: true},
		{name: "missing user identity is rejected", wantErr: "2FA code is required"},
		{name: "user without 2FA is allowed through", uuid: noFactorUUID},
		{name: "user with 2FA must present a code", uuid: factorUUID, wantErr: "2FA code is required"},
		{name: "user with 2FA rejects a wrong code", uuid: factorUUID, code: "000000", wantErr: "Invalid 2FA code"},
		{name: "unknown user surfaces the lookup error", uuid: "does-not-exist", code: "123456", wantErr: "record not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifySensitive2FACore(tt.uuid, tt.code, tt.isAPI)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// TestGet2FACodeSources documents every accepted carrier for the code. The
// order matters: context beats headers, headers beat query, query beats body.
func TestGet2FACodeSources(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*gin.Context)
		want   string
	}{
		{
			name: "context wins",
			mutate: func(c *gin.Context) {
				c.Set("2fa_code", "from-context")
				c.Request.Header.Set("X-2FA-Code", "from-header")
			},
			want: "from-context",
		},
		{
			name: "X-2FA-Code header",
			mutate: func(c *gin.Context) {
				c.Request.Header.Set("X-2FA-Code", "from-header")
			},
			want: "from-header",
		},
		{
			name: "X-Two-Factor-Code header",
			mutate: func(c *gin.Context) {
				c.Request.Header.Set("X-Two-Factor-Code", "from-legacy-header")
			},
			want: "from-legacy-header",
		},
		{
			name: "query parameter",
			mutate: func(c *gin.Context) {
				q := c.Request.URL.Query()
				q.Set("otp", "from-query")
				c.Request.URL.RawQuery = q.Encode()
			},
			want: "from-query",
		},
		{
			name: "json body",
			mutate: func(c *gin.Context) {
				c.Request.Method = http.MethodPost
				c.Request.Body = io.NopCloser(strings.NewReader(`{"otp":"from-body"}`))
			},
			want: "from-body",
		},
		{
			name: "GET never reads the body",
			mutate: func(c *gin.Context) {
				c.Request.Body = io.NopCloser(strings.NewReader(`{"otp":"ignored"}`))
			},
			want: "",
		},
		{name: "nothing supplied", mutate: func(c *gin.Context) {}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/admin/task/exec", nil)
			tt.mutate(c)
			if got := get2FACode(c); got != tt.want {
				t.Fatalf("get2FACode = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRequireSensitive2FAAbortsAndPasses covers the middleware wrapper and the
// "already verified" marker that stops the RPC boundary re-checking the code.
func TestRequireSensitive2FAAbortsAndPasses(t *testing.T) {
	resetIdentityState(t)
	plainUUID := create2FAUser(t, "mw-user", "")

	tests := []struct {
		name        string
		setup       func(*gin.Context)
		wantStatus  int
		wantVerified bool
	}{
		{
			name:         "api key passes without a code",
			setup:        func(c *gin.Context) { c.Set("api_key", "key") },
			wantStatus:   http.StatusOK,
			wantVerified: true,
		},
		{
			name:         "user without 2FA passes",
			setup:        func(c *gin.Context) { c.Set("uuid", plainUUID) },
			wantStatus:   http.StatusOK,
			wantVerified: true,
		},
		{
			name:       "anonymous is rejected",
			setup:      func(c *gin.Context) {},
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			var verified bool
			router := gin.New()
			router.Use(func(c *gin.Context) {
				tt.setup(c)
				c.Next()
			})
			router.Use(RequireSensitive2FA())
			router.POST("/protected", func(c *gin.Context) {
				verified = c.GetBool("sensitive_2fa_verified")
				c.Status(http.StatusOK)
			})

			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/protected", nil))
			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			if verified != tt.wantVerified {
				t.Fatalf("sensitive_2fa_verified = %v, want %v", verified, tt.wantVerified)
			}
		})
	}
}
