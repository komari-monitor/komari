package geoip

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	logger "github.com/komari-monitor/komari/utils/log"
	"github.com/oschwald/maxminddb-golang"
)

const (
	routeCountryFilePath = "./data/RouteGeoLite2-Country.mmdb"
	routeCountryURL      = "https://raw.githubusercontent.com/Loyalsoldier/geoip/release/GeoLite2-Country.mmdb"
	routeCountryInterval = 7 * 24 * time.Hour
	routeCountryMaxBytes = 32 << 20
)

// The route classifier has its own local database. It does not change the
// GeoIP provider selected for the rest of Komari or make per-hop HTTP calls.
var routeCountry = struct {
	sync.RWMutex
	reader *maxminddb.Reader
}{}

type routeCountryRecord struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
}

// StartRouteCountryUpdater loads an existing database immediately and checks
// once a day whether its last successful download is at least seven days old.
// A failed update leaves the previous reader and file intact.
func StartRouteCountryUpdater() func() error {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := loadRouteCountryFile(routeCountryFilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			logger.Warn("geoip", "route country database could not be loaded", "error", err)
		}
		refreshRouteCountryIfDue(ctx, routeCountryFilePath, routeCountryURL, time.Now())
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				refreshRouteCountryIfDue(ctx, routeCountryFilePath, routeCountryURL, now)
			}
		}
	}()
	return func() error {
		cancel()
		<-done
		routeCountry.Lock()
		defer routeCountry.Unlock()
		if routeCountry.reader == nil {
			return nil
		}
		err := routeCountry.reader.Close()
		routeCountry.reader = nil
		return err
	}
}

// LookupRouteCountry reads only the local MMDB. An unavailable or incomplete
// database returns an empty country rather than making an online lookup.
func LookupRouteCountry(ip net.IP) string {
	if ip == nil {
		return ""
	}
	routeCountry.RLock()
	defer routeCountry.RUnlock()
	if routeCountry.reader == nil {
		return ""
	}
	var record routeCountryRecord
	if err := routeCountry.reader.Lookup(ip, &record); err != nil {
		return ""
	}
	return record.Country.ISOCode
}

func routeCountryUpdateDue(path string, now time.Time) bool {
	info, err := os.Stat(path)
	return err != nil || !info.ModTime().Add(routeCountryInterval).After(now)
}

func refreshRouteCountryIfDue(ctx context.Context, path, url string, now time.Time) {
	routeCountry.RLock()
	readerMissing := routeCountry.reader == nil
	routeCountry.RUnlock()
	if !readerMissing && !routeCountryUpdateDue(path, now) {
		return
	}
	if err := downloadRouteCountry(ctx, path, url); err != nil {
		if ctx.Err() == nil {
			logger.Warn("geoip", "weekly route country database update failed; retaining previous database", "error", err)
		}
		return
	}
	logger.Info("geoip", "route country database updated")
}

func loadRouteCountryFile(path string) error {
	reader, err := maxminddb.Open(path)
	if err != nil {
		return err
	}
	if reader.Metadata.DatabaseType != "GeoLite2-Country" {
		reader.Close()
		return fmt.Errorf("unexpected route country database type %q", reader.Metadata.DatabaseType)
	}
	swapRouteCountryReader(reader)
	return nil
}

func swapRouteCountryReader(reader *maxminddb.Reader) {
	routeCountry.Lock()
	old := routeCountry.reader
	routeCountry.reader = reader
	routeCountry.Unlock()
	if old != nil {
		_ = old.Close()
	}
}

func downloadRouteCountry(ctx context.Context, path, url string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("route country download returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > routeCountryMaxBytes {
		return fmt.Errorf("route country download exceeds %d bytes", routeCountryMaxBytes)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".route-country-*.mmdb")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	written, copyErr := io.Copy(temp, io.LimitReader(response.Body, routeCountryMaxBytes+1))
	if copyErr == nil && written > routeCountryMaxBytes {
		copyErr = fmt.Errorf("route country download exceeds %d bytes", routeCountryMaxBytes)
	}
	if syncErr := temp.Sync(); copyErr == nil {
		copyErr = syncErr
	}
	if closeErr := temp.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return copyErr
	}
	reader, err := maxminddb.Open(tempPath)
	if err != nil {
		return fmt.Errorf("invalid route country database: %w", err)
	}
	if reader.Metadata.DatabaseType != "GeoLite2-Country" {
		reader.Close()
		return fmt.Errorf("unexpected route country database type %q", reader.Metadata.DatabaseType)
	}
	if err := os.Rename(tempPath, path); err != nil {
		reader.Close()
		return err
	}
	swapRouteCountryReader(reader)
	return nil
}
