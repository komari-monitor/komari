package admin

import (
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/utils/geoip"
	"github.com/komari-monitor/komari/web/api"
)

// update.go
// 文件/二进制/敏感操作类的更新接口，保留为 REST handler（不走 RPC 桥）。
// 由原 web/api/admin/update/ 子包合并而来。

func UpdateUser(c *gin.Context) {
	var req struct {
		Uuid     string  `json:"uuid" binding:"required"`
		Name     *string `json:"username"`
		Password *string `json:"password"`
		SsoType  *string `json:"sso_type"`
		TwoFa    string  `json:"2fa_code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.RespondError(c, 400, "Invalid or missing request body: "+err.Error())
		return
	}
	if req.Password == nil && req.Name == nil {
		api.RespondError(c, 400, "At least one field (username or password) must be provided")
		return
	}
	if req.Name != nil && len(*req.Name) < 3 {
		api.RespondError(c, 400, "Username must be at least 3 characters long")
		return
	}
	if req.Password != nil && len(*req.Password) < 6 {
		api.RespondError(c, 400, "Password must be at least 6 characters long")
		return
	}
	if req.Password != nil {
		c.Set("2fa_code", req.TwoFa)
		if err := api.VerifySensitive2FA(c); err != nil {
			api.RespondError(c, 401, err.Error())
			return
		}
	}
	if err := accounts.UpdateUser(req.Uuid, req.Name, req.Password, req.SsoType); err != nil {
		api.RespondError(c, 500, "Failed to update user: "+err.Error())
		return
	}
	uuid, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), uuid.(string), "User updated", "warn")
	api.RespondSuccess(c, gin.H{"uuid": req.Uuid})
}

func UpdateMmdbGeoIP(c *gin.Context) {
	if err := geoip.UpdateDatabase(); err != nil {
		api.RespondError(c, 500, "Failed to update GeoIP database "+err.Error())
		return
	}
	uuid, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), uuid.(string), "GeoIP database updated", "info")
	api.RespondSuccess(c, nil)
}

func UploadFavicon(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 5<<20) // 5MB
	data, err := io.ReadAll(c.Request.Body)
	if err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			api.RespondError(c, http.StatusRequestEntityTooLarge, "File too large. Maximum size is 5MB")
		} else {
			api.RespondError(c, http.StatusBadRequest, err.Error())
		}
		return
	}
	if err := os.WriteFile("./data/favicon.ico", data, 0644); err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to save favicon: "+err.Error())
		return
	}
	uuid, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), uuid.(string), "Favicon uploaded", "info")
	api.RespondSuccess(c, nil)
}

func DeleteFavicon(c *gin.Context) {
	if err := os.Remove("./data/favicon.ico"); err != nil {
		if os.IsNotExist(err) {
			api.RespondError(c, http.StatusNotFound, "Favicon not found")
		} else {
			api.RespondError(c, http.StatusInternalServerError, "Failed to delete favicon: "+err.Error())
		}
		return
	}
	uuid, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), uuid.(string), "Favicon deleted", "info")
	api.RespondSuccess(c, nil)
}
