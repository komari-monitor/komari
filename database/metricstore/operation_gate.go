package metricstore

import "context"

// storeOperationGate serializes operations that need a stable active Store.
// A channel-backed gate supports both non-blocking scheduled work and
// context-aware admin/shutdown waits.
type storeOperationGate struct {
	token chan struct{}
}

func newStoreOperationGate() *storeOperationGate {
	gate := &storeOperationGate{token: make(chan struct{}, 1)}
	gate.token <- struct{}{}
	return gate
}

func (g *storeOperationGate) Acquire(ctx context.Context) error {
	select {
	case <-g.token:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *storeOperationGate) TryAcquire() bool {
	select {
	case <-g.token:
		return true
	default:
		return false
	}
}

func (g *storeOperationGate) Release() {
	g.token <- struct{}{}
}
