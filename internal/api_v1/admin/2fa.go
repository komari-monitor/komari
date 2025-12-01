package admin

import (
	"image/png"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/internal/api_v1/resp"
	"github.com/komari-monitor/komari/internal/database/accounts"
	"github.com/pquerna/otp/totp"
)

func Generate2FA(c *gin.Context) {
	secret, img, err := accounts.Generate2Fa()
	if err != nil {
		resp.RespondError(c, 500, "Failed to generate 2FA: "+err.Error())
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
		resp.RespondError(c, 400, "2FA secret or code not provided")
		return
	}
	if !totp.Validate(code, secret) {
		resp.RespondError(c, 400, "Invalid 2FA code")
		return
	}
	err := accounts.Enable2Fa(uuid.(string), secret)
	if err != nil {
		resp.RespondError(c, 500, "Failed to enable 2FA: "+err.Error())
		return
	}
	c.SetCookie("2fa_secret", "", -1, "/", "", false, true)

	resp.RespondSuccess(c, "2FA enabled successfully")
}

func Disable2FA(c *gin.Context) {
	uuid, _ := c.Get("uuid")
	err := accounts.Disable2Fa(uuid.(string))
	if err != nil {
		resp.RespondError(c, 500, "Failed to disable 2FA: "+err.Error())
		return
	}
	resp.RespondSuccess(c, "")
}
