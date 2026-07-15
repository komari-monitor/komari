package client

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"testing"

	v1 "github.com/komari-monitor/komari/protocol/v1"
)

func TestUpdateBillingUsageBestEffortLogsAndContinues(t *testing.T) {
	var output bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
	})

	called := false
	updateBillingUsageBestEffort("node-1", v1.Report{}, func(string, v1.Report) error {
		called = true
		return errors.New("database locked")
	})

	if !called {
		t.Fatal("billing updater was not called")
	}
	if message := output.String(); !strings.Contains(message, "node-1") || !strings.Contains(message, "database locked") {
		t.Fatalf("billing failure log = %q", message)
	}
}
