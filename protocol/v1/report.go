package v1

import "time"

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

// GPUReport is the legacy v1 single-GPU shape kept for wire compatibility.
type GPUReport struct {
	Name  string  `json:"name,omitempty"`
	Usage float64 `json:"usage,omitempty"`
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
