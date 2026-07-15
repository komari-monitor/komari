package jsonrpc

import (
	"context"
	"encoding/json"
	"strings"
	"unicode"

	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/pkg/rpc"
)

const (
	visitorAuditMaxEventLen   = 64
	visitorAuditMaxPathLen    = 512
	visitorAuditMaxRouteLen   = 128
	visitorAuditMaxTargetLen  = 128
	visitorAuditMaxDetailLen  = 2048
	visitorAuditMaxMessageLen = 4096
)

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
	var params visitorAuditParams
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid params: "+err.Error(), nil)
	}

	event := normalizeVisitorAuditEvent(firstNonEmpty(params.Event, params.Action, params.Operation))
	if event == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "event is required", nil)
	}

	meta := rpc.MetaFromContext(ctx)
	ip := ""
	uuid := ""
	userAgent := ""
	if meta != nil {
		ip = meta.RemoteIP
		uuid = meta.UserUUID
		userAgent = meta.UserAgent
	}

	message, err := buildVisitorAuditMessage(visitorAuditMessage{
		Event:     event,
		Path:      truncateString(strings.TrimSpace(params.Path), visitorAuditMaxPathLen),
		Route:     truncateString(strings.TrimSpace(params.Route), visitorAuditMaxRouteLen),
		Target:    truncateString(strings.TrimSpace(params.Target), visitorAuditMaxTargetLen),
		UserAgent: truncateString(strings.TrimSpace(userAgent), visitorAuditMaxPathLen),
		Detail:    trimVisitorAuditDetail(params.Detail),
	})
	if err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid detail", nil)
	}

	auditlog.Log(ip, uuid, message, "visitor")
	return map[string]any{"status": "success"}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func normalizeVisitorAuditEvent(event string) string {
	event = strings.TrimSpace(strings.ToLower(event))
	if event == "" {
		return ""
	}
	var builder strings.Builder
	builder.Grow(len(event))
	for _, r := range event {
		switch {
		case r == '_' || r == '-' || r == ':' || r == '.':
			builder.WriteRune(r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(r)
		case unicode.IsSpace(r):
			builder.WriteByte('_')
		}
		if builder.Len() >= visitorAuditMaxEventLen {
			break
		}
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
	encoded, err := json.Marshal(message)
	if err != nil {
		return "", err
	}
	text := "visitor event: " + string(encoded)
	return truncateString(text, visitorAuditMaxMessageLen), nil
}

func truncateString(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}
