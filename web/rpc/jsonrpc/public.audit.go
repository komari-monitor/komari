package jsonrpc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/pkg/config"
	"github.com/komari-monitor/komari/pkg/rpc"
)

const (
	visitorAuditMaxEventLen     = 64
	visitorAuditMaxPathLen      = 512
	visitorAuditMaxRouteLen     = 128
	visitorAuditMaxTargetLen    = 128
	visitorAuditMaxUserAgentLen = 512
	visitorAuditMaxDetailLen    = 2048
	visitorAuditMaxMessageLen   = 4096
	visitorAuditRatePerMinute   = 30
	visitorAuditRateBurst       = 10
	visitorAuditRateMaxEntries  = 10000
	visitorAuditLimiterEntryTTL = 10 * time.Minute
	visitorAuditCleanupInterval = time.Minute
	visitorAuditMessagePrefix   = "visitor event: "
	visitorAuditUnknownIPKey    = "<unknown>"
)

type visitorAuditRateState struct {
	tokens     float64
	lastRefill time.Time
	lastSeen   time.Time
}

type visitorAuditRateLimiter struct {
	mu          sync.Mutex
	entries     map[string]*visitorAuditRateState
	lastCleanup time.Time
}

var visitorAuditLimiter = newVisitorAuditRateLimiter()

type visitorAuditParams struct {
	Event     string         `json:"event"`
	Action    string         `json:"action"`
	Operation string         `json:"operation"`
	Path      string         `json:"path"`
	Route     string         `json:"route"`
	Target    string         `json:"target"`
	Detail    map[string]any `json:"detail"`
}

type visitorAuditMessage struct {
	Event     string         `json:"event"`
	Path      string         `json:"path,omitempty"`
	Route     string         `json:"route,omitempty"`
	Target    string         `json:"target,omitempty"`
	UserAgent string         `json:"user_agent,omitempty"`
	Detail    map[string]any `json:"detail,omitempty"`
}

func init() {
	RegisterWithGroupAndMeta("recordVisitorEvent", "public", publicRecordVisitorEvent, &rpc.MethodMeta{
		Name:    "public:recordVisitorEvent",
		Summary: "Record a frontend visitor audit event",
		Params: []rpc.ParamMeta{
			{Name: "event", Type: "string", Required: true, Description: "Short event name, such as page_view or node_open"},
			{Name: "action", Type: "string", Description: "Alias of event"},
			{Name: "operation", Type: "string", Description: "Alias of event"},
			{Name: "path", Type: "string", Description: "Frontend path or URL path, without secrets"},
			{Name: "route", Type: "string", Description: "Frontend route name"},
			{Name: "target", Type: "string", Description: "Optional target identifier"},
			{Name: "detail", Type: "object", Description: "Optional bounded metadata supplied by the frontend"},
		},
		Returns: "{ status: string }",
	})
}

func publicRecordVisitorEvent(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	meta := rpc.MetaFromContext(ctx)
	ip := ""
	uuid := ""
	userAgent := ""
	if meta != nil {
		ip = meta.RemoteIP
		uuid = meta.UserUUID
		userAgent = meta.UserAgent
	}

	if !visitorAuditLimiter.Allow(ip, time.Now()) {
		return map[string]any{"status": "rate_limited"}, nil
	}
	enabled, err := config.GetAs[bool](config.VisitorAuditEnabledKey, false)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to get visitor audit configuration", nil)
	}
	if !enabled {
		return map[string]any{"status": "disabled"}, nil
	}

	var params visitorAuditParams
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid params: "+err.Error(), nil)
	}

	event := normalizeVisitorAuditEvent(firstNonEmpty(params.Event, params.Action, params.Operation))
	if event == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "event is required", nil)
	}

	message, err := buildVisitorAuditMessage(visitorAuditMessage{
		Event:     event,
		Path:      strings.TrimSpace(params.Path),
		Route:     strings.TrimSpace(params.Route),
		Target:    strings.TrimSpace(params.Target),
		UserAgent: strings.TrimSpace(userAgent),
		Detail:    params.Detail,
	})
	if err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid detail", nil)
	}

	auditlog.Log(ip, uuid, message, "visitor")
	return map[string]any{"status": "success"}, nil
}

func newVisitorAuditRateLimiter() *visitorAuditRateLimiter {
	return &visitorAuditRateLimiter{entries: make(map[string]*visitorAuditRateState)}
}

func (l *visitorAuditRateLimiter) Allow(ip string, now time.Time) bool {
	key := strings.TrimSpace(ip)
	if key == "" {
		key = visitorAuditUnknownIPKey
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.lastCleanup.IsZero() || now.Sub(l.lastCleanup) >= visitorAuditCleanupInterval {
		l.cleanupLocked(now)
	}

	state, ok := l.entries[key]
	if !ok {
		if len(l.entries) >= visitorAuditRateMaxEntries {
			return false
		}
		l.entries[key] = &visitorAuditRateState{
			tokens:     visitorAuditRateBurst - 1,
			lastRefill: now,
			lastSeen:   now,
		}
		return true
	}

	if now.After(state.lastRefill) {
		elapsedMinutes := now.Sub(state.lastRefill).Minutes()
		state.tokens = min(float64(visitorAuditRateBurst), state.tokens+elapsedMinutes*visitorAuditRatePerMinute)
		state.lastRefill = now
	}
	state.lastSeen = now
	if state.tokens < 1 {
		return false
	}
	state.tokens--
	return true
}

func (l *visitorAuditRateLimiter) cleanupLocked(now time.Time) {
	for key, state := range l.entries {
		if now.Sub(state.lastSeen) >= visitorAuditLimiterEntryTTL {
			delete(l.entries, key)
		}
	}
	l.lastCleanup = now
}

func normalizeVisitorAuditEvent(event string) string {
	event = strings.TrimSpace(strings.ToLower(event))
	if event == "" {
		return ""
	}
	var builder strings.Builder
	builder.Grow(len(event))
	written := 0
	for _, r := range event {
		allowed := false
		switch {
		case r == '_' || r == '-' || r == ':' || r == '.':
			allowed = true
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			allowed = true
		case unicode.IsSpace(r):
			r = '_'
			allowed = true
		}
		if !allowed {
			continue
		}
		if written >= visitorAuditMaxEventLen {
			break
		}
		builder.WriteRune(r)
		written++
	}
	return builder.String()
}

func trimVisitorAuditDetail(detail map[string]any) map[string]any {
	if len(detail) == 0 {
		return nil
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		return nil
	}
	if len(encoded) <= visitorAuditMaxDetailLen {
		return detail
	}
	return map[string]any{"truncated": true, "size": len(encoded)}
}

func buildVisitorAuditMessage(message visitorAuditMessage) (string, error) {
	message.Event = truncateString(strings.TrimSpace(message.Event), visitorAuditMaxEventLen)
	message.Path = truncateString(strings.TrimSpace(message.Path), visitorAuditMaxPathLen)
	message.Route = truncateString(strings.TrimSpace(message.Route), visitorAuditMaxRouteLen)
	message.Target = truncateString(strings.TrimSpace(message.Target), visitorAuditMaxTargetLen)
	message.UserAgent = truncateString(strings.TrimSpace(message.UserAgent), visitorAuditMaxUserAgentLen)
	message.Detail = trimVisitorAuditDetail(message.Detail)

	detailReduced := false
	for {
		encoded, err := json.Marshal(message)
		if err != nil {
			return "", err
		}
		if len(visitorAuditMessagePrefix)+len(encoded) <= visitorAuditMaxMessageLen {
			return visitorAuditMessagePrefix + string(encoded), nil
		}

		if message.Detail != nil && !detailReduced {
			message.Detail = map[string]any{"truncated": true}
			detailReduced = true
			continue
		}
		if !shrinkLongestVisitorAuditField(&message) {
			return "", errors.New("visitor audit message exceeds maximum length")
		}
	}
}

func shrinkLongestVisitorAuditField(message *visitorAuditMessage) bool {
	fields := []*string{&message.Path, &message.UserAgent, &message.Route, &message.Target}
	var longest *string
	longestLen := 0
	for _, field := range fields {
		if size := utf8.RuneCountInString(*field); size > longestLen {
			longest = field
			longestLen = size
		}
	}
	if longest == nil {
		return false
	}
	*longest = truncateString(*longest, longestLen/2)
	return true
}

func truncateString(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}
