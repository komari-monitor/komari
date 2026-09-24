package jsonrpc

import (
	"testing"

	"github.com/komari-monitor/komari/internal/config"
)

func TestValidateRouteTraceSettingChanges(t *testing.T) {
	cases := []struct {
		name  string
		cfg   map[string]interface{}
		valid bool
	}{
		{"defaults", map[string]interface{}{config.RouteTraceTargetKey: "", config.RouteTraceIntervalHoursKey: float64(6)}, true},
		{"trim target", map[string]interface{}{config.RouteTraceTargetKey: " example.com:443 "}, true},
		{"minimum interval", map[string]interface{}{config.RouteTraceIntervalHoursKey: float64(1)}, true},
		{"fraction", map[string]interface{}{config.RouteTraceIntervalHoursKey: 1.5}, false},
		{"zero interval", map[string]interface{}{config.RouteTraceIntervalHoursKey: float64(0)}, false},
		{"oversize interval", map[string]interface{}{config.RouteTraceIntervalHoursKey: float64(169)}, false},
		{"url target", map[string]interface{}{config.RouteTraceTargetKey: "https://example.com"}, false},
		{"space target", map[string]interface{}{config.RouteTraceTargetKey: "example .com"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRouteTraceSettingChanges(tc.cfg)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}
