package agent

import (
	"testing"

	"github.com/komari-monitor/komari/database/metricstore"
	v2 "github.com/komari-monitor/komari/protocol/v2"
)

func TestRemoveV2EventQueueRejectsDeletedClientEvents(t *testing.T) {
	v2EventMu.Lock()
	original := v2EventQueues
	v2EventQueues = make(map[string]*v2EventQueue)
	v2EventMu.Unlock()
	t.Cleanup(func() {
		v2EventMu.Lock()
		v2EventQueues = original
		v2EventMu.Unlock()
	})

	EnqueueV2Event("cleanup-node-a", v2.MethodAgentExec, v2.ExecParams{TaskID: "task-a"})
	EnqueueV2Event("cleanup-node-b", v2.MethodAgentExec, v2.ExecParams{TaskID: "task-b"})
	RemoveV2EventQueue("cleanup-node-a")
	metricstore.BlockEntityWrites("cleanup-node-a")
	t.Cleanup(func() { metricstore.UnblockEntityWrites("cleanup-node-a") })
	if event := EnqueueV2Event("cleanup-node-a", v2.MethodAgentExec, v2.ExecParams{TaskID: "late"}); event.ID != "" {
		t.Fatalf("blocked node accepted late event: %#v", event)
	}
	if events := TakeV2Events("cleanup-node-a", nil, 16); len(events) != 0 {
		t.Fatalf("deleted node events = %#v", events)
	}
	if events := TakeV2Events("cleanup-node-b", nil, 16); len(events) != 1 {
		t.Fatalf("unrelated node events = %#v", events)
	}
}
