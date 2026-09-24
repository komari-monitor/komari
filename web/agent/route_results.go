package agent

import (
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/komari-monitor/komari/utils/geoip"
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
	ordered := parseRouteHops(hops)
	var wg sync.WaitGroup
	limit := make(chan struct{}, 6)
	for index := range ordered {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			limit <- struct{}{}
			defer func() { <-limit }()
			ordered[index].asns = lookupOriginASNs(ordered[index].address.String())
			ordered[index].country = geoip.LookupRouteCountry(ordered[index].address)
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
