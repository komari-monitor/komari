package auditlog

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"gorm.io/gorm"
)

const (
	// maxAuditMessageRunes keeps persisted audit messages within safe bounds for
	// SQLite, PostgreSQL, and MySQL TEXT columns (64 KiB byte cap).
	maxAuditMessageRunes = 8192
	defaultValueMaxRunes = 240
	redactedPlaceholder  = "[REDACTED]"
)

var (
	dsnCredentialPattern    = regexp.MustCompile(`(?i)\b(password|passwd|pwd)\s*=\s*('[^']*'|"[^"]*"|[^\s,;)&]+)`)
	dsnURLCredentialPattern = regexp.MustCompile(`(?i)([a-z0-9_.-]+):([^@\s]+)@`)
)

type clientNameRow struct {
	UUID string
	Name string
}

type userNameRow struct {
	UUID     string
	Username string
}

// FormatClient formats a server identifier as "name(uuid)" for readable audit logs.
// When either part is empty, it falls back to whichever part is available.
func FormatClient(name, uuid string) string {
	name = strings.TrimSpace(name)
	uuid = strings.TrimSpace(uuid)
	switch {
	case name != "" && uuid != "":
		return name + "(" + uuid + ")"
	case name != "":
		return name
	default:
		return uuid
	}
}

// FormatClientUUID resolves a client's name from the main database and formats
// it as "name(uuid)", falling back to the bare UUID when not found.
func FormatClientUUID(uuid string) string {
	uuid = strings.TrimSpace(uuid)
	if uuid == "" {
		return ""
	}
	return formatClientUUIDWithDB(dbcore.GetDBInstance(), uuid)
}

func formatClientUUIDWithDB(db *gorm.DB, uuid string) string {
	uuid = strings.TrimSpace(uuid)
	if uuid == "" || db == nil {
		return uuid
	}
	var rows []clientNameRow
	if err := db.Model(&models.Client{}).Select("uuid", "name").Where("uuid = ?", uuid).Limit(1).Find(&rows).Error; err != nil || len(rows) == 0 {
		return uuid
	}
	return FormatClient(rows[0].Name, uuid)
}

// FormatClientUUIDs batch-resolves client names for the provided UUIDs and
// returns "name(uuid)" labels preserving the original order.
func FormatClientUUIDs(uuids []string) []string {
	return formatClientUUIDsWithDB(dbcore.GetDBInstance(), uuids)
}

func formatClientUUIDsWithDB(db *gorm.DB, uuids []string) []string {
	if len(uuids) == 0 {
		return nil
	}
	unique := make([]string, 0, len(uuids))
	seen := make(map[string]struct{}, len(uuids))
	for _, raw := range uuids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			unique = append(unique, id)
		}
	}
	names := lookupClientNames(db, unique)
	result := make([]string, 0, len(uuids))
	for _, raw := range uuids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		result = append(result, FormatClient(names[id], id))
	}
	return result
}

// FormatClients formats a slice of models.Client into "name(uuid)" labels.
// Entries that already carry a name avoid any database lookup; entries with an
// empty name are batch-resolved by UUID.
func FormatClients(list []models.Client) []string {
	return formatClientsWithDB(dbcore.GetDBInstance(), list)
}

func formatClientsWithDB(db *gorm.DB, list []models.Client) []string {
	if len(list) == 0 {
		return nil
	}
	var missing []string
	seenMissing := make(map[string]struct{})
	for _, c := range list {
		id := strings.TrimSpace(c.UUID)
		if strings.TrimSpace(c.Name) == "" && id != "" {
			if _, ok := seenMissing[id]; !ok {
				seenMissing[id] = struct{}{}
				missing = append(missing, id)
			}
		}
	}
	names := lookupClientNames(db, missing)
	result := make([]string, 0, len(list))
	for _, c := range list {
		id := strings.TrimSpace(c.UUID)
		name := strings.TrimSpace(c.Name)
		if name == "" && id != "" {
			name = names[id]
		}
		label := FormatClient(name, id)
		if label != "" {
			result = append(result, label)
		}
	}
	return result
}

func lookupClientNames(db *gorm.DB, uuids []string) map[string]string {
	if db == nil || len(uuids) == 0 {
		return nil
	}
	var rows []clientNameRow
	if err := db.Model(&models.Client{}).Select("uuid", "name").Where("uuid IN ?", uuids).Find(&rows).Error; err != nil {
		return nil
	}
	names := make(map[string]string, len(rows))
	for _, row := range rows {
		if trimmed := strings.TrimSpace(row.Name); trimmed != "" {
			names[row.UUID] = trimmed
		}
	}
	return names
}

// JoinLabels joins non-empty labels with ", ".
func JoinLabels(labels []string) string {
	filtered := make([]string, 0, len(labels))
	for _, label := range labels {
		if trimmed := strings.TrimSpace(label); trimmed != "" {
			filtered = append(filtered, trimmed)
		}
	}
	return strings.Join(filtered, ", ")
}

// FormatUser formats a user identifier as "username(uuid)", falling back to
// whichever part is available.
func FormatUser(username, uuid string) string {
	username = strings.TrimSpace(username)
	uuid = strings.TrimSpace(uuid)
	switch {
	case username != "" && uuid != "":
		return username + "(" + uuid + ")"
	case username != "":
		return username
	default:
		return uuid
	}
}

// FormatUserUUID resolves a user's username from the main database and formats
// it as "username(uuid)", falling back to the bare UUID when not found.
func FormatUserUUID(uuid string) string {
	uuid = strings.TrimSpace(uuid)
	if uuid == "" {
		return ""
	}
	return formatUserUUIDWithDB(dbcore.GetDBInstance(), uuid)
}

func formatUserUUIDWithDB(db *gorm.DB, uuid string) string {
	uuid = strings.TrimSpace(uuid)
	if uuid == "" || db == nil {
		return uuid
	}
	var rows []userNameRow
	if err := db.Model(&models.User{}).Select("uuid", "username").Where("uuid = ?", uuid).Limit(1).Find(&rows).Error; err != nil || len(rows) == 0 {
		return uuid
	}
	return FormatUser(rows[0].Username, uuid)
}

// FormatBytes formats a byte count into a human-readable IEC unit string.
func FormatBytes(n int64) string {
	const unit = 1024
	if n < 0 {
		return fmt.Sprintf("%d B", n)
	}
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 5; v /= unit {
		div *= unit
		exp++
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	return fmt.Sprintf("%.2f %s", float64(n)/float64(div), units[exp])
}

// Truncate shortens s to at most maxRunes UTF-8 runes, appending a truncation
// note when characters were removed.
func Truncate(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	total := utf8.RuneCountInString(s)
	if total <= maxRunes {
		return s
	}
	runes := []rune(s)
	return fmt.Sprintf("%s... (truncated, total %d chars)", string(runes[:maxRunes]), total)
}

// MaskToken masks a bearer/session token so only the prefix and suffix remain
// visible in audit logs.
func MaskToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	runes := []rune(token)
	if len(runes) <= 8 {
		return "***"
	}
	return string(runes[:4]) + "..." + string(runes[len(runes)-4:])
}

// IsSensitiveKey reports whether a configuration or payload key likely holds
// secret material that must not be recorded in plaintext.
func IsSensitiveKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	if k == "" {
		return false
	}
	sensitiveTokens := []string{
		"password",
		"passwd",
		"secret",
		"token",
		"api_key",
		"apikey",
		"auto_discovery_key",
		"two_factor",
		"private_key",
		"access_key",
		"app_key",
		"auth_key",
		"signing_key",
		"credential",
		"authorization",
		"cookie",
		"passphrase",
		"bearer",
		"2fa_code",
	}
	for _, needle := range sensitiveTokens {
		if strings.Contains(k, needle) {
			return true
		}
	}
	return false
}

// RedactDSN masks passwords in URL-style and key=value database DSNs.
func RedactDSN(dsn string) string {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return ""
	}
	redacted := dsnCredentialPattern.ReplaceAllString(dsn, "$1="+redactedPlaceholder)
	redacted = dsnURLCredentialPattern.ReplaceAllString(redacted, "$1:"+redactedPlaceholder+"@")
	return redacted
}

// RedactURL strips embedded passwords and redacts sensitive query parameters
// from a URL while preserving host and path for troubleshooting.
func RedactURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return RedactDSN(raw)
	}
	if parsed.User != nil {
		username := parsed.User.Username()
		if _, hasPass := parsed.User.Password(); hasPass {
			parsed.User = url.UserPassword(username, "REDACTED")
		}
	}
	if parsed.RawQuery != "" {
		q := parsed.Query()
		changed := false
		for key := range q {
			if IsSensitiveKey(key) || strings.EqualFold(key, "key") || strings.EqualFold(key, "sig") || strings.EqualFold(key, "sign") {
				q.Set(key, "REDACTED")
				changed = true
			}
		}
		if changed {
			parsed.RawQuery = q.Encode()
		}
	}
	return parsed.String()
}

// FormatValue renders a single audit field value safely with redaction and
// truncation applied according to its key name.
func FormatValue(key string, value any) string {
	if IsSensitiveKey(key) {
		if isEmptyValue(value) {
			return "(empty)"
		}
		return redactedPlaceholder
	}
	lowerKey := strings.ToLower(strings.TrimSpace(key))
	if value == nil {
		return "null"
	}
	switch v := value.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		switch {
		case strings.Contains(lowerKey, "dsn"):
			return strconv.Quote(Truncate(RedactDSN(trimmed), defaultValueMaxRunes))
		case lowerKey == "url" || strings.HasSuffix(lowerKey, "_url") || strings.HasSuffix(lowerKey, "url"):
			return strconv.Quote(Truncate(RedactURL(trimmed), defaultValueMaxRunes))
		default:
			return strconv.Quote(Truncate(v, defaultValueMaxRunes))
		}
	case time.Time:
		if v.IsZero() {
			return "null"
		}
		return v.UTC().Format(time.RFC3339)
	case *time.Time:
		if v == nil || v.IsZero() {
			return "null"
		}
		return v.UTC().Format(time.RFC3339)
	case bool:
		return strconv.FormatBool(v)
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprint(v)
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case json.Number:
		return v.String()
	default:
		if encoded, err := json.Marshal(v); err == nil {
			return Truncate(string(encoded), defaultValueMaxRunes)
		}
		return Truncate(fmt.Sprint(v), defaultValueMaxRunes)
	}
}

func isEmptyValue(value any) bool {
	if value == nil {
		return true
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v) == ""
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Slice, reflect.Map, reflect.Array:
		return rv.Len() == 0
	case reflect.Pointer:
		return rv.IsNil()
	}
	return false
}

// ValuesEqual compares two JSON-like values with numeric normalization (e.g.
// int 5 equals float64 5.0) and timestamp normalization for diffing audit logs.
func ValuesEqual(a, b any) bool {
	aNil := isNilInterface(a)
	bNil := isNilInterface(b)
	if aNil && bNil {
		return true
	}
	if aNil || bNil {
		return false
	}
	if fa, ok := toFloat64(a); ok {
		if fb, ok := toFloat64(b); ok {
			return fa == fb
		}
	}
	if ta, ok := toTime(a); ok {
		if tb, ok := toTime(b); ok {
			return ta.UTC().Equal(tb.UTC())
		}
	}
	return reflect.DeepEqual(a, b)
}

func isNilInterface(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Map, reflect.Func, reflect.Chan:
		return rv.IsNil()
	default:
		return false
	}
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func toTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case *time.Time:
		if t == nil {
			return time.Time{}, false
		}
		return *t, true
	case string:
		parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(t))
		return parsed, err == nil
	default:
		return time.Time{}, false
	}
}

// FormatMap returns deterministically sorted "key=value" entries with sensitive
// values redacted and long values truncated.
func FormatMap(fields map[string]any, skipKeys ...string) []string {
	if len(fields) == 0 {
		return nil
	}
	skip := make(map[string]struct{}, len(skipKeys))
	for _, k := range skipKeys {
		skip[k] = struct{}{}
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		if _, ignored := skip[k]; ignored {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("%s=%s", k, FormatValue(k, fields[k])))
	}
	return lines
}

// FormatDiff compares submitted changes against previous values and returns
// changed keys plus formatted "key: old -> new" lines in deterministic order.
// Sensitive keys never emit plaintext values.
func FormatDiff(before, submitted map[string]any, skipKeys ...string) (changedKeys []string, lines []string) {
	if len(submitted) == 0 {
		return nil, nil
	}
	skip := make(map[string]struct{}, len(skipKeys))
	for _, k := range skipKeys {
		skip[k] = struct{}{}
	}
	keys := make([]string, 0, len(submitted))
	for k := range submitted {
		if _, ignored := skip[k]; ignored {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		newVal := submitted[k]
		oldVal, hasBefore := before[k]
		if hasBefore && ValuesEqual(oldVal, newVal) {
			continue
		}
		changedKeys = append(changedKeys, k)
		if IsSensitiveKey(k) {
			if isEmptyValue(newVal) {
				lines = append(lines, fmt.Sprintf("%s: %s (cleared)", k, redactedPlaceholder))
			} else {
				lines = append(lines, fmt.Sprintf("%s: %s (updated)", k, redactedPlaceholder))
			}
			continue
		}
		if hasBefore {
			lines = append(lines, fmt.Sprintf("%s: %s -> %s", k, FormatValue(k, oldVal), FormatValue(k, newVal)))
		} else {
			lines = append(lines, fmt.Sprintf("%s: %s", k, FormatValue(k, newVal)))
		}
	}
	return changedKeys, lines
}

// JoinLines joins a summary line with optional non-empty detail lines.
func JoinLines(summary string, details ...string) string {
	lines := make([]string, 0, 1+len(details))
	if trimmed := strings.TrimSpace(summary); trimmed != "" {
		lines = append(lines, trimmed)
	}
	for _, detail := range details {
		if trimmed := strings.TrimSpace(detail); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return strings.Join(lines, "\n")
}
