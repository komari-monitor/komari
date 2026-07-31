package metrics

import (
	"runtime"
	"time"
)

var ProcessStartedAt = time.Now()

type CPUInfo struct {
	Model string
	Speed int
	Times map[string]uint64
}

type System struct {
	Release  string
	Version  string
	Uptime   float64
	LoadAvg  [3]float64
	TotalMem uint64
	FreeMem  uint64
	CPUs     []CPUInfo
}

type Process struct {
	UserCPU             time.Duration
	SystemCPU           time.Duration
	MaxRSS              uint64
	RSS                 uint64
	MinorPageFault      int64
	MajorPageFault      int64
	FSRead              int64
	FSWrite             int64
	VoluntarySwitches   int64
	InvoluntarySwitches int64
}

func CPUInfoValues(metrics []CPUInfo) []map[string]any {
	result := make([]map[string]any, len(metrics))
	for index, cpu := range metrics {
		result[index] = map[string]any{"model": cpu.Model, "speed": cpu.Speed, "times": cpu.Times}
	}
	return result
}

func Arch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	case "arm64":
		return "arm64"
	default:
		return runtime.GOARCH
	}
}

func Platform() string {
	if runtime.GOOS == "windows" {
		return "win32"
	}
	return runtime.GOOS
}
