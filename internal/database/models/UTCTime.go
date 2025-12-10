package models

import (
	"database/sql/driver"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

// LocalTime is a custom time type for GORM to handle time.Time correctly.
// CORE PRINCIPLE: Database always stores UTC time. Business logic always uses UTC.
// Only JSON output converts to user's display timezone.
// This ensures correct time comparisons in SQL queries (WHERE time < xxx).
type LocalTime time.Time

// Value implements the driver.Valuer interface.
// Always stores time in UTC format to ensure correct database comparisons.
func (t LocalTime) Value() (driver.Value, error) {
	if time.Time(t).IsZero() {
		return nil, nil
	}
	// Always store as UTC to ensure correct SQL comparisons
	return time.Time(t).UTC().Format("2006-01-02 15:04:05.0000000"), nil
}

// Scan implements the sql.Scanner interface.
// Reads time from database as UTC (since we always store UTC).
func (t *LocalTime) Scan(v interface{}) error {
	if v == nil {
		*t = LocalTime(time.Time{})
		return nil
	}

	switch val := v.(type) {
	case time.Time:
		// Database stores UTC, so interpret as UTC
		*t = LocalTime(val.UTC())
		return nil
	case []byte:
		return t.parseTime(string(val))
	case string:
		return t.parseTime(val)
	default:
		return fmt.Errorf("LocalTime scan source was not string, []byte or time.Time: %T (%v)", v, v)
	}
}

// parseTime handles parsing a string into LocalTime.
// Database stores UTC time, so we parse as UTC.
func (t *LocalTime) parseTime(timeStr string) error {
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
		// For formats without timezone, parse as UTC (database stores UTC)
		// For formats with timezone, parse normally then convert to UTC
		if parsedTime, err := time.ParseInLocation(layout, timeStr, time.UTC); err == nil {
			*t = LocalTime(parsedTime.UTC())
			return nil
		}
	}
	return fmt.Errorf("unable to parse time string '%s' into LocalTime", timeStr)
}

// MarshalJSON implements the json.Marshaler interface.
// Converts UTC time to user's display timezone for JSON output.
func (t LocalTime) MarshalJSON() ([]byte, error) {
	if time.Time(t).IsZero() {
		return []byte("null"), nil
	}
	// Convert from UTC to display timezone for user-facing output
	formattedTime := time.Time(t).In(GetDisplayLocation()).Format(time.RFC3339)
	return []byte(fmt.Sprintf(`"%s"`, formattedTime)), nil
}

// UnmarshalJSON implements the json.Unmarshaler interface.
// Parses JSON time and converts to UTC for internal storage.
func (t *LocalTime) UnmarshalJSON(data []byte) error {
	str := strings.Trim(string(data), `"`)
	if str == "null" || str == "" {
		*t = LocalTime(time.Time{})
		return nil
	}

	layouts := []string{
		time.RFC3339Nano, time.RFC3339,
		"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02",
	}

	for _, layout := range layouts {
		if parsedTime, err := time.Parse(layout, str); err == nil {
			// Convert to UTC for internal storage
			*t = LocalTime(parsedTime.UTC())
			return nil
		}
	}
	return fmt.Errorf("unable to parse time string '%s' into LocalTime", str)
}

// ToTime converts LocalTime to Go's native time.Time type (in UTC).
func (t LocalTime) ToTime() time.Time { return time.Time(t).UTC() }

// ToLocal converts LocalTime to user's display timezone.
func (t LocalTime) ToLocal() time.Time { return time.Time(t).In(GetDisplayLocation()) }

// FromTime converts Go's native time.Time type to LocalTime (stored as UTC).
func FromTime(t time.Time) LocalTime { return LocalTime(t.UTC()) }

// Now returns the current time in UTC.
func Now() LocalTime { return LocalTime(time.Now().UTC()) }

var (
	displayLocation *time.Location
	//locationOnce    sync.Once
)

// GetDisplayLocation retrieves the user's display timezone from the "TZ" environment variable.
// This is only used for JSON output formatting, not for internal storage or comparisons.
func GetDisplayLocation() *time.Location {
	return displayLocation
}

func init() {
	tz := os.Getenv("TZ")
	if tz == "" {
		tz = time.Now().Location().String()
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		log.Printf("Warning: Failed to load timezone '%s', falling back to UTC. Error: %v", tz, err)
		displayLocation = time.UTC
	} else {
		displayLocation = loc
	}
	//log.Printf("Display timezone is set to '%s'. Database always uses UTC.", displayLocation.String())
}

// Deprecated: GetAppLocation is deprecated, use GetDisplayLocation instead.
// Kept for backward compatibility.
func GetAppLocation() *time.Location {
	return GetDisplayLocation()
}
