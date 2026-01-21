package scheduler

import (
	"sync"
	"time"
)

type StopFunc func()

// Every runs fn on the given interval in its own goroutine.
// It returns a StopFunc that is safe to call multiple times.
func Every(interval time.Duration, fn func()) StopFunc {
	if interval <= 0 {
		return func() {}
	}
	if fn == nil {
		return func() {}
	}

	ticker := time.NewTicker(interval)
	stopCh := make(chan struct{})
	var once sync.Once

	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				fn()
			}
		}
	}()

	return func() {
		once.Do(func() { close(stopCh) })
	}
}
