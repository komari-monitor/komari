package clients

import (
	"sync"
	"testing"
	"time"

	"github.com/komari-monitor/komari/utils"
	"github.com/stretchr/testify/assert"
)

func TestComputeTrafficDeltaHandlesZeroAndReset(t *testing.T) {
	tests := []struct {
		name     string
		current  int64
		previous int64
		want     int64
	}{
		{name: "previous zero counts current delta", current: 120, previous: 0, want: 120},
		{name: "monotonic counter uses difference", current: 250, previous: 200, want: 50},
		{name: "counter reset uses current", current: 15, previous: 250, want: 15},
		{name: "negative previous remains guarded", current: 15, previous: -1, want: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, utils.ComputeTrafficDelta(test.current, test.previous))
		})
	}
}

func TestReportSaveLockSerializesSameClient(t *testing.T) {
	clientUUID := "client-lock-same"
	lock := getReportSaveLock(clientUUID)
	lock.Lock()
	defer lock.Unlock()

	acquired := make(chan struct{})
	go func() {
		secondLock := getReportSaveLock(clientUUID)
		secondLock.Lock()
		defer secondLock.Unlock()
		close(acquired)
	}()

	select {
	case <-acquired:
		t.Fatal("same-client report save lock was acquired while already held")
	case <-time.After(20 * time.Millisecond):
	}

	lock.Unlock()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("same-client report save lock was not released")
	}
	lock.Lock()
}

func TestReportSaveLockAllowsDifferentClients(t *testing.T) {
	firstLock := getReportSaveLock("client-lock-a")
	firstLock.Lock()
	defer firstLock.Unlock()

	var waitGroup sync.WaitGroup
	waitGroup.Add(1)
	acquired := make(chan struct{})
	go func() {
		defer waitGroup.Done()
		secondLock := getReportSaveLock("client-lock-b")
		secondLock.Lock()
		defer secondLock.Unlock()
		close(acquired)
	}()

	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("different-client report save lock should not block")
	}
	waitGroup.Wait()
}
