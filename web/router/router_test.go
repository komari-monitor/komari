package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// buildTestEngine registers every production route exactly once so the route
// table can be inspected without booting the whole application.
func buildTestEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	Register(engine)
	return engine
}

// routeKey is the "METHOD path" identifier used in assertions.
type routeKey struct {
	method string
	path   string
}

func routesOf(engine *gin.Engine) map[routeKey]bool {
	routes := make(map[routeKey]bool)
	for _, r := range engine.Routes() {
		routes[routeKey{r.Method, r.Path}] = true
	}
	return routes
}

// TestAdminRoutesAreRegistered pins that every administrative endpoint the
// frontend relies on still exists. A dropped route breaks the admin UI.
func TestAdminRoutesAreRegistered(t *testing.T) {
	engine := buildTestEngine(t)
	routes := routesOf(engine)

	required := []routeKey{
		{http.MethodGet, "/api/admin/settings/"},
		{http.MethodPost, "/api/admin/settings/"},
		{http.MethodGet, "/api/admin/client/list"},
		{http.MethodPost, "/api/admin/client/add"},
		{http.MethodPost, "/api/admin/client/:uuid/edit"},
		{http.MethodPost, "/api/admin/client/:uuid/remove"},
		{http.MethodGet, "/api/admin/client/:uuid/token"},
		{http.MethodGet, "/api/admin/client/:uuid/terminal"},
		{http.MethodPost, "/api/admin/task/exec"},
		{http.MethodGet, "/api/admin/task/all"},
		{http.MethodPost, "/api/admin/plugin/enabled"},
		{http.MethodGet, "/api/admin/plugin/list"},
		{http.MethodPost, "/api/admin/plugin/configuration"},
		{http.MethodPost, "/api/admin/upload/init"},
		{http.MethodPost, "/api/admin/upload/chunk"},
		{http.MethodPost, "/api/admin/upload/merge"},
		{http.MethodPost, "/api/admin/upload/cancel"},
		{http.MethodGet, "/api/admin/2fa/generate"},
		{http.MethodPost, "/api/admin/2fa/enable"},
		{http.MethodPost, "/api/admin/2fa/disable"},
		{http.MethodPost, "/api/admin/session/remove/all"},
		{http.MethodGet, "/api/admin/download/backup"},
	}
	for _, want := range required {
		if !routes[want] {
			t.Errorf("missing admin route %s %s", want.method, want.path)
		}
	}
}

// TestPublicRoutesAreRegistered pins the unauthenticated surface. Anything
// added here is reachable without credentials, so the list is asserted
// explicitly rather than derived.
func TestPublicRoutesAreRegistered(t *testing.T) {
	engine := buildTestEngine(t)
	routes := routesOf(engine)

	required := []routeKey{
		{http.MethodGet, "/ping"},
		{http.MethodPost, "/api/login"},
		{http.MethodGet, "/api/logout"},
		{http.MethodGet, "/api/me"},
		{http.MethodGet, "/api/nodes"},
		{http.MethodGet, "/api/public"},
		{http.MethodGet, "/api/version"},
		{http.MethodGet, "/api/oauth"},
		{http.MethodGet, "/api/oauth_callback"},
		{http.MethodGet, "/api/rpc2"},
		{http.MethodPost, "/api/rpc2"},
		{http.MethodPost, "/api/clients/register"},
	}
	for _, want := range required {
		if !routes[want] {
			t.Errorf("missing public route %s %s", want.method, want.path)
		}
	}
}

// TestAdminPrefixRejectsGuests is the privilege-escalation guard: every route
// under /api/admin must answer 401 to an unauthenticated caller. If a handler
// is registered outside the RequireRole(RoleAdmin) group, this test fails.
func TestAdminPrefixRejectsGuests(t *testing.T) {
	engine := buildTestEngine(t)

	adminRoutes := 0
	for _, route := range engine.Routes() {
		if !strings.HasPrefix(route.Path, "/api/admin") {
			continue
		}
		adminRoutes++
		// WebSocket upgrades and multipart uploads still run the middleware
		// chain first, so a plain GET/POST is enough to observe the 401.
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(route.Method, route.Path, nil)
		engine.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusUnauthorized {
			t.Errorf("%s %s returned %d for an anonymous caller, want 401",
				route.Method, route.Path, recorder.Code)
		}
	}
	if adminRoutes == 0 {
		t.Fatal("no admin routes were registered; the route table is empty")
	}
}

// TestClientPrefixRejectsGuests applies the same guard to the agent surface.
func TestClientPrefixRejectsGuests(t *testing.T) {
	engine := buildTestEngine(t)

	checked := 0
	for _, route := range engine.Routes() {
		if !strings.HasPrefix(route.Path, "/api/clients/") {
			continue
		}
		// /api/clients/register is intentionally public.
		if route.Path == "/api/clients/register" {
			continue
		}
		checked++
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(route.Method, route.Path, nil)
		engine.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusUnauthorized {
			t.Errorf("%s %s returned %d for an anonymous caller, want 401",
				route.Method, route.Path, recorder.Code)
		}
	}
	if checked == 0 {
		t.Fatal("no agent routes were registered")
	}
}

// TestSensitiveRoutesExist pins the endpoints that must stay behind the
// second-factor gate. Their middleware behaviour is covered by
// TestVerifySensitive2FACore in web/api, which does not depend on a live
// database; here we only guard against the routes being dropped.
func TestSensitiveRoutesExist(t *testing.T) {
	engine := buildTestEngine(t)
	routes := routesOf(engine)

	for _, want := range []routeKey{
		{http.MethodPost, "/api/admin/task/exec"},
		{http.MethodPost, "/api/admin/2fa/disable"},
	} {
		if !routes[want] {
			t.Errorf("sensitive route %s %s is missing", want.method, want.path)
		}
	}
}

// TestPingIsUnauthenticated pins the liveness probe used by deployments.
func TestPingIsUnauthenticated(t *testing.T) {
	engine := buildTestEngine(t)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/ping", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /ping = %d, want 200", recorder.Code)
	}
	if body := recorder.Body.String(); body != "pong" {
		t.Fatalf("GET /ping body = %q, want pong", body)
	}
}
