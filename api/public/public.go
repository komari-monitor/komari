package public

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/api"
	"github.com/komari-monitor/komari/config"
	"github.com/komari-monitor/komari/database"
)

func GetPublicSettings(c *gin.Context) {
	p, e := database.GetPublicInfo()
	if e != nil {
		api.RespondError(c, 500, e.Error())
		return
	}
	// 临时访问许可
	if func() bool {
		tempKey, err := c.Cookie("temp_key")
		if err != nil {
			return false
		}

		tempKeyExpireTime, err := config.GetAs[int64]("tempory_share_token_expire_at", 0)
		if err != nil {
			return false
		}
		allowTempKey, err := config.GetAs[string]("tempory_share_token", "")
		if err != nil {
			return false
		}

		if allowTempKey == "" || tempKey != allowTempKey {
			return false
		}
		now := time.Now().Unix()
		if tempKeyExpireTime < now {
			return false
		}

		return true
	}() {
		p["private_site"] = false
	}
	api.RespondSuccess(c, p)
}
