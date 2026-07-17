package jsonrpc

import (
	"context"
	"errors"
	"sync"

	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/metricstore"
	"github.com/komari-monitor/komari/pkg/metric"
	"github.com/komari-monitor/komari/pkg/rpc"
)

const (
	databaseLocationLocal    = "local"
	databaseLocationExternal = "external"
)

var databaseMaintenanceMu sync.Mutex

type databaseStorageStatus struct {
	Driver   string `json:"driver"`
	Location string `json:"location"`
	Size     *int64 `json:"size"`
	Action   string `json:"action"`
	Error    string `json:"error,omitempty"`
}

type databaseStatusResponse struct {
	// Type and Size preserve the original main-database-only response contract.
	Type       string                `json:"type"`
	Size       int64                 `json:"size"`
	Main       databaseStorageStatus `json:"main"`
	Monitoring databaseStorageStatus `json:"monitoring"`
	LocalTotal *int64                `json:"local_total"`
}

type databaseMaintenanceResult struct {
	Driver    string `json:"driver"`
	Action    string `json:"action"`
	Before    *int64 `json:"before"`
	After     *int64 `json:"after"`
	Success   bool   `json:"success"`
	Error     string `json:"error,omitempty"`
	SizeError string `json:"size_error,omitempty"`
}

type databaseMaintenanceResponse struct {
	// Before, After and Size preserve the original main-database-only fields.
	Before       int64                     `json:"before"`
	After        int64                     `json:"after"`
	Size         int64                     `json:"size"`
	AllSucceeded bool                      `json:"all_succeeded"`
	Main         databaseMaintenanceResult `json:"main"`
	Monitoring   databaseMaintenanceResult `json:"monitoring"`
}

func init() {
	// Retain the original method names so existing REST/RPC clients keep working;
	// the result now describes both databases and their driver-specific actions.
	reg("getDatabaseSize", adminGetDatabaseSize, "Get main and monitoring database storage usage")
	reg("vacuumDatabase", adminVacuumDatabase, "Reclaim space in the main and monitoring databases")
	reg("getDatabaseCompression", adminGetDatabaseCompression, "Get remote monitoring database compression capabilities")
	reg("setDatabaseCompression", adminSetDatabaseCompression, "Configure remote monitoring database compression")
}

func adminGetDatabaseSize(ctx context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	main := mainDatabaseStatus()
	monitoring := monitoringDatabaseStatus(ctx)
	legacySize := int64(0)
	if main.Size != nil {
		legacySize = *main.Size
	}

	return databaseStatusResponse{
		Type:       main.Driver,
		Size:       legacySize,
		Main:       main,
		Monitoring: monitoring,
		LocalTotal: localDatabaseTotal(main, monitoring),
	}, nil
}

func adminVacuumDatabase(ctx context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	if !databaseMaintenanceMu.TryLock() {
		return nil, rpc.MakeError(rpc.InternalError, "database maintenance is already in progress", nil)
	}
	defer databaseMaintenanceMu.Unlock()

	main := maintainMainDatabase(ctx)
	monitoring := maintainMonitoringDatabase(ctx)
	response := newDatabaseMaintenanceResponse(main, monitoring)

	actor, ip := auditActor(ctx)
	level := "warn"
	message := "reclaimed database space"
	if !response.AllSucceeded {
		message = "database space reclaim completed with errors"
		level = "error"
	}
	auditlog.Log(ip, actor, message, level)

	return response, nil
}

func adminGetDatabaseCompression(ctx context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	status, err := metricstore.InspectCompression(ctx)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "failed to inspect database compression: "+err.Error(), nil)
	}
	return status, nil
}

func adminSetDatabaseCompression(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var cfg metric.CompressionConfig
	if err := req.BindParams(&cfg); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "invalid database compression settings: "+err.Error(), nil)
	}
	if !databaseMaintenanceMu.TryLock() {
		return nil, rpc.MakeError(rpc.InternalError, "database maintenance is already in progress", nil)
	}
	defer databaseMaintenanceMu.Unlock()

	status, err := metricstore.ConfigureCompression(ctx, cfg)
	if err != nil {
		code := rpc.InternalError
		if errors.Is(err, metric.ErrInvalidArgument) {
			code = rpc.InvalidParams
		}
		return nil, rpc.MakeError(code, "failed to configure database compression: "+err.Error(), nil)
	}

	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, "updated monitoring database compression settings", "warn")
	return status, nil
}

func newDatabaseMaintenanceResponse(main, monitoring databaseMaintenanceResult) databaseMaintenanceResponse {
	return databaseMaintenanceResponse{
		Before:       valueOrZero(main.Before),
		After:        valueOrZero(main.After),
		Size:         valueOrZero(main.After),
		AllSucceeded: main.Success && monitoring.Success,
		Main:         main,
		Monitoring:   monitoring,
	}
}

func mainDatabaseStatus() databaseStorageStatus {
	status := databaseStorageStatus{
		Driver:   flags.NormalizeDatabaseType(flags.DatabaseType),
		Location: databaseLocationLocal,
		Action:   string(metric.MaintenanceVacuum),
	}
	size, err := dbcore.StorageSize()
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.Size = int64Pointer(size)
	return status
}

func monitoringDatabaseStatus(ctx context.Context) databaseStorageStatus {
	info, err := metricstore.InspectStorage(ctx)
	status := databaseStorageStatus{
		Driver: string(info.Driver),
		Action: string(info.Action),
	}
	status.Location = databaseLocationForDriver(info.Driver)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.Size = int64Pointer(info.Size)
	return status
}

func maintainMainDatabase(ctx context.Context) databaseMaintenanceResult {
	status := mainDatabaseStatus()
	result := databaseMaintenanceResult{
		Driver: status.Driver,
		Action: status.Action,
		Before: status.Size,
	}
	if status.Error != "" {
		result.SizeError = "before: " + status.Error
	}

	if !flags.IsSQLite() {
		result.Error = "main database maintenance is only supported for SQLite"
	} else if err := dbcore.ReclaimSpace(ctx); err != nil {
		result.Error = err.Error()
	} else {
		result.Success = true
	}

	after, err := dbcore.StorageSize()
	if err != nil {
		result.SizeError = appendMeasurementError(result.SizeError, "after", err)
	} else {
		result.After = int64Pointer(after)
	}
	return result
}

func maintainMonitoringDatabase(ctx context.Context) databaseMaintenanceResult {
	maintained, maintenanceErr := metricstore.ReclaimSpace(ctx)
	result := databaseMaintenanceResult{
		Driver:  string(maintained.Driver),
		Action:  string(maintained.Action),
		Success: maintenanceErr == nil,
	}
	if maintained.BeforeSizeError != nil {
		result.SizeError = appendMeasurementError(result.SizeError, "before", maintained.BeforeSizeError)
	} else {
		result.Before = int64Pointer(maintained.Before)
	}
	if maintained.AfterSizeError != nil {
		result.SizeError = appendMeasurementError(result.SizeError, "after", maintained.AfterSizeError)
	} else {
		result.After = int64Pointer(maintained.After)
	}
	if maintenanceErr != nil {
		result.Error = maintenanceErr.Error()
	}
	return result
}

func databaseLocationForDriver(driver metric.Driver) string {
	if driver == metric.DriverSQLite {
		return databaseLocationLocal
	}
	if driver == "" {
		return ""
	}
	return databaseLocationExternal
}

func localDatabaseTotal(statuses ...databaseStorageStatus) *int64 {
	var total int64
	for _, status := range statuses {
		if status.Location != databaseLocationLocal || status.Size == nil {
			return nil
		}
		total += *status.Size
	}
	return int64Pointer(total)
}

func appendMeasurementError(current, phase string, err error) string {
	next := phase + ": " + err.Error()
	if current == "" {
		return next
	}
	return current + "; " + next
}

func int64Pointer(value int64) *int64 {
	return &value
}

func valueOrZero(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}
