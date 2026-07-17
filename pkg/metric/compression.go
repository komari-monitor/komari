package metric

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

const (
	CompressionModeNone            = "none"
	CompressionModeMixed           = "mixed"
	CompressionModeMySQLRow        = "mysql_row"
	CompressionModeMySQLPage       = "mysql_page"
	CompressionModePostgreSQLToast = "postgresql_toast"
	CompressionModeTimescaleDB     = "timescaledb"
)

const (
	compressionReasonLocalDatabase                 = "local_database"
	compressionReasonInnoDBFilePerTableDisabled    = "innodb_file_per_table_disabled"
	compressionReasonMySQLPageUnsupported          = "mysql_page_unsupported"
	compressionReasonTimescaleDBNotInstalled       = "timescaledb_not_installed"
	compressionReasonTimescaleDBVersionUnsupported = "timescaledb_version_unsupported"
	compressionReasonTimescaleDBAlreadyEnabled     = "timescaledb_already_enabled"
	compressionReasonDatabaseOwnerRequired         = "database_owner_required"
	compressionReasonWALPermissionRequired         = "wal_compression_permission_required"

	compressionWarningMySQLPageFilesystem = "mysql_page_filesystem"
	compressionWarningPostgreSQLToast     = "postgresql_toast_existing_rows"
	compressionWarningTimescaleDB         = "timescaledb_irreversible"
	compressionWarningWALConnections      = "wal_compression_new_connections"
)

// CompressionOption describes a mutually exclusive table-storage mode.
// Algorithms is empty when the mode has no algorithm choice.
type CompressionOption struct {
	Mode       string   `json:"mode"`
	Supported  bool     `json:"supported"`
	Algorithms []string `json:"algorithms"`
	Reason     string   `json:"reason,omitempty"`
	Warning    string   `json:"warning,omitempty"`
}

type StorageCompressionStatus struct {
	Enabled   bool                `json:"enabled"`
	Mode      string              `json:"mode"`
	Algorithm string              `json:"algorithm,omitempty"`
	Options   []CompressionOption `json:"options"`
}

// WALCompressionStatus is separate from table storage because PostgreSQL WAL
// compression can be enabled at the same time as TOAST compression.
type WALCompressionStatus struct {
	Visible           bool     `json:"visible"`
	Supported         bool     `json:"supported"`
	Enabled           bool     `json:"enabled"`
	Algorithm         string   `json:"algorithm,omitempty"`
	Algorithms        []string `json:"algorithms"`
	Reason            string   `json:"reason,omitempty"`
	Warning           string   `json:"warning,omitempty"`
	RequiresReconnect bool     `json:"requires_reconnect"`
}

type CompressionStatus struct {
	Driver    Driver                   `json:"driver"`
	Available bool                     `json:"available"`
	Reason    string                   `json:"reason,omitempty"`
	Storage   StorageCompressionStatus `json:"storage"`
	WAL       *WALCompressionStatus    `json:"wal,omitempty"`
}

type CompressionConfig struct {
	StorageEnabled      bool   `json:"storage_enabled"`
	StorageMode         string `json:"storage_mode"`
	StorageAlgorithm    string `json:"storage_algorithm"`
	WALEnabled          bool   `json:"wal_enabled"`
	WALAlgorithm        string `json:"wal_algorithm"`
	ConfirmIrreversible bool   `json:"confirm_irreversible"`
}

type mysqlCompressionTableState struct {
	rowFormat     string
	createOptions string
}

type postgreSQLCompressionColumnState struct {
	storage     string
	compression string
}

type postgreSQLCompressionTable struct {
	name    string
	columns []string
}

// InspectCompression detects the active remote-backend compression settings
// and the modes supported by the current server and account.
func (s *Store) InspectCompression(ctx context.Context) (CompressionStatus, error) {
	s.maintenanceMu.RLock()
	defer s.maintenanceMu.RUnlock()

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.db == nil {
		return CompressionStatus{}, ErrClosed
	}
	return s.inspectCompression(ctx)
}

// ConfigureCompression applies only to tables owned by Store. Storage modes
// are mutually exclusive; PostgreSQL WAL compression remains independent.
func (s *Store) ConfigureCompression(ctx context.Context, cfg CompressionConfig) (CompressionStatus, error) {
	s.maintenanceMu.Lock()
	defer s.maintenanceMu.Unlock()

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.db == nil {
		return CompressionStatus{}, ErrClosed
	}

	status, err := s.inspectCompression(ctx)
	if err != nil {
		return CompressionStatus{}, err
	}
	if !status.Available {
		return status, fmt.Errorf("%w: compression settings are unavailable for %s", ErrInvalidArgument, s.cfg.Driver)
	}

	mode := CompressionModeNone
	algorithm := ""
	if cfg.StorageEnabled {
		mode = strings.ToLower(strings.TrimSpace(cfg.StorageMode))
	}
	if s.cfg.Driver == DriverPostgreSQL && status.Storage.Mode == CompressionModeTimescaleDB {
		if err := validatePostgreSQLStorageTransition(status.Storage.Mode, mode, cfg.ConfirmIrreversible); err != nil {
			return status, err
		}
	}
	if cfg.StorageEnabled {
		option, ok := supportedCompressionOption(status.Storage.Options, mode)
		if !ok {
			return status, fmt.Errorf("%w: storage compression mode %q is not supported", ErrInvalidArgument, cfg.StorageMode)
		}
		algorithm, err = selectCompressionAlgorithm(option.Algorithms, cfg.StorageAlgorithm)
		if err != nil {
			return status, err
		}
	}

	switch s.cfg.Driver {
	case DriverMySQL:
		if cfg.WALEnabled || strings.TrimSpace(cfg.WALAlgorithm) != "" {
			return status, fmt.Errorf("%w: WAL compression is only available for PostgreSQL", ErrInvalidArgument)
		}
		if status.Storage.Mode != mode || !strings.EqualFold(status.Storage.Algorithm, algorithm) {
			if err := s.configureMySQLCompression(ctx, status, mode, algorithm); err != nil {
				return status, err
			}
		}
	case DriverPostgreSQL:
		walAlgorithm, walChanged, err := resolvePostgreSQLWALCompression(status.WAL, cfg.WALEnabled, cfg.WALAlgorithm)
		if err != nil {
			return status, err
		}
		if status.Storage.Mode != CompressionModeTimescaleDB {
			if err := validatePostgreSQLStorageTransition(status.Storage.Mode, mode, cfg.ConfirmIrreversible); err != nil {
				return status, err
			}
		}
		if status.Storage.Mode != mode || !strings.EqualFold(status.Storage.Algorithm, algorithm) {
			if err := s.configurePostgreSQLStorageCompression(ctx, mode, algorithm); err != nil {
				return status, err
			}
		}
		if walChanged {
			if err := s.setPostgreSQLWALCompression(ctx, walAlgorithm); err != nil {
				return status, err
			}
		}
	default:
		return status, fmt.Errorf("%w: compression settings are unavailable for %s", ErrInvalidArgument, s.cfg.Driver)
	}

	return s.inspectCompression(ctx)
}

func (s *Store) inspectCompression(ctx context.Context) (CompressionStatus, error) {
	switch s.cfg.Driver {
	case DriverMySQL:
		return s.inspectMySQLCompression(ctx)
	case DriverPostgreSQL:
		return s.inspectPostgreSQLCompression(ctx)
	case DriverSQLite:
		return CompressionStatus{
			Driver: s.cfg.Driver,
			Reason: compressionReasonLocalDatabase,
			Storage: StorageCompressionStatus{
				Mode:    CompressionModeNone,
				Options: []CompressionOption{},
			},
		}, nil
	default:
		return CompressionStatus{}, fmt.Errorf("%w: unsupported driver %q", ErrInvalidArgument, s.cfg.Driver)
	}
}

func (s *Store) inspectMySQLCompression(ctx context.Context) (CompressionStatus, error) {
	var version, versionComment, filePerTable string
	if err := s.db.QueryRowContext(ctx, `SELECT VERSION(), @@version_comment, CAST(@@innodb_file_per_table AS CHAR)`).Scan(&version, &versionComment, &filePerTable); err != nil {
		return CompressionStatus{}, fmt.Errorf("metric: inspect MySQL compression capabilities: %w", err)
	}

	filePerTableEnabled := filePerTable == "1" || strings.EqualFold(filePerTable, "on")
	isMariaDB := strings.Contains(strings.ToLower(version+" "+versionComment), "mariadb")
	rowOption := CompressionOption{
		Mode:       CompressionModeMySQLRow,
		Supported:  filePerTableEnabled,
		Algorithms: []string{},
	}
	pageOption := CompressionOption{
		Mode:       CompressionModeMySQLPage,
		Supported:  filePerTableEnabled && !isMariaDB && mysqlVersionAtLeast(version, 5, 7, 8),
		Algorithms: []string{"zlib"},
		Warning:    compressionWarningMySQLPageFilesystem,
	}
	if !filePerTableEnabled {
		rowOption.Reason = compressionReasonInnoDBFilePerTableDisabled
		pageOption.Reason = compressionReasonInnoDBFilePerTableDisabled
	} else if !pageOption.Supported {
		pageOption.Reason = compressionReasonMySQLPageUnsupported
	}

	states, err := s.mysqlCompressionTableStates(ctx)
	if err != nil {
		return CompressionStatus{}, err
	}
	mode, algorithm := detectMySQLCompressionMode(states)
	return CompressionStatus{
		Driver:    s.cfg.Driver,
		Available: true,
		Storage: StorageCompressionStatus{
			Enabled:   mode != CompressionModeNone,
			Mode:      mode,
			Algorithm: algorithm,
			Options:   []CompressionOption{rowOption, pageOption},
		},
	}, nil
}

func (s *Store) mysqlCompressionTableStates(ctx context.Context) ([]mysqlCompressionTableState, error) {
	names := managedTableNames(s.tables)
	placeholders := make([]string, len(names))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	query := fmt.Sprintf(`SELECT ROW_FORMAT, COALESCE(CREATE_OPTIONS, '')
FROM information_schema.TABLES
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME IN (%s)`, strings.Join(placeholders, ", "))
	rows, err := s.db.QueryContext(ctx, query, stringsToAny(names)...)
	if err != nil {
		return nil, fmt.Errorf("metric: inspect MySQL metric table compression: %w", err)
	}
	defer rows.Close()

	states := make([]mysqlCompressionTableState, 0, len(names))
	for rows.Next() {
		var state mysqlCompressionTableState
		if err := rows.Scan(&state.rowFormat, &state.createOptions); err != nil {
			return nil, fmt.Errorf("metric: scan MySQL metric table compression: %w", err)
		}
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("metric: read MySQL metric table compression: %w", err)
	}
	return states, nil
}

func (s *Store) configureMySQLCompression(ctx context.Context, status CompressionStatus, mode, algorithm string) error {
	pageSyntaxSupported := false
	for _, option := range status.Storage.Options {
		if option.Mode == CompressionModeMySQLPage && option.Supported {
			pageSyntaxSupported = true
		}
	}

	for _, table := range managedTableNames(s.tables) {
		quoted := quoteMaintenanceIdentifier(DriverMySQL, table)
		if mode != CompressionModeMySQLPage && pageSyntaxSupported {
			if _, err := s.db.ExecContext(ctx, "ALTER TABLE "+quoted+" COMPRESSION='none'"); err != nil {
				return fmt.Errorf("metric: disable MySQL page compression on %s: %w", table, err)
			}
		}

		var query string
		switch mode {
		case CompressionModeNone:
			query = "ALTER TABLE " + quoted + " ROW_FORMAT=DYNAMIC"
		case CompressionModeMySQLRow:
			query = "ALTER TABLE " + quoted + " ROW_FORMAT=COMPRESSED"
		case CompressionModeMySQLPage:
			query = "ALTER TABLE " + quoted + " ROW_FORMAT=DYNAMIC"
		default:
			return fmt.Errorf("%w: unsupported MySQL compression mode %q", ErrInvalidArgument, mode)
		}
		if _, err := s.db.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("metric: set MySQL compression mode on %s: %w", table, err)
		}
		if mode == CompressionModeMySQLPage {
			if _, err := s.db.ExecContext(ctx, "ALTER TABLE "+quoted+" COMPRESSION='"+algorithm+"'"); err != nil {
				return fmt.Errorf("metric: enable MySQL page compression on %s: %w", table, err)
			}
		}
	}

	if mode == CompressionModeMySQLPage || status.Storage.Mode == CompressionModeMySQLPage || status.Storage.Mode == CompressionModeMixed {
		query, err := managedReclaimQuery(DriverMySQL, s.tables)
		if err != nil {
			return err
		}
		if err := s.optimizeMySQLTables(ctx, query); err != nil {
			return fmt.Errorf("metric: rebuild MySQL tables after compression change: %w", err)
		}
	}
	return nil
}

func (s *Store) inspectPostgreSQLCompression(ctx context.Context) (CompressionStatus, error) {
	var serverVersion int
	if err := s.db.QueryRowContext(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&serverVersion); err != nil {
		return CompressionStatus{}, fmt.Errorf("metric: inspect PostgreSQL version: %w", err)
	}

	toastAlgorithms := []string{"pglz"}
	if serverVersion >= 140000 {
		algorithms, err := s.postgreSQLEnumSettingValues(ctx, "default_toast_compression")
		if err != nil {
			return CompressionStatus{}, err
		}
		if len(algorithms) > 0 {
			toastAlgorithms = algorithms
		}
	}
	mode, algorithm, err := s.postgreSQLStorageCompressionState(ctx, serverVersion, toastAlgorithms[0])
	if err != nil {
		return CompressionStatus{}, err
	}

	timescale, err := s.inspectPostgreSQLTimescaleCompression(ctx)
	if err != nil {
		return CompressionStatus{}, err
	}
	toastOption := CompressionOption{
		Mode:       CompressionModePostgreSQLToast,
		Supported:  true,
		Algorithms: toastAlgorithms,
		Warning:    compressionWarningPostgreSQLToast,
	}
	if timescale.active {
		mode = CompressionModeTimescaleDB
		if timescale.compressionActive {
			algorithm = timescaleColumnstoreAlgorithm
		} else {
			algorithm = ""
		}
		toastOption.Supported = false
		toastOption.Reason = compressionReasonTimescaleDBAlreadyEnabled
	}

	wal, err := s.inspectPostgreSQLWALCompression(ctx)
	if err != nil {
		return CompressionStatus{}, err
	}
	return CompressionStatus{
		Driver:    s.cfg.Driver,
		Available: true,
		Storage: StorageCompressionStatus{
			Enabled:   mode != CompressionModeNone,
			Mode:      mode,
			Algorithm: algorithm,
			Options: []CompressionOption{
				toastOption,
				timescale.option(),
			},
		},
		WAL: wal,
	}, nil
}

func (s *Store) postgreSQLStorageCompressionState(ctx context.Context, serverVersion int, defaultAlgorithm string) (string, string, error) {
	tables := postgreSQLCompressionTables(s.tables)
	names := make([]string, len(tables))
	expected := make(map[string]map[string]bool, len(tables))
	for i, table := range tables {
		names[i] = table.name
		expected[table.name] = make(map[string]bool, len(table.columns))
		for _, column := range table.columns {
			expected[table.name][column] = true
		}
	}
	placeholders := make([]string, len(names))
	args := make([]any, len(names))
	for i, name := range names {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = name
	}
	compressionColumn := `''`
	if serverVersion >= 140000 {
		compressionColumn = "a.attcompression::text"
	}
	query := fmt.Sprintf(`SELECT c.relname, a.attname, a.attstorage::text, %s
FROM pg_catalog.pg_attribute AS a
JOIN pg_catalog.pg_class AS c ON c.oid = a.attrelid
JOIN pg_catalog.pg_namespace AS n ON n.oid = c.relnamespace
WHERE n.nspname = current_schema()
  AND c.relname IN (%s)
  AND a.attnum > 0 AND NOT a.attisdropped`, compressionColumn, strings.Join(placeholders, ", "))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return "", "", fmt.Errorf("metric: inspect PostgreSQL column compression: %w", err)
	}
	defer rows.Close()

	states := make([]postgreSQLCompressionColumnState, 0)
	for rows.Next() {
		var table, column string
		var state postgreSQLCompressionColumnState
		if err := rows.Scan(&table, &column, &state.storage, &state.compression); err != nil {
			return "", "", fmt.Errorf("metric: scan PostgreSQL column compression: %w", err)
		}
		if expected[table][column] {
			states = append(states, state)
		}
	}
	if err := rows.Err(); err != nil {
		return "", "", fmt.Errorf("metric: read PostgreSQL column compression: %w", err)
	}
	return detectPostgreSQLCompressionMode(states, defaultAlgorithm)
}

func (s *Store) inspectPostgreSQLWALCompression(ctx context.Context) (*WALCompressionStatus, error) {
	var current, valueType, settingContext string
	if err := s.db.QueryRowContext(ctx, `SELECT setting, vartype, context FROM pg_settings WHERE name = 'wal_compression'`).Scan(&current, &valueType, &settingContext); err != nil {
		if err == sql.ErrNoRows {
			return &WALCompressionStatus{Algorithms: []string{}}, nil
		}
		return nil, fmt.Errorf("metric: inspect PostgreSQL WAL compression: %w", err)
	}

	algorithms := []string{"on"}
	if valueType == "enum" {
		values, err := s.postgreSQLEnumSettingValues(ctx, "wal_compression")
		if err != nil {
			return nil, err
		}
		algorithms = filterEnabledCompressionAlgorithms(values)
	}

	configured, err := s.postgreSQLDatabaseSetting(ctx, "wal_compression", current)
	if err != nil {
		return nil, err
	}
	var databaseOwner bool
	if err := s.db.QueryRowContext(ctx, `SELECT pg_has_role(current_user, datdba, 'MEMBER')
FROM pg_database WHERE datname = current_database()`).Scan(&databaseOwner); err != nil {
		return nil, fmt.Errorf("metric: inspect PostgreSQL database ownership: %w", err)
	}
	var superuser bool
	if err := s.db.QueryRowContext(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&superuser); err != nil {
		return nil, fmt.Errorf("metric: inspect PostgreSQL role privileges: %w", err)
	}
	parameterAllowed := settingContext == "user" || superuser
	if !parameterAllowed {
		var serverVersion int
		if err := s.db.QueryRowContext(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&serverVersion); err != nil {
			return nil, fmt.Errorf("metric: inspect PostgreSQL version for parameter privileges: %w", err)
		}
		if serverVersion >= 150000 {
			if err := s.db.QueryRowContext(ctx, `SELECT has_parameter_privilege(current_user, 'wal_compression', 'SET')`).Scan(&parameterAllowed); err != nil {
				return nil, fmt.Errorf("metric: inspect PostgreSQL WAL parameter privilege: %w", err)
			}
		}
	}

	enabled := !isCompressionDisabled(configured)
	if enabled && !containsFold(algorithms, configured) {
		algorithms = append(algorithms, strings.ToLower(configured))
	}
	status := &WALCompressionStatus{
		Visible:           true,
		Supported:         databaseOwner && parameterAllowed && len(algorithms) > 0,
		Enabled:           enabled,
		Algorithm:         strings.ToLower(configured),
		Algorithms:        algorithms,
		Warning:           compressionWarningWALConnections,
		RequiresReconnect: true,
	}
	if !databaseOwner {
		status.Reason = compressionReasonDatabaseOwnerRequired
	} else if !parameterAllowed {
		status.Reason = compressionReasonWALPermissionRequired
	}
	return status, nil
}

func (s *Store) postgreSQLEnumSettingValues(ctx context.Context, name string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT unnest(enumvals) FROM pg_settings WHERE name = $1`, name)
	if err != nil {
		return nil, fmt.Errorf("metric: inspect PostgreSQL setting %s values: %w", name, err)
	}
	defer rows.Close()
	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("metric: scan PostgreSQL setting %s value: %w", name, err)
		}
		values = append(values, strings.ToLower(value))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("metric: read PostgreSQL setting %s values: %w", name, err)
	}
	return values, nil
}

func (s *Store) postgreSQLDatabaseSetting(ctx context.Context, name, fallback string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE((
  SELECT split_part(setting, '=', 2)
  FROM pg_db_role_setting AS settings
  CROSS JOIN LATERAL unnest(settings.setconfig) AS setting
  WHERE settings.setdatabase = (SELECT oid FROM pg_database WHERE datname = current_database())
    AND settings.setrole = 0
    AND setting LIKE $1
  LIMIT 1
), $2)`, name+"=%", fallback).Scan(&value)
	if err != nil {
		return "", fmt.Errorf("metric: inspect PostgreSQL database setting %s: %w", name, err)
	}
	return value, nil
}

func (s *Store) configurePostgreSQLStorageCompression(ctx context.Context, mode, algorithm string) error {
	if mode == CompressionModeTimescaleDB {
		return s.configureTimescaleDBCompression(ctx)
	}
	if mode != CompressionModeNone && mode != CompressionModePostgreSQLToast {
		return fmt.Errorf("%w: unsupported PostgreSQL storage compression mode %q", ErrInvalidArgument, mode)
	}
	var serverVersion int
	if err := s.db.QueryRowContext(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&serverVersion); err != nil {
		return fmt.Errorf("metric: inspect PostgreSQL version before compression change: %w", err)
	}

	tables := postgreSQLCompressionTables(s.tables)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("metric: begin PostgreSQL compression change: %w", err)
	}
	defer tx.Rollback()
	for _, table := range tables {
		actions := make([]string, 0, len(table.columns)*2)
		for _, column := range table.columns {
			quotedColumn := quoteMaintenanceIdentifier(DriverPostgreSQL, column)
			if mode == CompressionModeNone {
				actions = append(actions, "ALTER COLUMN "+quotedColumn+" SET STORAGE EXTERNAL")
				continue
			}
			actions = append(actions, "ALTER COLUMN "+quotedColumn+" SET STORAGE EXTENDED")
			if serverVersion >= 140000 {
				actions = append(actions, "ALTER COLUMN "+quotedColumn+" SET COMPRESSION "+algorithm)
			}
		}
		query := "ALTER TABLE " + quoteMaintenanceIdentifier(DriverPostgreSQL, table.name) + " " + strings.Join(actions, ", ")
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("metric: set PostgreSQL compression on %s: %w", table.name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("metric: commit PostgreSQL compression change: %w", err)
	}
	return nil
}

func validatePostgreSQLStorageTransition(currentMode, requestedMode string, confirmed bool) error {
	if currentMode == CompressionModeTimescaleDB && requestedMode != CompressionModeTimescaleDB {
		return fmt.Errorf("%w: TimescaleDB hypertable conversion is irreversible and cannot be disabled or changed to TOAST", ErrInvalidArgument)
	}
	if currentMode != CompressionModeTimescaleDB && requestedMode == CompressionModeTimescaleDB && !confirmed {
		return fmt.Errorf("%w: TimescaleDB hypertable conversion requires explicit irreversible-operation confirmation", ErrInvalidArgument)
	}
	return nil
}

func resolvePostgreSQLWALCompression(status *WALCompressionStatus, enabled bool, requestedAlgorithm string) (string, bool, error) {
	if status == nil || !status.Visible {
		if enabled || strings.TrimSpace(requestedAlgorithm) != "" {
			return "", false, fmt.Errorf("%w: PostgreSQL WAL compression is unavailable", ErrInvalidArgument)
		}
		return "off", false, nil
	}

	if !enabled {
		if !status.Enabled {
			return "off", false, nil
		}
		if !status.Supported {
			return "", false, fmt.Errorf("%w: PostgreSQL WAL compression cannot be changed by the current database user", ErrInvalidArgument)
		}
		return "off", true, nil
	}

	requestedAlgorithm = strings.ToLower(strings.TrimSpace(requestedAlgorithm))
	if requestedAlgorithm == "" && status.Enabled {
		requestedAlgorithm = strings.ToLower(status.Algorithm)
	}
	algorithm, err := selectCompressionAlgorithm(status.Algorithms, requestedAlgorithm)
	if err != nil {
		return "", false, err
	}
	if status.Enabled && strings.EqualFold(status.Algorithm, algorithm) {
		return algorithm, false, nil
	}
	if !status.Supported {
		return "", false, fmt.Errorf("%w: PostgreSQL WAL compression cannot be changed by the current database user", ErrInvalidArgument)
	}
	return algorithm, true, nil
}

func (s *Store) setPostgreSQLWALCompression(ctx context.Context, algorithm string) error {
	var databaseName string
	if err := s.db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&databaseName); err != nil {
		return fmt.Errorf("metric: inspect PostgreSQL database name: %w", err)
	}
	query := "ALTER DATABASE " + quoteMaintenanceIdentifier(DriverPostgreSQL, databaseName) + " SET wal_compression = '" + algorithm + "'"
	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("metric: configure PostgreSQL WAL compression: %w", err)
	}
	return nil
}

func supportedCompressionOption(options []CompressionOption, mode string) (CompressionOption, bool) {
	for _, option := range options {
		if option.Mode == mode && option.Supported {
			return option, true
		}
	}
	return CompressionOption{}, false
}

func selectCompressionAlgorithm(available []string, requested string) (string, error) {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if len(available) == 0 {
		if requested != "" {
			return "", fmt.Errorf("%w: this compression mode has no algorithm option", ErrInvalidArgument)
		}
		return "", nil
	}
	if requested == "" {
		return available[0], nil
	}
	if !containsFold(available, requested) {
		return "", fmt.Errorf("%w: compression algorithm %q is not supported", ErrInvalidArgument, requested)
	}
	return requested, nil
}

func detectMySQLCompressionMode(states []mysqlCompressionTableState) (string, string) {
	if len(states) == 0 {
		return CompressionModeNone, ""
	}
	mode, algorithm := mysqlCompressionTableMode(states[0])
	for _, state := range states[1:] {
		nextMode, nextAlgorithm := mysqlCompressionTableMode(state)
		if nextMode != mode || !strings.EqualFold(nextAlgorithm, algorithm) {
			return CompressionModeMixed, ""
		}
	}
	return mode, algorithm
}

func mysqlCompressionTableMode(state mysqlCompressionTableState) (string, string) {
	if algorithm := mysqlCreateOptionCompression(state.createOptions); algorithm != "" && algorithm != "none" {
		return CompressionModeMySQLPage, algorithm
	}
	if strings.EqualFold(strings.TrimSpace(state.rowFormat), "compressed") {
		return CompressionModeMySQLRow, ""
	}
	return CompressionModeNone, ""
}

func mysqlCreateOptionCompression(options string) string {
	lower := strings.ToLower(options)
	index := strings.Index(lower, "compression=")
	if index < 0 {
		return ""
	}
	value := strings.TrimSpace(lower[index+len("compression="):])
	value = strings.TrimLeft(value, "'\"")
	if end := strings.IndexAny(value, "'\" ,"); end >= 0 {
		value = value[:end]
	}
	return strings.TrimSpace(value)
}

func mysqlVersionAtLeast(raw string, major, minor, patch int) bool {
	parts := strings.SplitN(raw, ".", 4)
	if len(parts) < 2 {
		return false
	}
	parsed := [3]int{}
	for i := 0; i < len(parsed) && i < len(parts); i++ {
		digits := strings.TrimLeftFunc(parts[i], func(r rune) bool { return r < '0' || r > '9' })
		end := strings.IndexFunc(digits, func(r rune) bool { return r < '0' || r > '9' })
		if end >= 0 {
			digits = digits[:end]
		}
		value, err := strconv.Atoi(digits)
		if err != nil {
			return false
		}
		parsed[i] = value
	}
	want := [3]int{major, minor, patch}
	for i := range parsed {
		if parsed[i] != want[i] {
			return parsed[i] > want[i]
		}
	}
	return true
}

func postgreSQLCompressionTables(t tables) []postgreSQLCompressionTable {
	return []postgreSQLCompressionTable{
		{name: strings.ToLower(t.definitions), columns: []string{"metadata"}},
		{name: strings.ToLower(t.points), columns: []string{"tags", "labels"}},
		{name: strings.ToLower(t.rollups), columns: []string{"tags", "digest"}},
	}
}

func detectPostgreSQLCompressionMode(states []postgreSQLCompressionColumnState, defaultAlgorithm string) (string, string, error) {
	if len(states) == 0 {
		return CompressionModeNone, "", nil
	}
	mode, algorithm := postgreSQLCompressionColumnMode(states[0], defaultAlgorithm)
	for _, state := range states[1:] {
		nextMode, nextAlgorithm := postgreSQLCompressionColumnMode(state, defaultAlgorithm)
		if nextMode != mode || !strings.EqualFold(nextAlgorithm, algorithm) {
			return CompressionModeMixed, "", nil
		}
	}
	return mode, algorithm, nil
}

func postgreSQLCompressionColumnMode(state postgreSQLCompressionColumnState, defaultAlgorithm string) (string, string) {
	if state.storage == "e" {
		return CompressionModeNone, ""
	}
	switch state.compression {
	case "l":
		return CompressionModePostgreSQLToast, "lz4"
	case "p":
		return CompressionModePostgreSQLToast, "pglz"
	default:
		return CompressionModePostgreSQLToast, strings.ToLower(defaultAlgorithm)
	}
}

func filterEnabledCompressionAlgorithms(values []string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if !isCompressionDisabled(value) {
			filtered = append(filtered, strings.ToLower(value))
		}
	}
	return filtered
}

func isCompressionDisabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "0", "false", "off", "none":
		return true
	default:
		return false
	}
}

func containsFold(values []string, value string) bool {
	for _, candidate := range values {
		if strings.EqualFold(candidate, value) {
			return true
		}
	}
	return false
}
