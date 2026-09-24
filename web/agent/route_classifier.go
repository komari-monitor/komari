package agent

import (
	"net"
	"strconv"
	"strings"
)

// routeHop preserves the original TTL, including gaps caused by hidden hops.
type routeHop struct {
	address net.IP
	asns    []string
	country string
	ttl     int
}

func parseRouteHops(hops []string) []routeHop {
	seen := make(map[string]bool)
	var ordered []routeHop
	for index, hop := range hops {
		ip := net.ParseIP(hop)
		if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() {
			continue
		}
		if !seen[ip.String()] {
			seen[ip.String()] = true
			ordered = append(ordered, routeHop{address: ip, ttl: index + 1})
		}
	}
	if len(ordered) > 20 {
		ordered = ordered[len(ordered)-20:]
	}
	return ordered
}

func hasASN(hop routeHop, asn string) bool {
	for _, candidate := range hop.asns {
		if candidate == asn {
			return true
		}
	}
	return false
}

func inIPv4Prefix(hop routeHop, first, second byte) bool {
	ip := hop.address.To4()
	return ip != nil && ip[0] == first && ip[1] == second
}

func hasASNIn(hops []routeHop, asn string) bool {
	for _, hop := range hops {
		if hasASN(hop, asn) {
			return true
		}
	}
	return false
}

// domesticNetwork identifies a visible Chinese backbone, not an overseas
// international gateway such as CTGNet or CMI.
func domesticNetwork(hop routeHop) string {
	switch {
	case inIPv4Prefix(hop, 59, 43) || hasASN(hop, "4809"):
		return "CN2"
	case inIPv4Prefix(hop, 202, 97) || hasASN(hop, "4134"):
		return "163"
	case hasASN(hop, "9929"):
		return "9929"
	case hasASN(hop, "4837"):
		return "4837"
	case hasASN(hop, "4808"):
		return "4808"
	case hasASN(hop, "58807"):
		return "CMIN2"
	case hasASN(hop, "9808"):
		return "CMNET"
	case hasASN(hop, "4538"):
		return "CERNET"
	case hasASN(hop, "7497"):
		return "CSTNET"
	default:
		return ""
	}
}

func classifyRoute(hops []routeHop) string {
	// NetQuality's CN2 distinction depends on the first Chinese hop, not
	// whether a 202.97 hop appears anywhere near the destination.
	firstChina := -1
	for index, hop := range hops {
		if hop.country == "CN" {
			firstChina = index
			break
		}
	}
	if firstChina < 0 && hasCN2Hop(hops) {
		return "CN2" // No confirmed mainland entry; do not infer GIA or GT.
	}
	// When the local database is unavailable, retain the previous generic
	// backbone detection for other providers, but do not claim a CN2 tier.
	entryIndex := -1
	if firstChina >= 0 {
		entryIndex = firstChina
	} else {
		for index, hop := range hops {
			if domesticNetwork(hop) != "" {
				entryIndex = index
				break
			}
		}
	}
	if entryIndex >= 0 {
		index := entryIndex
		entry := domesticNetwork(hops[index])
		switch entry {
		case "CN2":
			if routeHopTTL(hops[index], index) > 1 {
				if hasASNIn(hops, "23764") {
					return "CTGGIA"
				}
				return "CN2GIA"
			}
			for _, later := range hops[index+1:] {
				if domesticNetwork(later) == "CN2" || hasASN(later, "23764") {
					continue
				}
				if inIPv4Prefix(later, 202, 97) {
					return "CN2GT"
				}
				break
			}
			return "CN2GIA" // A path label, not proof of a purchased service tier.
		case "4837":
			if hasASNIn(hops[:index], "10099") {
				return "10099"
			}
		case "CMNET":
			if hasASNIn(hops[:index], "58453") {
				return "CMI"
			}
		}
		if entry == "" && hasCN2Hop(hops) {
			return "CN2"
		}
		return entry
	}
	// A visible international gateway is useful even when mainland hops do
	// not respond; it is not evidence of a particular premium tier.
	switch {
	case hasASNIn(hops, "23764"):
		return "CTGNet"
	case hasASNIn(hops, "10099"):
		return "10099"
	case hasASNIn(hops, "58453"):
		return "CMI"
	default:
		return ""
	}
}

func hasCN2Hop(hops []routeHop) bool {
	for _, hop := range hops {
		if inIPv4Prefix(hop, 59, 43) || hasASN(hop, "4809") {
			return true
		}
	}
	return false
}

func routeHopTTL(hop routeHop, index int) int {
	if hop.ttl > 0 {
		return hop.ttl
	}
	return index + 1 // Synthetic tests and older in-process callers.
}

func cymruDNSName(ip net.IP) string {
	if v4 := ip.To4(); v4 != nil {
		return strings.Join([]string{
			strconv.Itoa(int(v4[3])), strconv.Itoa(int(v4[2])),
			strconv.Itoa(int(v4[1])), strconv.Itoa(int(v4[0])),
		}, ".") + ".origin.asn.cymru.com"
	}
	var nibbles []string
	for index := len(ip.To16()) - 1; index >= 0; index-- {
		value := ip[index]
		nibbles = append(nibbles, strconv.FormatUint(uint64(value&15), 16), strconv.FormatUint(uint64(value>>4), 16))
	}
	return strings.Join(nibbles, ".") + ".origin6.asn.cymru.com"
}
