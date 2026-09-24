package agent

import "testing"

func TestClassifyRouteASNs(t *testing.T) {
	tests := []struct {
		name string
		asns []string
		want string
	}{
		{"telecom cn2", []string{"4134", "4809"}, "CN2"},
		{"unicom premium", []string{"4837", "9929"}, "9929"},
		{"mobile premium", []string{"9808", "58807"}, "CMIN2"},
		{"ordinary telecom", []string{"4134"}, "163"},
		{"unclassified", []string{"64500"}, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			asns := make(map[string]bool)
			for _, asn := range test.asns {
				asns[asn] = true
			}
			if got := classifyRouteASNs(asns); got != test.want {
				t.Fatalf("classifyRouteASNs(%v) = %q, want %q", test.asns, got, test.want)
			}
		})
	}
}
