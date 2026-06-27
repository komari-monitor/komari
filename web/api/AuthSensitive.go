package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/accounts"
)

func RequireSensitive2FA() gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := VerifySensitive2FA(c); err != nil {
			RespondError(c, http.StatusUnauthorized, err.Error())
			c.Abort()
			return
		}
		// 标记本请求已通过敏感操作校验，避免下游（RPC 边界）重复校验。
		c.Set("sensitive_2fa_verified", true)
		c.Next()
	}
}

// VerifySensitive2FACore 传输无关的 2FA 校验核心。
// 输入原始值:userUUID、2FA code、是否为 API Key。
// API Key 豁免;未启用 2FA 的用户放行;其余需要有效 code。
func VerifySensitive2FACore(userUUID, code string, isAPIKey bool) error {
	if isAPIKey {
		return nil
	}
	if userUUID == "" {
		return err2FARequired()
	}
	user, err := accounts.GetUserByUUID(userUUID)
	if err != nil {
		return err
	}
	if user.TwoFactor == "" {
		return nil
	}
	if code == "" {
		return err2FARequired()
	}
	valid, err := accounts.Verify2Fa(userUUID, code)
	if err != nil {
		return err
	}
	if !valid {
		return err2FAInvalid()
	}
	return nil
}

// VerifySensitive2FA gin 适配层:从 gin.Context 提取参数后委托核心校验。
func VerifySensitive2FA(c *gin.Context) error {
	_, isAPIKey := c.Get("api_key")
	uuidRaw, _ := c.Get("uuid")
	uuid, _ := uuidRaw.(string)
	return VerifySensitive2FACore(uuid, get2FACode(c), isAPIKey)
}

func get2FACode(c *gin.Context) string {
	if code, ok := c.Get("2fa_code"); ok {
		if codeString, ok := code.(string); ok && codeString != "" {
			return codeString
		}
	}
	if code := c.GetHeader("X-2FA-Code"); code != "" {
		return code
	}
	if code := c.GetHeader("X-Two-Factor-Code"); code != "" {
		return code
	}
	for _, key := range []string{"2fa_code", "two_factor_code", "otp"} {
		if code := c.Query(key); code != "" {
			return code
		}
	}
	if c.Request.Body == nil || c.Request.Method == http.MethodGet {
		return ""
	}
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return ""
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	if len(bodyBytes) == 0 {
		return ""
	}
	var body map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		return ""
	}
	for _, key := range []string{"2fa_code", "two_factor_code", "otp"} {
		if value, ok := body[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func err2FARequired() error {
	return &sensitive2FAError{"2FA code is required"}
}

func err2FAInvalid() error {
	return &sensitive2FAError{"Invalid 2FA code"}
}

type sensitive2FAError struct {
	message string
}

func (e *sensitive2FAError) Error() string {
	return e.message
}
