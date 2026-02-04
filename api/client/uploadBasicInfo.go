package client

import (
	"net"

	"github.com/komari-monitor/komari/config"
	"github.com/komari-monitor/komari/database/clients"
	"github.com/komari-monitor/komari/utils/geoip"

	"github.com/gin-gonic/gin"
)

func getClientIPType(ip net.IP) int {
	// 0:ipv4 1:ipv6 -1:错误的输入
	if ip == nil {
		return -1
	}
	if ip.To4() == nil {
		return 1
	} else {
		return 0
	}
}

func UploadBasicInfo(c *gin.Context) {
	var cbi = map[string]interface{}{}
	if err := c.ShouldBindJSON(&cbi); err != nil {
		c.JSON(400, gin.H{"status": "error", "error": "Invalid or missing data"})
		return
	}

	token := c.Query("token")
	uuid, err := clients.GetClientUUIDByToken(token)
	if uuid == "" || err != nil {
		c.JSON(400, gin.H{"status": "error", "error": "Invalid token"})
		return
	}

	cbi["uuid"] = uuid

	if (func() bool {
		if v4, ok := cbi["ipv4"].(string); !ok || v4 == "" {
			if v6, ok := cbi["ipv6"].(string); !ok || v6 == "" {
				return true
			}
		}
		return false
	})() {
		ipStr := c.ClientIP()
		ip := net.ParseIP(ipStr)
		ipType := getClientIPType(ip)

		switch ipType {
		case 0:
			cbi["ipv4"] = ip
		case 1:
			cbi["ipv6"] = ip
		default:
			break
		}
	}

	if cfg, err := config.GetAs[bool](config.GeoIpEnabledKey); err == nil && cfg {
		if ipv4, ok := cbi["ipv4"].(string); ok && ipv4 != "" {
			ip4 := net.ParseIP(ipv4)
			ip4_record, _ := geoip.GetGeoInfo(ip4)
			if ip4_record != nil {
				cbi["region"] = geoip.GetRegionUnicodeEmoji(ip4_record.ISOCode)
			}
		} else if ipv6, ok := cbi["ipv6"].(string); ok && ipv6 != "" {
			ip6 := net.ParseIP(ipv6)
			ip6_record, _ := geoip.GetGeoInfo(ip6)
			if ip6_record != nil {
				cbi["region"] = geoip.GetRegionUnicodeEmoji(ip6_record.ISOCode)
			}
		}
	}

	if err := clients.SaveClientInfo(cbi); err != nil {
		c.JSON(500, gin.H{"status": "error", "error": err})
		return
	}

	c.JSON(200, gin.H{"status": "success"})
}
