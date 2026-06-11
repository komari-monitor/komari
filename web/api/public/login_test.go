package public

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/stretchr/testify/assert"
)

func TestLogin(t *testing.T) {
	// 设置测试模式
	gin.SetMode(gin.TestMode)
	accounts.CreateAccount("testuser", "correctpassword")
	tests := []struct {
		name           string
		requestBody    LoginRequest
		expectedStatus int
		expectedBody   map[string]interface{}
	}{
		{
			name: "成功登录",
			requestBody: LoginRequest{
				Username: "testuser",
				Password: "correctpassword",
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "无效的请求体",
			requestBody: LoginRequest{
				Username: "",
				Password: "",
			},
			expectedStatus: http.StatusBadRequest,
			expectedBody: map[string]interface{}{
				"status":  "error",
				"message": "Invalid request body: Username and password are required",
			},
		},
		{
			name: "错误的凭据",
			requestBody: LoginRequest{
				Username: "wronguser",
				Password: "wrongpassword",
			},
			expectedStatus: http.StatusUnauthorized,
			expectedBody: map[string]interface{}{
				"status":  "error",
				"message": "Invalid credentials",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 创建测试路由
			router := gin.New()
			router.POST("/login", Login)

			// 创建测试请求
			jsonBody, _ := json.Marshal(tt.requestBody)
			req, _ := http.NewRequest("POST", "/login", bytes.NewBuffer(jsonBody))
			req.Header.Set("Content-Type", "application/json")

			// 创建响应记录器
			w := httptest.NewRecorder()

			// 执行请求
			router.ServeHTTP(w, req)

			// 断言状态码
			assert.Equal(t, tt.expectedStatus, w.Code)

			// 解析响应体
			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			assert.NoError(t, err)

			// 断言响应体
			if tt.expectedStatus == http.StatusOK {
				// 对于成功的情况，我们只检查响应结构，不检查具体的 session token
				assert.Equal(t, "success", response["status"])
				assert.Equal(t, "", response["message"])
				data, ok := response["data"].(map[string]interface{})
				assert.True(t, ok)
				setCookie, ok := data["set-cookie"].(map[string]interface{})
				assert.True(t, ok)
				assert.NotEmpty(t, setCookie["session_token"])
				assert.Contains(t, strings.Join(w.Header().Values("Set-Cookie"), "\n"), "session_token=")
			} else {
				assert.Equal(t, tt.expectedBody, response)
			}
		})
	}
	// 清除测试数据
	accounts.DeleteAccountByUsername("testuser")
	accounts.DeleteAllSessions()
}

func TestSessionCookieSecureFollowsRequestScheme(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		requestURL string
		wantSecure bool
	}{
		{
			name:       "http",
			requestURL: "http://example.test/login",
		},
		{
			name:       "https",
			requestURL: "https://example.test/login",
			wantSecure: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, tt.requestURL, nil)

			setSessionCookie(c, "test-session", sessionCookieMaxAge)

			setCookie := strings.Join(w.Header().Values("Set-Cookie"), "\n")
			if tt.wantSecure {
				assert.Contains(t, setCookie, "Secure")
			} else {
				assert.NotContains(t, setCookie, "Secure")
			}
		})
	}
}
