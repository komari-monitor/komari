package ws

import (
	"net/http"
	"net/url"
	"os"
	"strings"
)

func CheckOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	// 显式关闭校验
	if strings.EqualFold(os.Getenv("KOMARI_WS_DISABLE_ORIGIN"), "true") {
		return true
	}
	if origin == "" {
		return false
	}
	host := r.Host
	originUrl, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return originUrl.Host == host
}
