package api

import (
	"net/http"
	"strings"

	"github.com/komari-monitor/komari/database/accounts"

	"github.com/gin-gonic/gin"
)

func GetMe(c *gin.Context) {
	session, err := c.Cookie("session_token")
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"username": "Guest", "logged_in": false})
		return
	}
	uuid, err := accounts.GetSession(session)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"username": "Guest", "logged_in": false})
		return
	}
	user, err := accounts.GetUserByUUID(uuid)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"username": "Guest", "logged_in": false})
		return
	}

	// 检查密码是否使用旧版哈希（需要迁移提示）
	// bcrypt 哈希以 $2a$ 或 $2b$ 开头
	needsMigration := !strings.HasPrefix(user.Passwd, "$2a$") && !strings.HasPrefix(user.Passwd, "$2b$")

	c.JSON(http.StatusOK, gin.H{
		"username":                   user.Username,
		"logged_in":                  true,
		"uuid":                       user.UUID,
		"sso_type":                   user.SSOType,
		"sso_id":                     user.SSOID,
		"2fa_enabled":                user.TwoFactor != "",
		"password_migration_required": needsMigration,
	})
}
