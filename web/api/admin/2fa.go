package admin

import (
	"image/png"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/web/api"
	"github.com/pquerna/otp/totp"
)

func Generate2FA(c *gin.Context) {
	secret, img, err := accounts.Generate2Fa()
	if err != nil {
		api.RespondError(c, 500, "Failed to generate 2FA: "+err.Error())
		return
	}
	c.SetCookie("2fa_secret", secret, 1800, "/", "", false, true)
	c.Header("Content-Type", "image/png")
	c.Writer.WriteHeader(200)
	png.Encode(c.Writer, img)
}

func Enable2FA(c *gin.Context) {
	uuid, _ := c.Get("uuid")
	secret, _ := c.Cookie("2fa_secret")
	code := c.Query("code")
	if secret == "" || uuid == nil || code == "" {
		api.RespondError(c, 400, "2FA secret or code not provided")
		return
	}
	if !totp.Validate(code, secret) {
		api.RespondError(c, 400, "Invalid 2FA code")
		return
	}
	err := accounts.Enable2Fa(uuid.(string), secret)
	if err != nil {
		api.RespondError(c, 500, "Failed to enable 2FA: "+err.Error())
		return
	}
	c.SetCookie("2fa_secret", "", -1, "/", "", false, true)

	api.RespondSuccess(c, "2FA enabled successfully")
}

func Disable2FA(c *gin.Context) {
	uuid, _ := c.Get("uuid")
	err := accounts.Disable2Fa(uuid.(string))
	if err != nil {
		api.RespondError(c, 500, "Failed to disable 2FA: "+err.Error())
		return
	}
	api.RespondSuccess(c, "")
}
