package models

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestTimeFieldsStoreAndSerializeAsUTCWithNanoseconds(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger:  logger.Default.LogMode(logger.Silent),
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := db.AutoMigrate(&Log{}); err != nil {
		t.Fatalf("migrate log: %v", err)
	}

	stamp := time.Date(2026, 7, 17, 1, 2, 3, 123456789, time.UTC)
	entry := Log{IP: "127.0.0.1", UUID: "test", Message: "test", MsgType: "info", Time: stamp}
	if err := db.Create(&entry).Error; err != nil {
		t.Fatalf("create log: %v", err)
	}
	var loaded Log
	if err := db.First(&loaded, entry.ID).Error; err != nil {
		t.Fatalf("load log: %v", err)
	}
	if !loaded.Time.Equal(stamp) || loaded.Time.Nanosecond() != stamp.Nanosecond() {
		t.Fatalf("loaded time = %s, want %s", loaded.Time, stamp)
	}

	encoded, err := json.Marshal(loaded)
	if err != nil {
		t.Fatalf("marshal log: %v", err)
	}
	if !strings.Contains(string(encoded), `"time":"2026-07-17T01:02:03.123456789Z"`) {
		t.Fatalf("unexpected JSON: %s", encoded)
	}
}

func TestNullableTimeFieldReadsAndSerializesNull(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := db.AutoMigrate(&Client{}); err != nil {
		t.Fatalf("migrate client: %v", err)
	}
	if err := db.Exec(`INSERT INTO clients (uuid, token, expired_at) VALUES (?, ?, NULL)`, "nullable", "token").Error; err != nil {
		t.Fatalf("insert client: %v", err)
	}

	var client Client
	if err := db.First(&client, "uuid = ?", "nullable").Error; err != nil {
		t.Fatalf("load client: %v", err)
	}
	if client.ExpiredAt != nil {
		t.Fatalf("expired_at = %s, want nil", client.ExpiredAt)
	}
	encoded, err := json.Marshal(client)
	if err != nil {
		t.Fatalf("marshal client: %v", err)
	}
	if !strings.Contains(string(encoded), `"expired_at":null`) {
		t.Fatalf("unexpected JSON: %s", encoded)
	}
}
