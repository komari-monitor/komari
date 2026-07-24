package server

import (
	"errors"
	"sync"
	"testing"
)

func TestMetricCompactCycleStateConcurrentAddAndReset(t *testing.T) {
	const workers = 64
	wantErr := errors.New("compact step failed")
	var state metricCompactCycleState
	var group sync.WaitGroup
	for i := 0; i < workers; i++ {
		group.Add(1)
		go func(failed bool) {
			defer group.Done()
			var err error
			if failed {
				err = wantErr
			}
			state.add(1, err)
		}(i%2 == 0)
	}
	group.Wait()

	written, err := state.finish()
	if written != workers {
		t.Fatalf("cycle buckets = %d, want %d", written, workers)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("cycle error = %v, want joined compact error", err)
	}
	if written, err := state.finish(); written != 0 || err != nil {
		t.Fatalf("reset cycle = buckets %d, err %v; want zero state", written, err)
	}
}
