package accounts

import (
	"os"
	"testing"

	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
)

// TestMain wires a shared in-memory SQLite database for the accounts tests.
func TestMain(m *testing.M) {
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:komari_accounts_test?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	os.Exit(m.Run())
}

// resetAccountsState clears users and sessions so each test starts clean.
func resetAccountsState(t *testing.T) {
	t.Helper()
	clear := func() {
		db := dbcore.GetDBInstance()
		if err := db.Where("1 = 1").Delete(&models.User{}).Error; err != nil {
			t.Fatalf("clear users: %v", err)
		}
		if err := db.Where("1 = 1").Delete(&models.Session{}).Error; err != nil {
			t.Fatalf("clear sessions: %v", err)
		}
	}
	clear()
	t.Cleanup(clear)
}

// countSessions returns how many session rows match the query.
func countSessions(t *testing.T, query string, args ...any) int64 {
	t.Helper()
	var count int64
	if err := dbcore.GetDBInstance().Model(&models.Session{}).Where(query, args...).Count(&count).Error; err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return count
}
