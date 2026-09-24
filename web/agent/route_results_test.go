package agent

import (
	"net"
	"strings"
	"testing"
)

func TestClassifyRoute(t *testing.T) {
	hop := func(address string, asns ...string) routeHop {
		country := ""
		if strings.HasPrefix(address, "59.43.") || strings.HasPrefix(address, "202.97.") {
			country = "CN"
		}
		return routeHop{address: net.ParseIP(address), asns: asns, country: country}
	}
	tests := []struct {
		name string
		hops []routeHop
		want string
	}{
		{"CN2 GIA after overseas gateway", []routeHop{hop("1.1.1.1", "23764"), hop("59.43.1.1", "4809")}, "CTGGIA"},
		{"CN2 GIA without CTG gateway", []routeHop{hop("59.43.1.1", "4809")}, "CN2GIA"},
		{"CN2 GT switches to 163", []routeHop{hop("59.43.1.1", "4809"), hop("202.97.1.1", "4134")}, "CN2GT"},
		{"CN2 GIA may later cross 163 near destination", []routeHop{hop("1.1.1.1", "64500"), hop("59.43.1.1", "4809"), hop("202.97.1.1", "4134")}, "CN2GIA"},
		{"late CTGNet still identifies CTG GIA", []routeHop{hop("1.1.1.1", "64500"), hop("59.43.1.1", "4809"), hop("2.2.2.2", "23764"), hop("202.97.1.1", "4134")}, "CTGGIA"},
		{"CN2 GT skips CTG gateway at first China hop", []routeHop{hop("59.43.1.1", "4809"), hop("2.2.2.2", "23764"), hop("202.97.1.1", "4134")}, "CN2GT"},
		{"CN2 without confirmed China entry", []routeHop{hop("1.1.1.1", "4809"), hop("2.2.2.2", "4134")}, "CN2"},
		{"CN2 after unrecognized first China hop", []routeHop{{address: net.ParseIP("1.1.1.1"), country: "CN", asns: []string{"64500"}}, hop("59.43.1.1", "4809")}, "CN2"},
		{"first backbone matters", []routeHop{hop("202.97.1.1", "4134"), hop("59.43.1.1", "4809")}, "163"},
		{"CN2 prefix beats earlier generic ASN", []routeHop{hop("1.1.1.1", "4134"), hop("59.43.1.1", "4809")}, "CN2GIA"},
		{"163 by ASN", []routeHop{hop("1.1.1.1", "4134")}, "163"},
		{"CTGNet alone is not GIA", []routeHop{hop("1.1.1.1", "23764")}, "CTGNet"},
		{"Unicom 9929", []routeHop{hop("1.1.1.1", "9929"), hop("2.2.2.2", "4837")}, "9929"},
		{"Unicom 10099 gateway", []routeHop{hop("1.1.1.1", "10099"), hop("2.2.2.2", "4837")}, "10099"},
		{"Unicom 4837", []routeHop{hop("1.1.1.1", "4837")}, "4837"},
		{"Unicom 4808", []routeHop{hop("1.1.1.1", "4808")}, "4808"},
		{"Mobile CMIN2", []routeHop{hop("1.1.1.1", "58807"), hop("2.2.2.2", "9808")}, "CMIN2"},
		{"Mobile CMI", []routeHop{hop("1.1.1.1", "58453"), hop("2.2.2.2", "9808")}, "CMI"},
		{"Mobile CMNET", []routeHop{hop("1.1.1.1", "9808")}, "CMNET"},
		{"CERNET", []routeHop{hop("1.1.1.1", "4538")}, "CERNET"},
		{"CSTNET", []routeHop{hop("1.1.1.1", "7497")}, "CSTNET"},
		{"IPv6 backbone", []routeHop{hop("2001:db8::1", "9929")}, "9929"},
		{"unknown", []routeHop{hop("1.1.1.1", "64500")}, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyRoute(test.hops); got != test.want {
				t.Fatalf("classifyRoute(%v) = %q, want %q", test.hops, got, test.want)
			}
		})
	}
}

func TestParseRouteHopsKeepsOriginalTTL(t *testing.T) {
	hops := parseRouteHops([]string{"", "59.43.1.1", "", "202.97.1.1", "59.43.1.1"})
	if len(hops) != 2 || hops[0].ttl != 2 || hops[1].ttl != 4 {
		t.Fatalf("original TTL positions were lost: %+v", hops)
	}
}

func TestCymruDNSName(t *testing.T) {
	if got := cymruDNSName(net.ParseIP("216.90.108.31")); got != "31.108.90.216.origin.asn.cymru.com" {
		t.Fatalf("IPv4 DNS name = %q", got)
	}
	got := cymruDNSName(net.ParseIP("2001:db8::1"))
	if !strings.HasPrefix(got, "1.0.0.0.0.0.0.0.") || !strings.HasSuffix(got, ".2.origin6.asn.cymru.com") {
		t.Fatalf("IPv6 DNS name = %q", got)
	}
}
