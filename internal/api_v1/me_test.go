package api_v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/internal/database/accounts"
	"github.com/stretchr/testify/assert"
)

func TestGetMe(t *testing.T) {
	// 设置测试模式
	gin.SetMode(gin.TestMode)

	// 创建测试用户
	user, err := accounts.CreateAccount("testuser", "password")
	if err != nil {
		t.Fatalf("Failed to create test user: %v", err)
	}
	uuid := user.UUID
	sessionToken, _ := accounts.CreateSession(uuid, 2592000, "test_user_agent", "127.0.0.1", "oauth")

	tests := []struct {
		name           string
		sessionToken   string
		expectedStatus int
		expectedBody   map[string]interface{}
	}{
		{
			name:           "有效的会话",
			sessionToken:   sessionToken,
			expectedStatus: http.StatusOK,
			expectedBody: map[string]interface{}{
				"username": "testuser",
			},
		},
		{
			name:           "无效的会话",
			sessionToken:   "invalid_token",
			expectedStatus: http.StatusOK,
			expectedBody: map[string]interface{}{
				"username": "Guest",
			},
		},
		{
			name:           "无会话",
			sessionToken:   "",
			expectedStatus: http.StatusOK,
			expectedBody: map[string]interface{}{
				"username": "Guest",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 创建测试路由
			router := gin.New()
			router.GET("/me", GetMe)

			// 创建测试请求
			req, _ := http.NewRequest("GET", "/me", nil)
			if tt.sessionToken != "" {
				req.AddCookie(&http.Cookie{
					Name:  "session_token",
					Value: tt.sessionToken,
				})
			}

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
			assert.Equal(t, tt.expectedBody, response)
		})
	}

	// 清除测试数据
	accounts.DeleteAccountByUsername("testuser")
	accounts.DeleteAllSessions()
}
