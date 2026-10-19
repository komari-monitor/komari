package geoip_test

import (
	"testing"

	"github.com/komari-monitor/komari/utils/geoip"
)

func TestUnicodeEmoji(t *testing.T) {
	if emoji := geoip.GetRegionUnicodeEmoji("CN"); emoji != "🇨🇳" {
		t.Errorf("Expected emoji for CN, got %s", emoji)
	}
}
