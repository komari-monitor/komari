package timeutil

import (
	"time"
)

// SameSystemDate compares calendar dates in the operating system timezone.
func SameSystemDate(left, right time.Time) bool {
	left = left.In(time.Local)
	right = right.In(time.Local)
	ly, lm, ld := left.Date()
	ry, rm, rd := right.Date()
	return ly == ry && lm == rm && ld == rd
}

// FormatSystemDate formats an instant as a system-local calendar date.
func FormatSystemDate(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.In(time.Local).Format("2006-01-02")
}

// SystemDateDistance returns the number of local calendar boundaries between
// two instants, independent of daylight-saving day length.
func SystemDateDistance(from, to time.Time) int {
	fy, fm, fd := from.In(time.Local).Date()
	ty, tm, td := to.In(time.Local).Date()
	fromDate := time.Date(fy, fm, fd, 0, 0, 0, 0, time.UTC)
	toDate := time.Date(ty, tm, td, 0, 0, 0, 0, time.UTC)
	return int(toDate.Sub(fromDate) / (24 * time.Hour))
}
