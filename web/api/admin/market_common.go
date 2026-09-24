package admin

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"time"

	"github.com/komari-monitor/komari/database/models"
)

const (
	marketCatalogMaxSize = 2 << 20
	marketPackageMaxSize = 100 << 20
	marketCacheTTL       = 10 * time.Minute
)

func newMarketSourceID() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func isMarketText(value any) bool {
	return models.IsLocalizedText(value)
}

func isValidMarketShort(short string) bool {
	if short == "" || short == "default" {
		return false
	}
	for _, r := range short {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func validateMarketURLSyntax(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil {
		return errors.New("must be a valid HTTP or HTTPS URL")
	}
	return nil
}
