package geoip

import (
	"net"
	"strings"
	"time"
	"unicode"

	"github.com/komari-monitor/komari/pkg/config"
	logger "github.com/komari-monitor/komari/utils/log"
	"github.com/patrickmn/go-cache"
)

var CurrentProvider GeoIPService
var geoCache *cache.Cache

type GeoInfo struct {
	ISOCode string
	Name    string
}

func init() {
	CurrentProvider = &EmptyProvider{}
	geoCache = cache.New(48*time.Hour, 1*time.Hour)
}

// GeoIPService 接口定义了获取地理位置信息的核心方法。
// 任何实现此接口的类型都可以作为地理位置服务提供者。
type GeoIPService interface {
	Name() string

	GetGeoInfo(ip net.IP) (*GeoInfo, error)

	UpdateDatabase() error

	Close() error
}

func GetRegionUnicodeEmoji(isoCode string) string {
	if len(isoCode) != 2 {
		return ""
	}
	isoCode = strings.ToUpper(isoCode)

	if !unicode.IsLetter(rune(isoCode[0])) || !unicode.IsLetter(rune(isoCode[1])) {
		return ""
	}

	rune1 := rune(0x1F1E6 + (rune(isoCode[0]) - 'A'))
	rune2 := rune(0x1F1E6 + (rune(isoCode[1]) - 'A'))
	return string(rune1) + string(rune2)
}

func InitGeoIp() {
	conf, err := config.GetMany(map[string]any{
		config.GeoIpEnabledKey:  true,
		config.GeoIpProviderKey: "ipinfo",
	})
	if err != nil {
		panic("Failed to get configuration for GeoIP: " + err.Error())
	}
	if !conf[config.GeoIpEnabledKey].(bool) {
		return
	}
	switch conf[config.GeoIpProviderKey].(string) {
	case "mmdb":
		NewCurrentProvider, err := NewMaxMindGeoIPService()
		if err != nil {
			logger.Error("geoip", "failed to initialize MaxMind GeoIP service", "error", err)
		}
		if NewCurrentProvider != nil {
			CurrentProvider = NewCurrentProvider
		} else {
			CurrentProvider = &EmptyProvider{}
			logger.Error("geoip", "failed to initialize MaxMind GeoIP service; using EmptyProvider")
		}
	case "ip-api":
		NewCurrentProvider, err := NewIPAPIService()
		if err != nil {
			logger.Error("geoip", "failed to initialize ip-api service", "error", err)
		}
		if NewCurrentProvider != nil {
			CurrentProvider = NewCurrentProvider
			logger.Info("geoip", "using GeoIP provider", "provider", "ip-api.com")
		} else {
			CurrentProvider = &EmptyProvider{}
			logger.Warn("geoip", "failed to initialize ip-api service; using EmptyProvider")
		}
	case "geojs":
		NewCurrentProvider, err := NewGeoJSService()
		if err != nil {
			logger.Error("geoip", "failed to initialize GeoJS service", "error", err)
		}
		if NewCurrentProvider != nil {
			CurrentProvider = NewCurrentProvider
			logger.Info("geoip", "using GeoIP provider", "provider", "geojs.io")
		} else {
			CurrentProvider = &EmptyProvider{}
			logger.Warn("geoip", "failed to initialize GeoJS service; using EmptyProvider")
		}
	case "ipinfo":
		NewCurrentProvider, err := NewIPInfoService()
		if err != nil {
			logger.Error("geoip", "failed to initialize IPInfo service", "error", err)
		}
		if NewCurrentProvider != nil {
			CurrentProvider = NewCurrentProvider
			logger.Info("geoip", "using GeoIP provider", "provider", "ipinfo.io")
		} else {
			CurrentProvider = &EmptyProvider{}
			logger.Warn("geoip", "failed to initialize IPInfo service; using EmptyProvider")
		}
	default:
		CurrentProvider = &EmptyProvider{}
	}
}

// Shutdown 关闭当前 GeoIP provider 持有的资源（如 mmdb 文件句柄）。供关闭流程调用。
func Shutdown() error {
	if CurrentProvider == nil {
		return nil
	}
	return CurrentProvider.Close()
}

func GetGeoInfo(ip net.IP) (*GeoInfo, error) {
	providerName := CurrentProvider.Name()
	cacheKey := providerName + ":" + ip.String()

	if cachedInfo, found := geoCache.Get(cacheKey); found {
		return cachedInfo.(*GeoInfo), nil
	}

	info, err := CurrentProvider.GetGeoInfo(ip)
	if err == nil && info != nil {
		geoCache.Set(cacheKey, info, cache.DefaultExpiration)
	}
	return info, err
}

func UpdateDatabase() error {
	err := CurrentProvider.UpdateDatabase()
	if err == nil {
		geoCache.Flush()
		logger.Info("geoip", "cache cleared due to database update")
	}
	return err
}
