package geoip

import (
	"log/slog"
	"net"
	"strings"
	"time"
	"unicode"

	"github.com/komari-monitor/komari/config"
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
			slog.Error("Failed to initialize MaxMind GeoIP service: " + err.Error())
		}
		if NewCurrentProvider != nil {
			CurrentProvider = NewCurrentProvider
		} else {
			CurrentProvider = &EmptyProvider{}
			slog.Error("Failed to initialize MaxMind GeoIP service, using EmptyProvider instead.")
		}
	case "ip-api":
		NewCurrentProvider, err := NewIPAPIService()
		if err != nil {
			slog.Error("Failed to initialize ip-api service: " + err.Error())
		}
		if NewCurrentProvider != nil {
			CurrentProvider = NewCurrentProvider
			slog.Info("Using ip-api.com as GeoIP provider.")
		} else {
			CurrentProvider = &EmptyProvider{}
			slog.Info("Failed to initialize ip-api service, using EmptyProvider instead.")
		}
	case "geojs":
		NewCurrentProvider, err := NewGeoJSService()
		if err != nil {
			slog.Error("Failed to initialize GeoJS service: " + err.Error())
		}
		if NewCurrentProvider != nil {
			CurrentProvider = NewCurrentProvider
			slog.Info("Using geojs.io as GeoIP provider.")
		} else {
			CurrentProvider = &EmptyProvider{}
			slog.Info("Failed to initialize GeoJS service, using EmptyProvider instead.")
		}
	case "ipinfo":
		NewCurrentProvider, err := NewIPInfoService()
		if err != nil {
			slog.Error("Failed to initialize IPInfo service: " + err.Error())
		}
		if NewCurrentProvider != nil {
			CurrentProvider = NewCurrentProvider
			slog.Info("Using ipinfo.io as GeoIP provider.")
		} else {
			CurrentProvider = &EmptyProvider{}
			slog.Info("Failed to initialize IPInfo service, using EmptyProvider instead.")
		}
	default:
		CurrentProvider = &EmptyProvider{}
	}
}

func GetGeoInfo(ip net.IP) (*GeoInfo, error) {
	providerName := CurrentProvider.Name()
	cacheKey := providerName + ":" + ip.String()

	if cachedInfo, found := geoCache.Get(cacheKey); found {
		//log.Println("GeoIP cache hit for", cacheKey)
		return cachedInfo.(*GeoInfo), nil
	}

	info, err := CurrentProvider.GetGeoInfo(ip)
	if err == nil && info != nil {
		//log.Println("GeoIP cache miss for", cacheKey)
		geoCache.Set(cacheKey, info, cache.DefaultExpiration)
	}
	return info, err
}

func UpdateDatabase() error {
	err := CurrentProvider.UpdateDatabase()
	if err == nil {
		geoCache.Flush()
		slog.Info("GeoIP cache cleared due to database update.")
	}
	return err
}
