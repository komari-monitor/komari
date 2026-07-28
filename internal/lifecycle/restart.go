// Package lifecycle carries process-level requests without coupling web RPC
// handlers to the server runtime.
package lifecycle

// RestartReason identifies why the current process must restart.
type RestartReason string

const RestartForMetricStoreStructureUpgrade RestartReason = "metric-store-structure-upgrade"

var restartRequests = make(chan RestartReason, 1)

// RequestRestart asks the running server to shut down cleanly and exit. A
// pending request is sufficient because the process will be replaced.
func RequestRestart(reason RestartReason) {
	select {
	case restartRequests <- reason:
	default:
	}
}

// RestartRequests returns process restart requests for the server runtime.
func RestartRequests() <-chan RestartReason {
	return restartRequests
}
