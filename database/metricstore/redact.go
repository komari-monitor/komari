package metricstore

import (
	"regexp"
	"strings"
)

var (
	metricCredentialPattern    = regexp.MustCompile(`(?i)\b(password|passwd)\s*=\s*('[^']*'|"[^"]*"|[^\s,;)&]+)`)
	metricURLCredentialPattern = regexp.MustCompile(`(?i)([a-z0-9_.-]+):([^@\s]+)@`)
)

// RedactConnectionError removes the supplied DSN and common password fragments
// before a connection error is shown to an administrator or written to a log.
func RedactConnectionError(message, dsn string) string {
	message = strings.TrimSpace(message)
	if dsn != "" {
		message = strings.ReplaceAll(message, dsn, "[redacted]")
	}
	message = metricCredentialPattern.ReplaceAllString(message, "$1=[redacted]")
	return metricURLCredentialPattern.ReplaceAllString(message, "$1:[redacted]@")
}
