package v2

import "encoding/json"

const (
	MethodNetworkTestNextTrace       = "networkTest.nextTrace"
	MethodNetworkTestIperf3          = "networkTest.iperf3"
	MethodNetworkTestMeshTrace       = "networkTest.meshTrace"
	MethodNetworkTestGetMeshTraceJob = "networkTest.getMeshTraceJob"
)

type IPFamily string

const (
	IPFamilyAuto IPFamily = "auto"
	IPFamilyIPv4 IPFamily = "ipv4"
	IPFamilyIPv6 IPFamily = "ipv6"
)

type TraceProtocol string

const (
	TraceProtocolICMP TraceProtocol = "icmp"
	TraceProtocolTCP  TraceProtocol = "tcp"
	TraceProtocolUDP  TraceProtocol = "udp"
)

type MeshMode string

const (
	MeshModeAllToAll MeshMode = "all_to_all"
	MeshModeOneToAll MeshMode = "one_to_all"
	MeshModePairs    MeshMode = "pairs"
)

type NodeEndpoint struct {
	NodeID string   `json:"node_id"`
	Name   string   `json:"name"`
	Host   string   `json:"host"`
	Family IPFamily `json:"ip_family"`
}

type NextTraceParams struct {
	TaskID     string        `json:"task_id"`
	SourceID   string        `json:"source_id"`
	TargetID   string        `json:"target_id"`
	TargetHost string        `json:"target_host"`
	IPFamily   IPFamily      `json:"ip_family"`
	Protocol   TraceProtocol `json:"protocol"`
	MaxHops    int           `json:"max_hops"`
	TimeoutMs  int           `json:"timeout_ms"`
	RawOutput  bool          `json:"raw_output"`
}

type NextTraceResult struct {
	TaskID     string          `json:"task_id"`
	SourceID   string          `json:"source_id"`
	TargetID   string          `json:"target_id"`
	TargetHost string          `json:"target_host"`
	IPFamily   IPFamily        `json:"ip_family"`
	Protocol   TraceProtocol   `json:"protocol"`
	StartedAt  string          `json:"started_at"`
	FinishedAt string          `json:"finished_at"`
	DurationMs int             `json:"duration_ms"`
	OK         bool            `json:"ok"`
	Error      string          `json:"error"`
	Summary    TraceSummary    `json:"summary"`
	Hops       []TraceHop      `json:"hops"`
	Raw        json.RawMessage `json:"raw"`
	RawText    string          `json:"raw_text"`
	ExitCode   int             `json:"exit_code"`
	Truncated  bool            `json:"truncated"`
}

type TraceSummary struct {
	HopCount   int     `json:"hop_count"`
	Reached    bool    `json:"reached"`
	PacketLoss float64 `json:"packet_loss"`
	RTTMs      float64 `json:"rtt_ms"`
}

type TraceHop struct {
	Hop      int     `json:"hop"`
	Host     string  `json:"host"`
	IP       string  `json:"ip"`
	RTTMs    float64 `json:"rtt_ms"`
	Loss     float64 `json:"loss"`
	ASN      string  `json:"asn"`
	Location string  `json:"location"`
}

type MeshTraceParams struct {
	SourceNodeIDs  []string      `json:"source_node_ids"`
	TargetNodeIDs  []string      `json:"target_node_ids"`
	Mode           MeshMode      `json:"mode"`
	IPFamily       IPFamily      `json:"ip_family"`
	Protocol       TraceProtocol `json:"protocol"`
	MaxHops        int           `json:"max_hops"`
	TimeoutMs      int           `json:"timeout_ms"`
	MaxConcurrency int           `json:"max_concurrency"`
	PerAgentLimit  int           `json:"per_agent_limit"`
}

type MeshTraceAccepted struct {
	JobID          string `json:"job_id"`
	Status         string `json:"status"`
	TotalPairs     int    `json:"total_pairs"`
	AcceptedAt     string `json:"accepted_at"`
	PollIntervalMs int    `json:"poll_interval_ms"`
}

type MeshTraceJobSnapshot struct {
	JobID      string            `json:"job_id"`
	Status     string            `json:"status"`
	Total      int               `json:"total"`
	Done       int               `json:"done"`
	Failed     int               `json:"failed"`
	Running    int               `json:"running"`
	Results    []NextTraceResult `json:"results"`
	StartedAt  string            `json:"started_at"`
	UpdatedAt  string            `json:"updated_at"`
	FinishedAt string            `json:"finished_at"`
	Error      string            `json:"error"`
}
