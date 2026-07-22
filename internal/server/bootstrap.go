package server

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/internal/config"
	"github.com/komari-monitor/komari/pkg/resourceprobe"
	"github.com/komari-monitor/komari/utils"
	logger "github.com/komari-monitor/komari/utils/log"
)

// Bootstrap initializes the data directory, primary database, and settings.
func (a *App) Bootstrap() error {
	if err := os.MkdirAll("./data/theme", os.ModePerm); err != nil {
		return fmt.Errorf("failed to create theme directory: %w", err)
	}

	dbcore.SetVersionID(utils.CurrentVersion + "-" + utils.VersionHash)
	if err := dbcore.Initialize(); err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	a.dbReady = true
	a.addCleanup("database", func(context.Context) error { return dbcore.Close() })

	gin.SetMode(gin.ReleaseMode)
	lowResourceMode, err := ensureLowResourceModeDefault()
	if err != nil {
		return fmt.Errorf("failed to initialize low resource mode: %w", err)
	}
	if err := dbcore.ConfigureLowResourceMode(lowResourceMode); err != nil {
		return err
	}

	settings, err := config.GetManyAs[config.Settings]()
	if err != nil {
		return fmt.Errorf("failed to load settings: %w", err)
	}
	a.settings = settings
	return nil
}

func ensureLowResourceModeDefault() (bool, error) {
	values, err := config.GetMany(map[string]any{config.LowResourceModeKey: nil})
	if err != nil {
		return false, err
	}
	if saved, ok := values[config.LowResourceModeKey]; ok {
		enabled, ok := saved.(bool)
		if !ok {
			return false, fmt.Errorf("%s must be a boolean", config.LowResourceModeKey)
		}
		return enabled, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	result := resourceprobe.Detect(ctx, "./data")
	if err := config.Set(config.LowResourceModeKey, result.LowResource); err != nil {
		return false, err
	}
	logger.Infof("server", "Low resource mode auto-detection: enabled=%t memory=%dMiB disk_free=%dMiB cpu=%.0fops/s random_write=%.2fMiB/s iops=%.0f reasons=%v", result.LowResource, result.MemoryBytes/(1024*1024), result.DiskFreeBytes/(1024*1024), result.CPUOpsPerSecond, result.WriteBytesPerSecond/(1024*1024), result.WriteIOPS, result.Reasons)
	return result.LowResource, nil
}
