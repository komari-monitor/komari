package jsonrpc

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/internal/metricstore"
	"github.com/komari-monitor/komari/pkg/metric"
	"github.com/komari-monitor/komari/pkg/rpc"
)

const (
	databaseTargetMain    = "main"
	databaseTargetMetrics = "metrics"
	defaultDBQueryLimit   = 1000
	maxDBQueryLimit       = 10000
)

type databaseQueryParams struct {
	Database *string `json:"database"`
	SQL      string  `json:"sql"`
	Args     []any   `json:"args"`
	Limit    *int    `json:"limit"`
}

type databaseExecParams struct {
	Database *string `json:"database"`
	SQL      string  `json:"sql"`
	Args     []any   `json:"args"`
}

type databaseTablesParams struct {
	Database *string `json:"database"`
}

type databaseQueryResponse struct {
	Database  string   `json:"database"`
	Driver    string   `json:"driver"`
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	RowCount  int      `json:"row_count"`
	Truncated bool     `json:"truncated"`
}

type databaseExecResponse struct {
	Database     string `json:"database"`
	Driver       string `json:"driver"`
	RowsAffected int64  `json:"rows_affected"`
	LastInsertID *int64 `json:"last_insert_id"`
}

type databaseTablesResponse struct {
	Database string   `json:"database"`
	Driver   string   `json:"driver"`
	Tables   []string `json:"tables"`
}

func init() {
	RegisterWithGroupAndMeta("dbQuery", rpc.RoleAdmin, adminDBQuery, &rpc.MethodMeta{
		Name:    "admin:dbQuery",
		Summary: "Execute a SQL query against the main or metrics database",
		Params: []rpc.ParamMeta{
			{Name: "database", Type: "main | metrics", Description: "Database target (default main)"},
			{Name: "sql", Type: "string", Required: true, Description: "SQL query to execute"},
			{Name: "args", Type: "any[]", Description: "Positional SQL parameters"},
			{Name: "limit", Type: "number", Description: "Maximum rows to return (default 1000, max 10000)"},
		},
		Returns: "{ database: string, driver: string, columns: string[], rows: any[][], row_count: number, truncated: boolean }",
	})
	RegisterWithGroupAndMeta("dbExec", rpc.RoleAdmin, adminDBExec, &rpc.MethodMeta{
		Name:    "admin:dbExec",
		Summary: "Execute a SQL statement against the main or metrics database",
		Params: []rpc.ParamMeta{
			{Name: "database", Type: "main | metrics", Description: "Database target (default main)"},
			{Name: "sql", Type: "string", Required: true, Description: "SQL statement to execute"},
			{Name: "args", Type: "any[]", Description: "Positional SQL parameters"},
		},
		Returns: "{ database: string, driver: string, rows_affected: number, last_insert_id: number | null }",
	})
	RegisterWithGroupAndMeta("dbTables", rpc.RoleAdmin, adminDBTables, &rpc.MethodMeta{
		Name:    "admin:dbTables",
		Summary: "List tables in the main or metrics database",
		Params: []rpc.ParamMeta{
			{Name: "database", Type: "main | metrics", Description: "Database target (default main)"},
		},
		Returns: "{ database: string, driver: string, tables: string[] }",
	})
}

func adminDBQuery(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	params, target, statement, limit, rpcErr := parseDatabaseQueryParams(req)
	if rpcErr != nil {
		return nil, rpcErr
	}

	response, err := queryDatabase(ctx, target, statement, params.Args, limit)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to execute database query: "+err.Error(), nil)
	}
	return response, nil
}

func adminDBExec(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params databaseExecParams
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}
	target, rpcErr := parseDatabaseTarget(params.Database)
	if rpcErr != nil {
		return nil, rpcErr
	}
	statement := strings.TrimSpace(params.SQL)
	if statement == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "sql is required", nil)
	}

	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, "execute database SQL on "+target, "warn")

	result, driver, err := executeDatabase(ctx, target, statement, params.Args)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to execute database statement: "+err.Error(), nil)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to read affected row count: "+err.Error(), nil)
	}

	var lastInsertID *int64
	if id, err := result.LastInsertId(); err == nil {
		lastInsertID = &id
	}
	return databaseExecResponse{
		Database:     target,
		Driver:       string(driver),
		RowsAffected: rowsAffected,
		LastInsertID: lastInsertID,
	}, nil
}

func adminDBTables(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params databaseTablesParams
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}
	target, rpcErr := parseDatabaseTarget(params.Database)
	if rpcErr != nil {
		return nil, rpcErr
	}

	response, err := listDatabaseTables(ctx, target)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to list database tables: "+err.Error(), nil)
	}
	return response, nil
}

func parseDatabaseQueryParams(req *rpc.JsonRpcRequest) (databaseQueryParams, string, string, int, *rpc.JsonRpcError) {
	var params databaseQueryParams
	if err := req.BindParams(&params); err != nil {
		return databaseQueryParams{}, "", "", 0, rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}
	target, rpcErr := parseDatabaseTarget(params.Database)
	if rpcErr != nil {
		return databaseQueryParams{}, "", "", 0, rpcErr
	}
	statement := strings.TrimSpace(params.SQL)
	if statement == "" {
		return databaseQueryParams{}, "", "", 0, rpc.MakeError(rpc.InvalidParams, "sql is required", nil)
	}
	limit := defaultDBQueryLimit
	if params.Limit != nil {
		limit = *params.Limit
		if limit < 1 || limit > maxDBQueryLimit {
			return databaseQueryParams{}, "", "", 0, rpc.MakeError(rpc.InvalidParams, fmt.Sprintf("limit must be between 1 and %d", maxDBQueryLimit), nil)
		}
	}
	return params, target, statement, limit, nil
}

func parseDatabaseTarget(value *string) (string, *rpc.JsonRpcError) {
	if value == nil {
		return databaseTargetMain, nil
	}
	target := strings.TrimSpace(*value)
	if target != databaseTargetMain && target != databaseTargetMetrics {
		return "", rpc.MakeError(rpc.InvalidParams, "database must be main or metrics", nil)
	}
	return target, nil
}

func queryDatabase(ctx context.Context, target, statement string, args []any, limit int) (databaseQueryResponse, error) {
	rows, driver, release, err := openDatabaseRows(ctx, target, statement, args...)
	if err != nil {
		return databaseQueryResponse{}, err
	}
	defer func() {
		_ = rows.Close()
		release()
	}()

	columns, resultRows, truncated, err := collectDatabaseRows(rows, limit)
	if err != nil {
		return databaseQueryResponse{}, err
	}
	return databaseQueryResponse{
		Database:  target,
		Driver:    string(driver),
		Columns:   columns,
		Rows:      resultRows,
		RowCount:  len(resultRows),
		Truncated: truncated,
	}, nil
}

func executeDatabase(ctx context.Context, target, statement string, args []any) (sql.Result, metric.Driver, error) {
	switch target {
	case databaseTargetMain:
		db, err := dbcore.GetDBInstance().DB()
		if err != nil {
			return nil, "", err
		}
		result, err := db.ExecContext(ctx, statement, args...)
		return result, metric.DriverSQLite, err
	case databaseTargetMetrics:
		return metricstore.ExecContext(ctx, statement, args...)
	default:
		return nil, "", fmt.Errorf("unsupported database target: %s", target)
	}
}

func listDatabaseTables(ctx context.Context, target string) (databaseTablesResponse, error) {
	var (
		rows         *sql.Rows
		actualDriver metric.Driver
		release      func()
		err          error
	)
	switch target {
	case databaseTargetMain:
		statement, err := tableListSQL(metric.DriverSQLite)
		if err != nil {
			return databaseTablesResponse{}, err
		}
		rows, actualDriver, release, err = openDatabaseRows(ctx, target, statement)
	case databaseTargetMetrics:
		rows, actualDriver, release, err = metricstore.QueryForDriver(ctx, tableListSQL)
	default:
		return databaseTablesResponse{}, fmt.Errorf("unsupported database target: %s", target)
	}
	if err != nil {
		return databaseTablesResponse{}, err
	}
	defer func() {
		_ = rows.Close()
		release()
	}()

	tables := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return databaseTablesResponse{}, err
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		return databaseTablesResponse{}, err
	}
	sort.Strings(tables)
	return databaseTablesResponse{Database: target, Driver: string(actualDriver), Tables: tables}, nil
}

func openDatabaseRows(ctx context.Context, target, statement string, args ...any) (*sql.Rows, metric.Driver, func(), error) {
	switch target {
	case databaseTargetMain:
		db, err := dbcore.GetDBInstance().DB()
		if err != nil {
			return nil, "", nil, err
		}
		rows, err := db.QueryContext(ctx, statement, args...)
		return rows, metric.DriverSQLite, func() {}, err
	case databaseTargetMetrics:
		return metricstore.QueryContext(ctx, statement, args...)
	default:
		return nil, "", nil, fmt.Errorf("unsupported database target: %s", target)
	}
}

func collectDatabaseRows(rows *sql.Rows, limit int) ([]string, [][]any, bool, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, nil, false, err
	}
	resultRows := make([][]any, 0, limit)
	values := make([]any, len(columns))
	scans := make([]any, len(columns))
	for i := range scans {
		scans[i] = &values[i]
	}

	for rows.Next() {
		if len(resultRows) == limit {
			return columns, resultRows, true, nil
		}
		if err := rows.Scan(scans...); err != nil {
			return nil, nil, false, err
		}
		row := make([]any, len(values))
		for i, value := range values {
			row[i] = normalizeDatabaseValue(value)
		}
		resultRows = append(resultRows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, false, err
	}
	return columns, resultRows, false, nil
}

func normalizeDatabaseValue(value any) any {
	switch value := value.(type) {
	case []byte:
		return string(value)
	case time.Time:
		return value.Format(time.RFC3339Nano)
	default:
		return value
	}
}

func tableListSQL(driver metric.Driver) (string, error) {
	switch driver {
	case metric.DriverSQLite:
		return "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'", nil
	case metric.DriverMySQL:
		return "SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE'", nil
	case metric.DriverPostgreSQL:
		return "SELECT tablename FROM pg_catalog.pg_tables WHERE schemaname = current_schema()", nil
	default:
		return "", fmt.Errorf("unsupported database driver: %s", driver)
	}
}
