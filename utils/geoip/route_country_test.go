package geoip

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRouteCountryUpdateDue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "country.mmdb")
	now := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	if !routeCountryUpdateDue(path, now) {
		t.Fatal("missing database should be downloaded")
	}
	if err := os.WriteFile(path, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	updated := now.Add(-6 * 24 * time.Hour)
	if err := os.Chtimes(path, updated, updated); err != nil {
		t.Fatal(err)
	}
	if routeCountryUpdateDue(path, now) {
		t.Fatal("database younger than seven days should not be downloaded")
	}
	if !routeCountryUpdateDue(path, updated.Add(routeCountryInterval)) {
		t.Fatal("database should be updated after exactly seven days")
	}
}

func TestInvalidRouteCountryDownloadKeepsPreviousFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "country.mmdb")
	if err := os.WriteFile(path, []byte("previous database"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("invalid database"))
	}))
	defer server.Close()
	if err := downloadRouteCountry(context.Background(), path, server.URL); err == nil {
		t.Fatal("invalid database should fail validation")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "previous database" {
		t.Fatalf("failed download changed the previous file: %q", data)
	}
}
