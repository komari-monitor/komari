package server

import (
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/internal/migrations"
	installweb "github.com/komari-monitor/komari/web/install"
	recoveryweb "github.com/komari-monitor/komari/web/recovery"
	upgradeweb "github.com/komari-monitor/komari/web/update"
)

// InstallRequired reports whether the instance still needs the first-run guide.
func (a *App) InstallRequired() (bool, error) {
	var count int64
	if err := dbcore.GetDBInstance().Model(&models.User{}).Count(&count).Error; err != nil {
		return false, err
	}
	return count == 0, nil
}

func (a *App) LegacyUpgradeRequired() (bool, migrations.LegacyMonitoringSummary, error) {
	return migrations.LegacyMonitoringMigrationRequired(dbcore.GetDBInstance())
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

// RunLegacyUpgrade keeps login available while migration is in progress.
func (a *App) RunLegacyUpgrade(summary migrations.LegacyMonitoringSummary) (bool, error) {
	a.initOAuth()
	return a.runGuideServer(upgradeweb.NewController(dbcore.GetDBInstance(), summary), guideServerConfig{
		pagePath:        upgradeweb.PagePath,
		missingAPI:      "Not found in upgrade mode",
		logMessage:      "Legacy monitoring data requires the 1.2.7 upgrade wizard on %s",
		requireIdentity: true,
	})
}
