package v2

import (
	"encoding/json"
	"time"
)

const (
	Version               = "2.0"
	MethodAgentReport     = "agent.report"
	MethodAgentBasicInfo  = "agent.basicInfo"
	MethodAgentPingResult = "agent.pingResult"
	MethodAgentTaskResult = "agent.taskResult"
	MethodAgentExec       = "agent.exec"
	MethodAgentPing       = "agent.ping"
	MethodAgentMessage    = "agent.message"
	MethodAgentEvent      = "agent.event"
	MethodAgentTerminal   = "agent.terminal.request"
	MethodAgentPull       = "agent.pull"
	MethodAgentFile       = "agent.file"
	MethodAgentFileResult = "agent.file.result"
)

type Request struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
	ID      any    `json:"id,omitempty"`
}

type Response struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *RPCError `json:"error,omitempty"`
}

type Event struct {
	ID        string    `json:"id"`
	Method    string    `json:"method"`
	Params    any       `json:"params,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type ReportParams struct {
	Report      Report   `json:"report"`
	AckEventIDs []string `json:"ack_event_ids,omitempty"`
}

type Message struct {
	Type      string `json:"type"`
	Content   string `json:"content"`
	Sender    string `json:"sender"`
	Timestamp int64  `json:"timestamp"`
}

type IPAddress struct {
	Ipv4 string `json:"ipv4"`
	Ipv6 string `json:"ipv6"`
}

type Report struct {
	UUID        string            `json:"uuid,omitempty"`
	CPU         CPUReport         `json:"cpu"`
	Ram         RamReport         `json:"ram"`
	Swap        RamReport         `json:"swap"`
	Load        LoadReport        `json:"load"`
	Disk        DiskReport        `json:"disk"`
	Network     NetworkReport     `json:"network"`
	Connections ConnectionsReport `json:"connections"`
	GPU         *GPUDetailReport  `json:"gpu,omitempty"`
	Uptime      int64             `json:"uptime"`
	Process     int               `json:"process"`
	Message     string            `json:"message"`
	Method      string            `json:"method,omitempty"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type CPUReport struct {
	Name  string  `json:"name,omitempty"`
	Cores int     `json:"cores,omitempty"`
	Arch  string  `json:"arch,omitempty"`
	Usage float64 `json:"usage,omitempty"`
}

type GPUDetailReport struct {
	Count        int             `json:"count"`
	AverageUsage float64         `json:"average_usage"`
	DetailedInfo []GPUDeviceInfo `json:"detailed_info"`
}

type GPUDeviceInfo struct {
	Name        string  `json:"name"`
	MemoryTotal int64   `json:"memory_total"`
	MemoryUsed  int64   `json:"memory_used"`
	Utilization float64 `json:"utilization"`
	Temperature int     `json:"temperature"`
}

type RamReport struct {
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
}

type LoadReport struct {
	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`
}

type DiskReport struct {
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
}

type NetworkReport struct {
	Up        int64 `json:"up"`
	Down      int64 `json:"down"`
	TotalUp   int64 `json:"totalUp"`
	TotalDown int64 `json:"totalDown"`
}

type ConnectionsReport struct {
	TCP int `json:"tcp"`
	UDP int `json:"udp"`
}

type BasicInfoParams struct {
	Info map[string]interface{} `json:"info"`
}

type PingResultParams struct {
	TaskID     uint      `json:"task_id"`
	PingType   string    `json:"ping_type"`
	Value      int       `json:"value"`
	FinishedAt time.Time `json:"finished_at"`
}

type TaskResultParams struct {
	TaskID     string    `json:"task_id"`
	Result     string    `json:"result"`
	ExitCode   int       `json:"exit_code"`
	FinishedAt time.Time `json:"finished_at"`
}

type PullParams struct {
	Capabilities []string `json:"capabilities,omitempty"`
	AckEventIDs  []string `json:"ack_event_ids,omitempty"`
	LastEventID  string   `json:"last_event_id,omitempty"`
}

type ExecParams struct {
	TaskID  string `json:"task_id"`
	Command string `json:"command"`
}

type PingParams struct {
	TaskID uint   `json:"ping_task_id"`
	Type   string `json:"ping_type"`
	Target string `json:"ping_target"`
}

type MessageParams struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type EventParams struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
}

type TerminalRequestParams struct {
	RequestID string `json:"request_id"`
}

// FileOperation is metadata-only. File contents travel through the dedicated
// HTTP transfer endpoint rather than through JSON-RPC.
type FileOperation struct {
	UUID      string         `json:"uuid"`
	RequestID string         `json:"request_id"`
	Op        string         `json:"op"`
	Args      map[string]any `json:"args,omitempty"`
}

type FileResult struct {
	UUID      string          `json:"uuid"`
	RequestID string          `json:"request_id"`
	OK        bool            `json:"ok"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
}

func Success(id any, result any) Response {
	return Response{JSONRPC: Version, ID: id, Result: result}
}

func Error(id any, code int, message string, data any) Response {
	return Response{JSONRPC: Version, ID: id, Error: &RPCError{Code: code, Message: message, Data: data}}
}
