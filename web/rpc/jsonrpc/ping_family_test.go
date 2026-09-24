package jsonrpc

import (
	"testing"

	"github.com/komari-monitor/komari/database/models"
)

func TestValidatePingTaskTargets(t *testing.T) {
	tests := []struct {
		name, kind, ipv4, ipv6 string
		families               models.StringArray
		valid                  bool
	}{
		{"legacy IPv4", "tcp", "example.com:443", "", nil, true},
		{"dual TCP", "tcp", "v4.example:443", "v6.example:443", models.StringArray{"ipv4", "ipv6"}, true},
		{"IPv6-only ICMP", "icmp", "", "2001:db8::1", models.StringArray{"ipv6"}, true},
		{"missing IPv6 target", "tcp", "v4.example", "", models.StringArray{"ipv4", "ipv6"}, false},
		{"HTTP IPv6 unsupported", "http", "", "v6.example", models.StringArray{"ipv6"}, false},
		{"duplicate family", "tcp", "v4.example", "", models.StringArray{"ipv4", "ipv4"}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validatePingTaskTargets(test.kind, test.ipv4, test.ipv6, test.families)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v, err=%v", test.valid, err)
			}
		})
	}
}
