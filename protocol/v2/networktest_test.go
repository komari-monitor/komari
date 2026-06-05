package v2

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestNetworkTestMethodConstants(t *testing.T) {
	tests := map[string]string{
		MethodNetworkTestNextTrace:       "networkTest.nextTrace",
		MethodNetworkTestIperf3:          "networkTest.iperf3",
		MethodNetworkTestMeshTrace:       "networkTest.meshTrace",
		MethodNetworkTestGetMeshTraceJob: "networkTest.getMeshTraceJob",
	}

	for got, want := range tests {
		if got != want {
			t.Fatalf("method constant = %q, want %q", got, want)
		}
	}
}

func TestNextTraceParamsJSON(t *testing.T) {
	params := NextTraceParams{
		TaskID:     "trace-1",
		SourceID:   "agent-a",
		TargetID:   "agent-b",
		TargetHost: "203.0.113.10",
		IPFamily:   IPFamilyIPv4,
		Protocol:   TraceProtocolICMP,
		MaxHops:    30,
		TimeoutMs:  5000,
		RawOutput:  true,
	}

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}

	assertJSONKeys(t, data, []string{
		"task_id",
		"source_id",
		"target_id",
		"target_host",
		"ip_family",
		"protocol",
		"max_hops",
		"timeout_ms",
		"raw_output",
	})
	assertNoCamelCaseKeys(t, data)

	var decoded NextTraceParams
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, params) {
		t.Fatalf("decoded params = %#v, want %#v", decoded, params)
	}
}

func TestNextTraceResultJSON(t *testing.T) {
	result := NextTraceResult{
		TaskID:     "trace-1",
		SourceID:   "agent-a",
		TargetID:   "agent-b",
		TargetHost: "203.0.113.10",
		IPFamily:   IPFamilyIPv4,
		Protocol:   TraceProtocolICMP,
		StartedAt:  "2026-06-05T10:00:00Z",
		FinishedAt: "2026-06-05T10:00:01Z",
		DurationMs: 1000,
		OK:         true,
		Error:      "",
		Summary: TraceSummary{
			HopCount:   2,
			Reached:    true,
			PacketLoss: 0,
			RTTMs:      12.5,
		},
		Hops: []TraceHop{
			{Hop: 1, Host: "router", IP: "192.0.2.1", RTTMs: 1.2, Loss: 0, ASN: "AS64500", Location: "lab"},
			{Hop: 2, Host: "target", IP: "203.0.113.10", RTTMs: 12.5, Loss: 0, ASN: "AS64501", Location: "edge"},
		},
		Raw:       json.RawMessage(`{"parser":"nexttrace"}`),
		RawText:   "raw trace text",
		ExitCode:  0,
		Truncated: false,
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}

	assertJSONKeys(t, data, []string{
		"task_id",
		"source_id",
		"target_id",
		"target_host",
		"ip_family",
		"protocol",
		"started_at",
		"finished_at",
		"duration_ms",
		"ok",
		"error",
		"summary",
		"hops",
		"raw",
		"raw_text",
		"exit_code",
		"truncated",
	})
	assertJSONKeysAt(t, data, "summary", []string{"hop_count", "reached", "packet_loss", "rtt_ms"})
	assertJSONKeysAt(t, data, "hops.0", []string{"hop", "host", "ip", "rtt_ms", "loss", "asn", "location"})
	assertNoCamelCaseKeys(t, data)

	var decoded NextTraceResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, result) {
		t.Fatalf("decoded result = %#v, want %#v", decoded, result)
	}
}

func TestMeshTraceParamsJSON(t *testing.T) {
	params := MeshTraceParams{
		SourceNodeIDs:  []string{"agent-a"},
		TargetNodeIDs:  []string{"agent-b", "agent-c"},
		Mode:           MeshModeOneToAll,
		IPFamily:       IPFamilyIPv6,
		Protocol:       TraceProtocolTCP,
		MaxHops:        20,
		TimeoutMs:      8000,
		MaxConcurrency: 4,
		PerAgentLimit:  2,
	}

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}

	assertJSONKeys(t, data, []string{
		"source_node_ids",
		"target_node_ids",
		"mode",
		"ip_family",
		"protocol",
		"max_hops",
		"timeout_ms",
		"max_concurrency",
		"per_agent_limit",
	})
	assertNoCamelCaseKeys(t, data)

	var decoded MeshTraceParams
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, params) {
		t.Fatalf("decoded params = %#v, want %#v", decoded, params)
	}
}

func TestMeshTraceJobSnapshotJSON(t *testing.T) {
	snapshot := MeshTraceJobSnapshot{
		JobID:   "mesh-1",
		Status:  "running",
		Total:   2,
		Done:    1,
		Failed:  0,
		Running: 1,
		Results: []NextTraceResult{
			{
				TaskID:     "trace-1",
				SourceID:   "agent-a",
				TargetID:   "agent-b",
				TargetHost: "203.0.113.10",
				IPFamily:   IPFamilyIPv4,
				Protocol:   TraceProtocolUDP,
				StartedAt:  "2026-06-05T10:00:00Z",
				FinishedAt: "2026-06-05T10:00:01Z",
				DurationMs: 1000,
				OK:         false,
				Error:      "timeout",
				Summary:    TraceSummary{HopCount: 0, Reached: false, PacketLoss: 1, RTTMs: 0},
				Hops:       []TraceHop{},
				Raw:        json.RawMessage(`null`),
				RawText:    "",
				ExitCode:   1,
				Truncated:  false,
			},
		},
		StartedAt:  "2026-06-05T10:00:00Z",
		UpdatedAt:  "2026-06-05T10:00:01Z",
		FinishedAt: "",
		Error:      "",
	}

	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}

	assertJSONKeys(t, data, []string{
		"job_id",
		"status",
		"total",
		"done",
		"failed",
		"running",
		"results",
		"started_at",
		"updated_at",
		"finished_at",
		"error",
	})
	assertJSONKeysAt(t, data, "results.0", []string{"task_id", "source_id", "target_id", "target_host"})
	assertNoCamelCaseKeys(t, data)

	var decoded MeshTraceJobSnapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, snapshot) {
		t.Fatalf("decoded snapshot = %#v, want %#v", decoded, snapshot)
	}
}

func assertJSONKeys(t *testing.T, data []byte, keys []string) {
	t.Helper()

	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}

	for _, key := range keys {
		if _, ok := object[key]; !ok {
			t.Fatalf("missing JSON key %q in %s", key, data)
		}
	}
}

func assertJSONKeysAt(t *testing.T, data []byte, path string, keys []string) {
	t.Helper()

	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}

	object := jsonObjectAt(t, root, path)
	for _, key := range keys {
		if _, ok := object[key]; !ok {
			t.Fatalf("missing JSON key %q at %q in %s", key, path, data)
		}
	}
}

func assertNoCamelCaseKeys(t *testing.T, data []byte) {
	t.Helper()

	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}

	var walk func(any)
	walk = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			for key, next := range typed {
				if strings.ContainsAny(key, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
					t.Fatalf("JSON key %q contains uppercase characters in %s", key, data)
				}
				walk(next)
			}
		case []any:
			for _, next := range typed {
				walk(next)
			}
		}
	}
	walk(value)
}

func jsonObjectAt(t *testing.T, root any, path string) map[string]any {
	t.Helper()

	current := root
	for _, part := range strings.Split(path, ".") {
		switch typed := current.(type) {
		case map[string]any:
			current = typed[part]
		case []any:
			if part != "0" {
				t.Fatalf("unsupported array path segment %q", part)
			}
			current = typed[0]
		default:
			t.Fatalf("path %q reached non-container value %#v", path, current)
		}
	}

	object, ok := current.(map[string]any)
	if !ok {
		t.Fatalf("path %q is not a JSON object: %#v", path, current)
	}
	return object
}
