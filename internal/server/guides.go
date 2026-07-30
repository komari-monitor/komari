package server

import (
	"context"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/internal/metricstore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/internal/migrations"
	installweb "github.com/komari-monitor/komari/web/install"
	migrationweb "github.com/komari-monitor/komari/web/migration"
	recoveryweb "github.com/komari-monitor/komari/web/recovery"
)

// InstallRequired reports whether the instance still needs the first-run guide.
func (a *App) InstallRequired() (bool, error) {
	var count int64
	if err := dbcore.GetDBInstance().Model(&models.User{}).Count(&count).Error; err != nil {
		return false, err
	}
	return count == 0, nil
}

type DatabaseMigrationRequirement struct {
	mode    migrationweb.Mode
	summary migrations.LegacyMonitoringSummary
}

func (r DatabaseMigrationRequirement) Required() bool { return r.mode != "" }

// DatabaseMigrationRequired checks both supported migration inputs before the
// normal Metric Store connection is initialized. Structure upgrades run first
// when both inputs exist, then the startup loop detects the legacy tables on
// its next pass.
func (a *App) DatabaseMigrationRequired() (DatabaseMigrationRequirement, error) {
	structureRequired, err := metricstore.StructureUpgradeRequired(context.Background())
	if err != nil {
		return DatabaseMigrationRequirement{}, err
	}
	if structureRequired {
		return DatabaseMigrationRequirement{mode: migrationweb.ModeMetricStructure}, nil
	}
	legacyRequired, summary, err := migrations.LegacyMonitoringMigrationRequired(dbcore.GetDBInstance())
	if err != nil {
		return DatabaseMigrationRequirement{}, err
	}
	if legacyRequired {
		return DatabaseMigrationRequirement{mode: migrationweb.ModeLegacyMonitoring, summary: summary}, nil
	}
	return DatabaseMigrationRequirement{}, nil
}

// RunInstallGuide exposes only first-run installation APIs. It intentionally
// does not mount authentication or normal application routes.
func (a *App) RunInstallGuide() (bool, error) {
	return a.runGuideServer(installweb.NewController(dbcore.GetDBInstance()), guideServerConfig{
		pagePath:   installweb.PagePath,
		missingAPI: "Not found in install mode",
		logMessage: "First-run installation guide is available on %s",
	})
}

// RunMetricStoreRecovery keeps login available while exposing only the
// administrator-protected metric-store recovery API.
func (a *App) RunMetricStoreRecovery(initialErr error) (bool, error) {
	a.initOAuth()
	return a.runGuideServer(recoveryweb.NewController(initialErr, metricStoreReconnectAttempts), guideServerConfig{
		pagePath:         recoveryweb.PagePath,
		missingAPI:       "Not found in database recovery mode",
		logMessage:       "Metric store recovery is available on %s",
		requireIdentity:  true,
		restrictedStatic: true,
	})
}

// RunDatabaseMigration serves the same authenticated guide and status model
// for either migration input while leaving each conversion engine independent.
func (a *App) RunDatabaseMigration(requirement DatabaseMigrationRequirement) (bool, error) {
	a.initOAuth()
	var controller guideController
	switch requirement.mode {
	case migrationweb.ModeMetricStructure:
		controller = migrationweb.NewStructureController()
	case migrationweb.ModeLegacyMonitoring:
		controller = migrationweb.NewLegacyController(dbcore.GetDBInstance(), requirement.summary)
	default:
		return false, nil
	}
	return a.runGuideServer(controller, guideServerConfig{
		pagePath:         migrationweb.PagePath,
		missingAPI:       "Not found in database migration mode",
		logMessage:       "Database migration guide is available on %s",
		requireIdentity:  true,
		restrictedStatic: true,
	})
}
