// Package filemanager correlates panel file requests with agent responses.
package filemanager

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	v2 "github.com/komari-monitor/komari/protocol/v2"
	agent_runtime "github.com/komari-monitor/komari/web/agent"
)

const defaultTimeout = 30 * time.Second

var (
	ErrOffline      = errors.New("agent is not connected")
	ErrUnsupported  = errors.New("agent does not support file operations")
	ErrTimeout      = errors.New("file operation timed out")
	ErrUnknownToken = errors.New("unknown or expired file operation")

	pendingMu sync.Mutex
	pending   = make(map[string]pendingCall)
)

type pendingCall struct {
	uuid     string
	response chan v2.FileResult
}

type CallOptions struct {
	Timeout time.Duration
}

// Call dispatches a metadata-only filesystem control operation. Binary file
// data must use the HTTP transfer endpoint exposed by the transfer package.
func Call(ctx context.Context, uuid, op string, args map[string]any, options ...CallOptions) (json.RawMessage, error) {
	if !agent_runtime.IsV2Client(uuid) {
		return nil, ErrUnsupported
	}
	timeout := defaultTimeout
	if len(options) > 0 && options[0].Timeout > 0 {
		timeout = options[0].Timeout
	}
	requestID, err := newRequestID()
	if err != nil {
		return nil, err
	}

	responses := make(chan v2.FileResult, 1)
	pendingMu.Lock()
	pending[requestID] = pendingCall{uuid: uuid, response: responses}
	pendingMu.Unlock()
	defer removePending(requestID)

	ok := agent_runtime.DispatchV2Event(uuid, v2.MethodAgentFile, v2.FileOperation{
		UUID:      uuid,
		RequestID: requestID,
		Op:        op,
		Args:      args,
	})
	if !ok {
		return nil, ErrOffline
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case result := <-responses:
		if !result.OK {
			if result.Error == "" {
				return nil, errors.New("file operation failed")
			}
			return nil, errors.New(result.Error)
		}
		return result.Result, nil
	case <-timer.C:
		return nil, ErrTimeout
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func Resolve(result v2.FileResult) bool {
	if result.RequestID == "" || result.UUID == "" {
		return false
	}
	pendingMu.Lock()
	wait, ok := pending[result.RequestID]
	if ok && wait.uuid != result.UUID {
		pendingMu.Unlock()
		return false
	}
	delete(pending, result.RequestID)
	pendingMu.Unlock()
	if !ok {
		return false
	}
	wait.response <- result
	return true
}

func removePending(requestID string) {
	pendingMu.Lock()
	if _, ok := pending[requestID]; ok {
		delete(pending, requestID)
	}
	pendingMu.Unlock()
}

func newRequestID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", fmt.Errorf("generate file request id: %w", err)
	}
	return hex.EncodeToString(id[:]), nil
}
