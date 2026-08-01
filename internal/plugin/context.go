package plugin

import (
	"net"
	"net/http"
	"strings"

	"github.com/dop251/goja"
	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/pkg/rpc"
)

// principalContextKey stays in sync with web/api/principal.go:
// IdentityMiddleware stores the resolved principal under this gin key.
const principalContextKey = "principal"

// routeRequestContext builds the JS request context with the caller identity
// resolved by IdentityMiddleware. Raw credentials (session/client tokens) are
// intentionally not exposed.
func routeRequestContext(vm *goja.Runtime, c *gin.Context) *goja.Object {
	ctx := vm.NewObject()
	principal := rpcPrincipalFromGin(c)
	p := vm.NewObject()
	if principal != nil {
		_ = p.Set("type", principalTypeString(principal.Type))
		_ = p.Set("roles", principal.Roles)
		_ = p.Set("user_uuid", principal.UserUUID)
		_ = p.Set("client_uuid", principal.ClientUUID)
		_ = p.Set("is_api_key", principal.IsAPIKey)
	}
	_ = ctx.Set("principal", p)
	if role, ok := c.Get("role"); ok {
		if s, ok := role.(string); ok {
			_ = ctx.Set("role", s)
		}
	}
	if uuid, ok := c.Get("uuid"); ok {
		_ = ctx.Set("user_uuid", uuid)
	}
	if clientUUID, ok := c.Get("client_uuid"); ok {
		_ = ctx.Set("client_uuid", clientUUID)
	}
	_ = ctx.Set("remote_ip", c.ClientIP())
	_ = ctx.Set("user_agent", c.GetHeader("User-Agent"))
	return ctx
}

// hookRequestContext builds the JS request context at the raw HTTP layer
// (before the gin identity middleware runs): only network metadata is
// available there.
func hookRequestContext(vm *goja.Runtime, r *http.Request) *goja.Object {
	ctx := vm.NewObject()
	_ = ctx.Set("remote_ip", remoteIP(r))
	_ = ctx.Set("user_agent", r.Header.Get("User-Agent"))
	return ctx
}

func rpcPrincipalFromGin(c *gin.Context) *rpc.Principal {
	value, ok := c.Get(principalContextKey)
	if !ok {
		return nil
	}
	principal, _ := value.(*rpc.Principal)
	return principal
}

func principalTypeString(t rpc.PrincipalType) string {
	switch t {
	case rpc.PrincipalAgent:
		return "agent"
	case rpc.PrincipalUser:
		return "user"
	case rpc.PrincipalAPIKey:
		return "api_key"
	default:
		return "anonymous"
	}
}

// remoteIP mirrors gin's ClientIP for the raw request layer: first
// X-Forwarded-For entry, then X-Real-IP, then the RemoteAddr host.
func remoteIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		if first, _, ok := strings.Cut(forwarded, ","); ok {
			return strings.TrimSpace(first)
		}
		return strings.TrimSpace(forwarded)
	}
	if real := r.Header.Get("X-Real-IP"); real != "" {
		return strings.TrimSpace(real)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
