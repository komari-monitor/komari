package metric

import (
	"context"
	"errors"
	"testing"
)

func TestSQLiteCompressionUnavailable(t *testing.T) {
	store, err := Open(context.Background(), SQLite(":memory:"))
	if err != nil {
		t.Fatalf("open SQLite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	status, err := store.InspectCompression(context.Background())
	if err != nil {
		t.Fatalf("inspect SQLite compression: %v", err)
	}
	if status.Available || status.Driver != DriverSQLite || status.Reason != compressionReasonLocalDatabase {
		t.Fatalf("unexpected SQLite compression status: %#v", status)
	}
	_, err = store.ConfigureCompression(context.Background(), CompressionConfig{
		StorageEnabled: true,
		StorageMode:    CompressionModeMySQLRow,
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("configure SQLite compression error = %v, want %v", err, ErrInvalidArgument)
	}
}

func TestDetectMySQLCompressionMode(t *testing.T) {
	tests := []struct {
		name      string
		states    []mysqlCompressionTableState
		mode      string
		algorithm string
	}{
		{name: "none", states: []mysqlCompressionTableState{{rowFormat: "Dynamic"}, {rowFormat: "Compact"}}, mode: CompressionModeNone},
		{name: "row", states: []mysqlCompressionTableState{{rowFormat: "Compressed"}, {rowFormat: "COMPRESSED"}}, mode: CompressionModeMySQLRow},
		{name: "page", states: []mysqlCompressionTableState{{rowFormat: "Dynamic", createOptions: `COMPRESSION="zlib"`}, {createOptions: `compression='ZLIB'`}}, mode: CompressionModeMySQLPage, algorithm: "zlib"},
		{name: "mixed", states: []mysqlCompressionTableState{{rowFormat: "Compressed"}, {rowFormat: "Dynamic"}}, mode: CompressionModeMixed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mode, algorithm := detectMySQLCompressionMode(test.states)
			if mode != test.mode || algorithm != test.algorithm {
				t.Fatalf("mode = %q/%q, want %q/%q", mode, algorithm, test.mode, test.algorithm)
			}
		})
	}
}

func TestMySQLVersionAtLeast(t *testing.T) {
	for _, test := range []struct {
		version string
		want    bool
	}{
		{version: "5.7.8", want: true},
		{version: "5.7.7-log", want: false},
		{version: "8.0.36", want: true},
		{version: "10.11.6-MariaDB", want: true},
		{version: "invalid", want: false},
	} {
		if got := mysqlVersionAtLeast(test.version, 5, 7, 8); got != test.want {
			t.Errorf("mysqlVersionAtLeast(%q) = %t, want %t", test.version, got, test.want)
		}
	}
}

func TestDetectPostgreSQLCompressionMode(t *testing.T) {
	tests := []struct {
		name      string
		states    []postgreSQLCompressionColumnState
		mode      string
		algorithm string
	}{
		{name: "disabled", states: []postgreSQLCompressionColumnState{{storage: "e"}, {storage: "e"}}, mode: CompressionModeNone},
		{name: "default", states: []postgreSQLCompressionColumnState{{storage: "x"}, {storage: "x"}}, mode: CompressionModePostgreSQLToast, algorithm: "pglz"},
		{name: "lz4", states: []postgreSQLCompressionColumnState{{storage: "x", compression: "l"}, {storage: "x", compression: "l"}}, mode: CompressionModePostgreSQLToast, algorithm: "lz4"},
		{name: "mixed", states: []postgreSQLCompressionColumnState{{storage: "e"}, {storage: "x", compression: "p"}}, mode: CompressionModeMixed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mode, algorithm, err := detectPostgreSQLCompressionMode(test.states, "pglz")
			if err != nil {
				t.Fatalf("detect compression: %v", err)
			}
			if mode != test.mode || algorithm != test.algorithm {
				t.Fatalf("mode = %q/%q, want %q/%q", mode, algorithm, test.mode, test.algorithm)
			}
		})
	}
}

func TestSelectCompressionAlgorithm(t *testing.T) {
	if got, err := selectCompressionAlgorithm([]string{"pglz", "lz4"}, ""); err != nil || got != "pglz" {
		t.Fatalf("default algorithm = %q, %v", got, err)
	}
	if got, err := selectCompressionAlgorithm([]string{"pglz", "lz4"}, "LZ4"); err != nil || got != "lz4" {
		t.Fatalf("selected algorithm = %q, %v", got, err)
	}
	if _, err := selectCompressionAlgorithm([]string{"pglz"}, "zstd"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("unsupported algorithm error = %v", err)
	}
}

func TestResolvePostgreSQLWALCompression(t *testing.T) {
	readOnlyEnabled := &WALCompressionStatus{
		Visible:    true,
		Supported:  false,
		Enabled:    true,
		Algorithm:  "pglz",
		Algorithms: []string{"pglz", "lz4"},
	}
	algorithm, changed, err := resolvePostgreSQLWALCompression(readOnlyEnabled, true, "pglz")
	if err != nil || changed || algorithm != "pglz" {
		t.Fatalf("unchanged read-only WAL = %q, changed=%t, err=%v", algorithm, changed, err)
	}
	if _, _, err := resolvePostgreSQLWALCompression(readOnlyEnabled, false, ""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("read-only WAL disable error = %v, want %v", err, ErrInvalidArgument)
	}

	writableDisabled := &WALCompressionStatus{
		Visible:    true,
		Supported:  true,
		Enabled:    false,
		Algorithm:  "off",
		Algorithms: []string{"pglz", "lz4"},
	}
	algorithm, changed, err = resolvePostgreSQLWALCompression(writableDisabled, true, "")
	if err != nil || !changed || algorithm != "pglz" {
		t.Fatalf("enable WAL = %q, changed=%t, err=%v", algorithm, changed, err)
	}
	algorithm, changed, err = resolvePostgreSQLWALCompression(writableDisabled, false, "pglz")
	if err != nil || changed || algorithm != "off" {
		t.Fatalf("unchanged disabled WAL = %q, changed=%t, err=%v", algorithm, changed, err)
	}
}

func TestValidatePostgreSQLStorageTransition(t *testing.T) {
	if err := validatePostgreSQLStorageTransition(CompressionModeNone, CompressionModeTimescaleDB, false); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("unconfirmed conversion error = %v, want %v", err, ErrInvalidArgument)
	}
	if err := validatePostgreSQLStorageTransition(CompressionModeNone, CompressionModeTimescaleDB, true); err != nil {
		t.Fatalf("confirmed conversion returned an error: %v", err)
	}
	if err := validatePostgreSQLStorageTransition(CompressionModeTimescaleDB, CompressionModeNone, true); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("irreversible disable error = %v, want %v", err, ErrInvalidArgument)
	}
	if err := validatePostgreSQLStorageTransition(CompressionModeTimescaleDB, CompressionModePostgreSQLToast, true); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("irreversible TOAST switch error = %v, want %v", err, ErrInvalidArgument)
	}
}

func TestTimescaleCompressionFlavorForVersion(t *testing.T) {
	tests := []struct {
		version string
		want    timescaleCompressionFlavor
	}{
		{version: "1.7.5", want: timescaleCompressionUnsupported},
		{version: "2.0.0", want: timescaleCompressionLegacy},
		{version: "2.17.2", want: timescaleCompressionLegacy},
		{version: "2.18.0", want: timescaleCompressionHypercore},
		{version: "2.19.1-dev", want: timescaleCompressionHypercore},
		{version: "invalid", want: timescaleCompressionUnsupported},
	}
	for _, test := range tests {
		if got := timescaleCompressionFlavorForVersion(test.version); got != test.want {
			t.Errorf("timescaleCompressionFlavorForVersion(%q) = %d, want %d", test.version, got, test.want)
		}
	}
}

func TestTimescaleCompressionTables(t *testing.T) {
	tables := timescaleCompressionTables(tables{points: "custom_points", rollups: "custom_rollups"})
	if len(tables) != 2 {
		t.Fatalf("table plan length = %d, want 2", len(tables))
	}
	if tables[0].name != "custom_points" || tables[0].timeColumn != "ts_nano" || len(tables[0].segmentColumns) != 3 {
		t.Fatalf("unexpected points plan: %#v", tables[0])
	}
	if tables[1].name != "custom_rollups" || tables[1].timeColumn != "bucket_nano" || len(tables[1].segmentColumns) != 4 {
		t.Fatalf("unexpected rollups plan: %#v", tables[1])
	}
}
