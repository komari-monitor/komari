package models

import (
	"database/sql/driver"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// LocalTime is a custom time type for GORM to handle time.Time correctly.
// It ensures that all times are stored and retrieved based on the application's configured timezone.
type LocalTime time.Time

// Value implements the driver.Valuer interface.
// It converts UTCTime to a TEXT format that the database understands.
// The time is formatted as a string in the application's local timezone, without timezone information.
func (t LocalTime) Value() (driver.Value, error) {
	if time.Time(t).IsZero() {
		return nil, nil
	}
	return time.Time(t).In(GetAppLocation()).Format("2006-01-02 15:04:05.0000000"), nil
}

// Scan implements the sql.Scanner interface.
// It scans a value from the database into a UTCTime object.
func (t *LocalTime) Scan(v interface{}) error {
	if v == nil {
		*t = LocalTime(time.Time{})
		return nil
	}

	loc := GetAppLocation()

	switch val := v.(type) {
	case time.Time:
		// CRITICAL FIX: When the driver reads a timezone-less string (e.g., "16:00:00"),
		// it often incorrectly assumes it's UTC. We must correct this by re-interpreting
		// the date and clock values in the application's actual timezone.
		year, month, day := val.Date()
		hour, min, sec := val.Clock()
		nanosec := val.Nanosecond()
		*t = LocalTime(time.Date(year, month, day, hour, min, sec, nanosec, loc))
		return nil
	case []byte:
		return t.parseTime(string(val), loc)
	case string:
		return t.parseTime(val, loc)
	default:
		return fmt.Errorf("UTCTime scan source was not string, []byte or time.Time: %T (%v)", v, v)
	}
}

// parseTime handles parsing a string into UTCTime.
func (t *LocalTime) parseTime(timeStr string, loc *time.Location) error {
	timeStr = strings.TrimSpace(timeStr)
	if timeStr == "" {
		*t = LocalTime(time.Time{})
		return nil
	}

	layouts := []string{
		time.RFC3339Nano, time.RFC3339,
		"2006-01-02 15:04:05.0000000-07:00", "2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05.0000000", "2006-01-02 15:04:05", "2006-01-02",
	}

	for _, layout := range layouts {
		if parsedTime, err := time.ParseInLocation(layout, timeStr, loc); err == nil {
			*t = LocalTime(parsedTime)
			return nil
		}
	}
	return fmt.Errorf("unable to parse time string '%s' into UTCTime", timeStr)
}

// MarshalJSON implements the json.Marshaler interface.
// Serializes UTCTime to JSON in RFC3339 format with the correct timezone offset.
func (t LocalTime) MarshalJSON() ([]byte, error) {
	if time.Time(t).IsZero() {
		return []byte("null"), nil
	}
	formattedTime := time.Time(t).In(GetAppLocation()).Format(time.RFC3339)
	return []byte(fmt.Sprintf(`"%s"`, formattedTime)), nil
}

// ToTime converts UTCTime to Go's native time.Time type.
func (t LocalTime) ToTime() time.Time { return time.Time(t) }

// FromTime converts Go's native time.Time type to UTCTime.
func FromTime(t time.Time) LocalTime { return LocalTime(t) }

// Now returns the current time in the application's configured location.
func Now() LocalTime { return LocalTime(time.Now().In(GetAppLocation())) }

var (
	appLocation  *time.Location
	locationOnce sync.Once
)

// GetAppLocation retrieves the application's global timezone from the "TZ" environment variable.
func GetAppLocation() *time.Location {
	locationOnce.Do(func() {
		tz := os.Getenv("TZ")
		if tz == "" {
			tz = "UTC"
		}
		loc, err := time.LoadLocation(tz)
		if err != nil {
			log.Printf("Warning: Failed to load timezone '%s', falling back to UTC. Error: %v", tz, err)
			appLocation = time.UTC
		} else {
			appLocation = loc
		}
		time.Local = appLocation
		log.Printf("Application timezone is set to '%s'.", appLocation.String())
	})
	return appLocation
}
