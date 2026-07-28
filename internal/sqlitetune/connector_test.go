package sqlitetune

import (
	"testing"
	"time"
)

func TestNormalizeAllowsUnlimitedJournalSize(t *testing.T) {
	options, err := normalize(Options{
		BusyTimeout:           5 * time.Second,
		CacheSizeKB:           8,
		WALAutoCheckpoint:     1,
		JournalSizeLimitBytes: -1,
	})
	if err != nil {
		t.Fatalf("normalize unlimited journal size: %v", err)
	}
	if options.JournalSizeLimitBytes != -1 {
		t.Fatalf("journal size limit = %d, want -1", options.JournalSizeLimitBytes)
	}
}
