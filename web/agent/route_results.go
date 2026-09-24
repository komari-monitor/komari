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

func routeResultKey(uuid string, taskID uint) string {
	return uuid + ":" + stringID(taskID)
}

func stringID(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}

// RecordRouteResult resolves visible public hops to origin ASNs in the background.
func RecordRouteResult(uuid string, taskID uint, hops []string, traceError string) {
	result := RouteResult{UUID: uuid, TaskID: taskID, Status: "unknown", CheckedAt: time.Now().UTC()}
	if len(hops) > 28 {
		hops = hops[:28]
	}
	asns := lookupRouteASNs(hops)
	result.Label = classifyRoute(hops, asns)
	if result.Label != "" {
		result.Status = "ok"
	} else if traceError != "" {
		result.Status = "error"
	}
	routeResults.Lock()
	routeResults.items[routeResultKey(uuid, taskID)] = result
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

func lookupRouteASNs(hops []string) map[string]bool {
	seen := make(map[string]bool)
	var addresses []string
	for _, hop := range hops {
		ip := net.ParseIP(hop)
		if ip == nil || ip.To4() == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() {
			continue
		}
		if !seen[ip.String()] {
			seen[ip.String()] = true
			addresses = append(addresses, ip.String())
		}
	}
	if len(addresses) > 20 {
		addresses = addresses[len(addresses)-20:]
	}
	results := make(map[string]bool)
	var mu sync.Mutex
	var wg sync.WaitGroup
	limit := make(chan struct{}, 6)
	for _, address := range addresses {
		wg.Add(1)
		go func(address string) {
			defer wg.Done()
			limit <- struct{}{}
			defer func() { <-limit }()
			for _, asn := range lookupOriginASNs(address) {
				mu.Lock()
				results[asn] = true
				mu.Unlock()
			}
		}(address)
	}
	wg.Wait()
	return results
}

func lookupOriginASNs(address string) []string {
	routeASNCache.RLock()
	cached, ok := routeASNCache.items[address]
	routeASNCache.RUnlock()
	if ok && time.Now().Before(cached.expiresAt) {
		return cached.asns
	}
	ip := net.ParseIP(address).To4()
	if ip == nil {
		return nil
	}
	name := strings.Join([]string{
		strconv.Itoa(int(ip[3])), strconv.Itoa(int(ip[2])),
		strconv.Itoa(int(ip[1])), strconv.Itoa(int(ip[0])),
	}, ".") + ".origin.asn.cymru.com"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	answers, err := net.DefaultResolver.LookupTXT(ctx, name)
	var asns []string
	if err == nil {
		for _, answer := range answers {
			parts := strings.SplitN(answer, "|", 2)
			for _, candidate := range strings.Fields(parts[0]) {
				if _, err := strconv.ParseUint(candidate, 10, 32); err == nil {
					asns = append(asns, candidate)
				}
			}
		}
	}
	routeASNCache.Lock()
	routeASNCache.items[address] = asnCacheEntry{asns: asns, expiresAt: time.Now().Add(12 * time.Hour)}
	routeASNCache.Unlock()
	return asns
}

func classifyRoute(hops []string, asns map[string]bool) string {
	cn2Backbone := false
	for _, hop := range hops {
		ip := net.ParseIP(hop).To4()
		if ip != nil && ip[0] == 59 && ip[1] == 43 {
			cn2Backbone = true
			break
		}
	}
	cn2 := cn2Backbone || asns["4809"]
	switch {
	// CTGNet alone also carries ordinary transit; require evidence of CN2.
	case asns["23764"] && cn2:
		return "CTGGIA"
	case cn2:
		return "CN2"
	case asns["9929"]:
		return "9929"
	case asns["58807"]:
		return "CMIN2"
	case asns["23764"]:
		return "CTGNet"
	case asns["4134"]:
		return "163"
	case asns["4837"]:
		return "4837"
	case asns["58453"]:
		return "CMI"
	case asns["9808"]:
		return "CMNET"
	default:
		return ""
	}
}
