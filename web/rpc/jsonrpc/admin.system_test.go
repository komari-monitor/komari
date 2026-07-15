package jsonrpc

import (
	"strings"
	"sync"
	"testing"

	"github.com/komari-monitor/komari/database/models"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
	gormtests "gorm.io/gorm/utils/tests"
)

func TestLogSchemaDefinesQueryIndexes(t *testing.T) {
	parsed, err := schema.Parse(&models.Log{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("parse log schema: %v", err)
	}
	indexes := make(map[string]*schema.Index)
	for _, index := range parsed.ParseIndexes() {
		indexes[index.Name] = index
	}
	for _, name := range []string{"idx_logs_time", "idx_logs_msg_type_time"} {
		if indexes[name] == nil {
			t.Fatalf("expected log index %q to be defined", name)
		}
	}
	composite := indexes["idx_logs_msg_type_time"]
	if len(composite.Fields) != 2 || composite.Fields[0].Field.Name != "MsgType" || composite.Fields[1].Field.Name != "Time" {
		t.Fatalf("unexpected composite log index fields: %#v", composite.Fields)
	}
}

func TestFilterAdminLogsByMessageType(t *testing.T) {
	db, err := gorm.Open(gormtests.DummyDialector{}, &gorm.Config{
		DryRun:               true,
		DisableAutomaticPing: true,
		Logger:               logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open dry-run database: %v", err)
	}

	var logs []models.Log
	statement := filterAdminLogsByMessageType(db.Model(&models.Log{}), " visitor ").Find(&logs).Statement
	if sql := statement.SQL.String(); !strings.Contains(sql, "WHERE msg_type = ?") {
		t.Fatalf("filtered SQL missing message type predicate: %s", sql)
	}
	if len(statement.Vars) != 1 || statement.Vars[0] != "visitor" {
		t.Fatalf("unexpected filter variables: %#v", statement.Vars)
	}
}
