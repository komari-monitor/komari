package jsruntime

import "time"

var nodeProcessStartedAt = time.Now()

type nodeCPUInfo struct {
	model string
	speed int
	times map[string]uint64
}

type nodeSystemMetrics struct {
	release  string
	version  string
	uptime   float64
	loadavg  [3]float64
	totalMem uint64
	freeMem  uint64
	cpus     []nodeCPUInfo
}

type nodeProcessMetrics struct {
	userCPU             time.Duration
	systemCPU           time.Duration
	maxRSS              uint64
	rss                 uint64
	minorPageFault      int64
	majorPageFault      int64
	fsRead              int64
	fsWrite             int64
	voluntarySwitches   int64
	involuntarySwitches int64
}

func cpuInfoValues(metrics []nodeCPUInfo) []map[string]any {
	result := make([]map[string]any, len(metrics))
	for index, cpu := range metrics {
		result[index] = map[string]any{"model": cpu.model, "speed": cpu.speed, "times": cpu.times}
	}
	return result
}
