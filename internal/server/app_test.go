package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRetryMetricStoreConnectionStopsAfterRecovery(t *testing.T) {
	wantErr := errors.New("temporary connection failure")
	attempts := 0
	err := retryMetricStoreConnection(3, 0, func() error {
		attempts++
		if attempts < 3 {
			return wantErr
		}
		return nil
	})
	if err != nil {
		t.Fatalf("retry returned error after recovery: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestGuideNoRouteRejectsUnknownAPIAndRedirectsPages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(noStoreAPIResponses())
	router.NoRoute(guideNoRoute("/install", "Not found in install mode", nil))

	apiResponse := httptest.NewRecorder()
	router.ServeHTTP(apiResponse, httptest.NewRequest(http.MethodGet, "/api/unknown", nil))
	if apiResponse.Code != http.StatusNotFound {
		t.Fatalf("API status = %d, want %d", apiResponse.Code, http.StatusNotFound)
	}
	if got := apiResponse.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("API cache control = %q, want no-store", got)
	}

	pageResponse := httptest.NewRecorder()
	router.ServeHTTP(pageResponse, httptest.NewRequest(http.MethodGet, "/stale-guide", nil))
	if pageResponse.Code != http.StatusTemporaryRedirect {
		t.Fatalf("page status = %d, want %d", pageResponse.Code, http.StatusTemporaryRedirect)
	}
	if got := pageResponse.Header().Get("Location"); got != "/install" {
		t.Fatalf("redirect location = %q, want /install", got)
	}
}

func TestRetryMetricStoreConnectionReturnsLastError(t *testing.T) {
	wantErr := errors.New("connection unavailable")
	attempts := 0
	err := retryMetricStoreConnection(3, 0, func() error {
		attempts++
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}
