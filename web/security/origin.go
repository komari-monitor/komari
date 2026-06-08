package security

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/komari-monitor/komari/pkg/config"
)

func SplitAllowlist(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	})
	entries := make([]string, 0, len(parts))
	for _, part := range parts {
		entry := strings.TrimSpace(part)
		if entry != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}

func HasAllowlist(raw string) bool {
	return len(SplitAllowlist(raw)) > 0
}

func OriginMatchesHost(origin, host string) bool {
	_, originHost, ok := normalizeOrigin(origin)
	return ok && strings.EqualFold(originHost, host)
}

func OriginInAllowlist(origin, rawAllowlist string) bool {
	normalizedOrigin, originHost, ok := normalizeOrigin(origin)
	if !ok {
		return false
	}
	for _, entry := range SplitAllowlist(rawAllowlist) {
		if entry == "*" {
			return true
		}
		if strings.Contains(entry, "://") {
			normalizedEntry, _, ok := normalizeOrigin(entry)
			if ok && strings.EqualFold(normalizedEntry, normalizedOrigin) {
				return true
			}
			continue
		}
		if strings.EqualFold(entry, originHost) {
			return true
		}
	}
	return false
}

func IsAPIKeyRequest(r *http.Request) bool {
	apiKeyConfig, err := config.GetAs[string](config.ApiKeyKey, "")
	if err != nil || apiKeyConfig == "" || len(apiKeyConfig) < 12 {
		return false
	}
	return r.Header.Get("Authorization") == "Bearer "+apiKeyConfig
}

func IsAuthorizationPreflight(r *http.Request) bool {
	if r.Method != http.MethodOptions {
		return false
	}
	for _, header := range strings.Split(r.Header.Get("Access-Control-Request-Headers"), ",") {
		if strings.EqualFold(strings.TrimSpace(header), "authorization") {
			return true
		}
	}
	return false
}

func normalizeOrigin(raw string) (string, string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", "", false
	}
	host := strings.ToLower(parsed.Host)
	return strings.ToLower(parsed.Scheme) + "://" + host, host, true
}
