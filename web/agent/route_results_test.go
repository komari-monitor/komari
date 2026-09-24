package agent

import "testing"

func TestClassifyRoute(t *testing.T) {
	tests := []struct {
		name string
		hops []string
		asns []string
		want string
	}{
		{"telecom cn2 by ASN", nil, []string{"4134", "4809"}, "CN2"},
		{"telecom cn2 by backbone hop", []string{"59.43.1.1"}, []string{"4134"}, "CN2"},
		{"ctg gia by backbone hop", []string{"59.43.1.1"}, []string{"23764", "4134"}, "CTGGIA"},
		{"ctg gia by ASN", nil, []string{"23764", "4809", "4134"}, "CTGGIA"},
		{"ctgnet alone is not proof of gia", nil, []string{"23764", "4134"}, "CTGNet"},
		{"unrelated similar address", []string{"59.44.1.1"}, []string{"4134"}, "163"},
		{"unicom premium", nil, []string{"4837", "9929"}, "9929"},
		{"mobile premium", nil, []string{"9808", "58807"}, "CMIN2"},
		{"ordinary telecom", nil, []string{"4134"}, "163"},
		{"unclassified", nil, []string{"64500"}, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			asns := make(map[string]bool)
			for _, asn := range test.asns {
				asns[asn] = true
			}
			if got := classifyRoute(test.hops, asns); got != test.want {
				t.Fatalf("classifyRoute(%v, %v) = %q, want %q", test.hops, test.asns, got, test.want)
			}
		})
	}
}
