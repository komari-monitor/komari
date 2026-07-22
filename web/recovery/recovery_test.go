package recovery

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/pkg/config"
	"github.com/komari-monitor/komari/web/api"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestDecodeJSONRejectsUnknownFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	request := httptest.NewRequest("POST", APIPath, strings.NewReader(`{"dsn":"./data/metrics.db","driver":"sqlite"}`))
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = request

	var payload dsnRequest
	if err := decodeJSON(ctx, &payload); err == nil {
		t.Fatal("decodeJSON accepted an unsupported configuration field")
	}
}

func TestDecodeJSONRejectsTrailingValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	request := httptest.NewRequest("POST", APIPath, strings.NewReader(`{"dsn":"./data/metrics.db"}{}`))
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = request

	var payload dsnRequest
	if err := decodeJSON(ctx, &payload); err == nil {
		t.Fatal("decodeJSON accepted trailing JSON values")
	}
}

func TestDecodeJSONBoundsRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	request := httptest.NewRequest("POST", APIPath, strings.NewReader(`{"dsn":"`+strings.Repeat("x", maxRequestBody)+`"}`))
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = request

	var payload dsnRequest
	if err := decodeJSON(ctx, &payload); err == nil || !strings.Contains(err.Error(), "request body too large") {
		t.Fatalf("decodeJSON oversized body error = %v", err)
	}
}

func TestRegisterAddsDSNOnlyEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	controller := &Controller{done: make(chan struct{})}
	controller.Register(router)

	found := false
	for _, route := range router.Routes() {
		if route.Method == "POST" && route.Path == APIPath {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("POST %s route was not registered", APIPath)
	}
}

func TestStatusRequiresAdministrator(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	controller := &Controller{done: make(chan struct{})}
	controller.Activate()
	controller.Register(router)

	request := httptest.NewRequest("GET", APIPath+"/status", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != 401 {
		t.Fatalf("status code = %d, want 401", recorder.Code)
	}
}

func TestStatusAllowsAdminAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open config db: %v", err)
	}
	config.SetDb(db)
	if err := config.Set(config.ApiKeyKey, "recovery-api-key"); err != nil {
		t.Fatalf("set API key: %v", err)
	}

	router := gin.New()
	router.Use(api.IdentityMiddleware())
	controller := &Controller{done: make(chan struct{})}
	controller.Activate()
	controller.Register(router)

	request := httptest.NewRequest("GET", APIPath+"/status", nil)
	request.Header.Set("Authorization", "Bearer recovery-api-key")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != 200 {
		t.Fatalf("API key status code = %d, want 200", recorder.Code)
	}
}

func TestRestrictedLoginBodyIsBounded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/login", limitLoginBody, func(ctx *gin.Context) {
		if _, err := io.ReadAll(ctx.Request.Body); err != nil {
			ctx.Status(400)
			return
		}
		ctx.Status(200)
	})

	request := httptest.NewRequest("POST", "/api/login", strings.NewReader(strings.Repeat("x", maxRequestBody+1)))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != 400 {
		t.Fatalf("oversized login body status code = %d, want 400", recorder.Code)
	}
}
