package api_v1

import (
	"github.com/komari-monitor/komari/internal/database/accounts"

	"github.com/gin-gonic/gin"
)

func GetMe(c *gin.Context) {
	session, err := c.Cookie("session_token")
	if err != nil {
		c.JSON(200, gin.H{"username": "Guest", "logged_in": false})
		return
	}
	uuid, err := accounts.GetSession(session)
	if err != nil {
		c.JSON(200, gin.H{"username": "Guest", "logged_in": false})
		return
	}
	user, err := accounts.GetUserByUUID(uuid)
	if err != nil {
		c.JSON(200, gin.H{"username": "Guest", "logged_in": false})
		return
	}
	c.JSON(200, gin.H{"username": user.Username, "logged_in": true, "uuid": user.UUID, "sso_type": user.SSOType, "sso_id": user.SSOID, "2fa_enabled": user.TwoFactor != ""})

}
