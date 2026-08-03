package metric

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

type sqliteForeignKey struct {
	table    string
	from     string
	to       string
	onDelete string
}

func TestNormalizedSchemaDeclaresPortableForeignKeys(t *testing.T) {
	for _, driver := range []Driver{DriverSQLite, DriverMySQL, DriverPostgreSQL} {
		t.Run(string(driver), func(t *testing.T) {
			s := schemaTestStore(driver, "er_")
			ddl := strings.Join(s.normalizedSchemaStatements(), "\n")
			for _, clause := range []string{
				"FOREIGN KEY (metric_name) REFERENCES er_definitions(name) ON DELETE CASCADE",
				"FOREIGN KEY (series_id) REFERENCES er_series(id) ON DELETE CASCADE",
				"FOREIGN KEY (resolution_id) REFERENCES er_resolutions(id) ON DELETE CASCADE",
				"FOREIGN KEY (label_id) REFERENCES er_label_sets(id) ON DELETE CASCADE",
			} {
				if !strings.Contains(ddl, clause) {
					t.Fatalf("%s schema is missing %q:\n%s", driver, clause, ddl)
				}
			}
		})
	}
}

func TestSQLiteSchemaExposesERRelationships(t *testing.T) {
	ctx := context.Background()
	store := newMemStore(t)

	var enabled int
	if err := store.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled); err != nil {
		t.Fatalf("query foreign key enforcement: %v", err)
	}
	if enabled != 1 {
		t.Fatalf("foreign key enforcement = %d, want 1", enabled)
	}

	want := map[string]sqliteForeignKey{
		"metric_name":   {table: store.tables.definitions, from: "metric_name", to: "name", onDelete: "CASCADE"},
		"series_id":     {table: store.tables.series, from: "series_id", to: "id", onDelete: "CASCADE"},
		"resolution_id": {table: store.tables.resolutions, from: "resolution_id", to: "id", onDelete: "CASCADE"},
		"label_id":      {table: store.tables.labels, from: "label_id", to: "id", onDelete: "CASCADE"},
	}
	got := make(map[string]sqliteForeignKey)
	for _, table := range []string{store.tables.series, store.tables.rollups} {
		rows, err := store.db.QueryContext(ctx, "PRAGMA foreign_key_list("+table+")")
		if err != nil {
			t.Fatalf("list foreign keys for %s: %v", table, err)
		}
		for rows.Next() {
			var id, seq int
			var fk sqliteForeignKey
			var onUpdate, match string
			if err := rows.Scan(&id, &seq, &fk.table, &fk.from, &fk.to, &onUpdate, &fk.onDelete, &match); err != nil {
				_ = rows.Close()
				t.Fatalf("scan foreign key for %s: %v", table, err)
			}
			got[fk.from] = fk
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("close foreign key rows for %s: %v", table, err)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("foreign keys = %#v, want %#v", got, want)
	}
	for column, expected := range want {
		if actual := got[column]; actual != expected {
			t.Fatalf("foreign key for %s = %#v, want %#v", column, actual, expected)
		}
	}

	if err := store.CreateMetric(ctx, Definition{Name: "cascade", Type: TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatalf("create metric: %v", err)
	}
	at := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	if err := store.WriteBatch(ctx, []Point{{MetricName: "cascade", EntityID: "node-1", Timestamp: at, Value: 1}}); err != nil {
		t.Fatalf("write metric: %v", err)
	}
	if err := store.flushAllHotRollups(ctx); err != nil {
		t.Fatalf("flush rollup: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE name = ?", store.tables.definitions), "cascade"); err != nil {
		t.Fatalf("delete definition: %v", err)
	}
	for _, table := range []string{store.tables.series, store.tables.rollups} {
		var count int
		if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatalf("count %s after cascade: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s rows after cascade = %d, want 0", table, count)
		}
	}
}

func schemaTestStore(driver Driver, prefix string) *Store {
	return &Store{
		cfg:     Config{Driver: driver, TablePrefix: prefix},
		dialect: newDialect(driver),
		tables: tables{
			definitions: prefix + "definitions",
			series:      prefix + "series",
			labels:      prefix + "label_sets",
			resolutions: prefix + "resolutions",
			rollups:     prefix + "rollups",
			state:       prefix + "store_state",
		},
	}
}
