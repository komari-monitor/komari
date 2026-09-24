package agent

import (
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RouteResult exposes only a classification, not the internal hop addresses.
type RouteResult struct {
	UUID      string    `json:"uuid"`
	TaskID    uint      `json:"task_id"`
	Family    string    `json:"family"`
	Label     string    `json:"label"`
	Status    string    `json:"status"`
	CheckedAt time.Time `json:"checked_at"`
}

var routeResults = struct {
	sync.RWMutex
	items map[string]RouteResult
}{items: make(map[string]RouteResult)}

type asnCacheEntry struct {
	asns      []string
	expiresAt time.Time
}

// routeHop keeps the trace order: an ASN seen at the destination does not
// necessarily describe the network used to enter mainland China.
type routeHop struct {
	address net.IP
	asns    []string
}

var routeASNCache = struct {
	sync.RWMutex
	items map[string]asnCacheEntry
}{items: make(map[string]asnCacheEntry)}

func routeResultKey(uuid string, taskID uint, family string) string {
	return uuid + ":" + stringID(taskID) + ":" + family
}

func stringID(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}

// RecordRouteResult resolves visible public hops to origin ASNs in the background.
func RecordRouteResult(uuid string, taskID uint, family string, hops []string, traceError string) {
	if family == "" {
		family = "ipv4" // Reports from older forked agents.
	}
	result := RouteResult{UUID: uuid, TaskID: taskID, Family: family, Status: "unknown", CheckedAt: time.Now().UTC()}
	if len(hops) > 28 {
		hops = hops[:28]
	}
	result.Label = classifyRoute(lookupRouteHops(hops))
	if result.Label != "" {
		result.Status = "ok"
	} else if traceError != "" {
		result.Status = "error"
		if family == "ipv6" && traceError == "no IPv6 target address" {
			result.Label = "NO_IPV6"
		}
	}
	routeResults.Lock()
	routeResults.items[routeResultKey(uuid, taskID, family)] = result
	routeResults.Unlock()
}

func ListRouteResults() []RouteResult {
	routeResults.RLock()
	defer routeResults.RUnlock()
	out := make([]RouteResult, 0, len(routeResults.items))
	for _, result := range routeResults.items {
		out = append(out, result)
	}
	return out
}

func lookupRouteHops(hops []string) []routeHop {
	seen := make(map[string]bool)
	var ordered []routeHop
	for _, hop := range hops {
		ip := net.ParseIP(hop)
		if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() {
			continue
		}
		if !seen[ip.String()] {
			seen[ip.String()] = true
			ordered = append(ordered, routeHop{address: ip})
		}
	}
	if len(ordered) > 20 {
		ordered = ordered[len(ordered)-20:]
	}
	var wg sync.WaitGroup
	limit := make(chan struct{}, 6)
	for index := range ordered {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			limit <- struct{}{}
			defer func() { <-limit }()
			ordered[index].asns = lookupOriginASNs(ordered[index].address.String())
		}(index)
	}
	wg.Wait()
	return ordered
}

func lookupOriginASNs(address string) []string {
	routeASNCache.RLock()
	cached, ok := routeASNCache.items[address]
	routeASNCache.RUnlock()
	if ok && time.Now().Before(cached.expiresAt) {
		return cached.asns
	}
	ip := net.ParseIP(address)
	if ip == nil {
		return nil
	}
	name := cymruDNSName(ip)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	answers, err := net.DefaultResolver.LookupTXT(ctx, name)
	var asns []string
	if err == nil {
		for _, answer := range answers {
			parts := strings.SplitN(answer, "|", 2)
			for _, candidate := range strings.Fields(parts[0]) {
				if number, err := strconv.ParseUint(candidate, 10, 32); err == nil && number != 0 {
					asns = append(asns, candidate)
				}
			}
		}
	}
	ttl := 12 * time.Hour
	if len(asns) == 0 {
		ttl = 10 * time.Minute // A transient DNS failure must not hide a route all day.
	}
	routeASNCache.Lock()
	routeASNCache.items[address] = asnCacheEntry{asns: asns, expiresAt: time.Now().Add(ttl)}
	routeASNCache.Unlock()
	return asns
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

// domesticNetwork identifies the first visible Chinese backbone, not a
// potentially overseas international gateway such as CTGNet or CMI.
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
	// These two backbone prefixes are stronger evidence of the mainland path
	// than an ASN that may also appear on an overseas-facing router.
	entryIndex := -1
	for index, hop := range hops {
		if inIPv4Prefix(hop, 59, 43) || inIPv4Prefix(hop, 202, 97) {
			entryIndex = index
			break
		}
	}
	if entryIndex < 0 {
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
			if hasASNIn(hops[:index], "23764") {
				return "CTGGIA"
			}
			for _, later := range hops[index+1:] {
				if domesticNetwork(later) == "CN2" {
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
		return entry
	}
	// A visible international gateway is useful even when the mainland hops
	// do not respond; it is not evidence of a particular premium tier.
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
