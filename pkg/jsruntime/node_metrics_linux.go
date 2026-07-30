//go:build linux

package jsruntime

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func readNodeSystemMetrics() (nodeSystemMetrics, error) {
	info := syscall.Sysinfo_t{}
	if err := syscall.Sysinfo(&info); err != nil {
		return nodeSystemMetrics{}, err
	}
	unit := uint64(info.Unit)
	if unit == 0 {
		unit = 1
	}
	release, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return nodeSystemMetrics{}, err
	}
	version, err := os.ReadFile("/proc/sys/kernel/version")
	if err != nil {
		return nodeSystemMetrics{}, err
	}
	cpus, err := linuxCPUs()
	if err != nil {
		return nodeSystemMetrics{}, err
	}
	const loadScale = 1 << 16
	return nodeSystemMetrics{
		release: strings.TrimSpace(string(release)), version: strings.TrimSpace(string(version)), uptime: float64(info.Uptime),
		loadavg:  [3]float64{float64(info.Loads[0]) / loadScale, float64(info.Loads[1]) / loadScale, float64(info.Loads[2]) / loadScale},
		totalMem: uint64(info.Totalram) * unit, freeMem: uint64(info.Freeram) * unit, cpus: cpus,
	}, nil
}

func linuxCPUs() ([]nodeCPUInfo, error) {
	cpuInfo, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return nil, err
	}
	models, speeds := make([]string, 0), make([]int, 0)
	model, speed := "", 0
	scanner := bufio.NewScanner(bytes.NewReader(cpuInfo))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			models, speeds = append(models, model), append(speeds, speed)
			model, speed = "", 0
			continue
		}
		name, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		switch strings.TrimSpace(name) {
		case "model name", "Hardware", "Processor":
			if model == "" {
				model = strings.TrimSpace(value)
			}
		case "cpu MHz":
			mhz, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
			speed = int(mhz)
		}
	}
	if model != "" || len(models) == 0 {
		models, speeds = append(models, model), append(speeds, speed)
	}
	stat, err := os.ReadFile("/proc/stat")
	if err != nil {
		return nil, err
	}
	times := make([]map[string]uint64, 0, len(models))
	for _, line := range strings.Split(string(stat), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 8 || !strings.HasPrefix(fields[0], "cpu") || fields[0] == "cpu" {
			continue
		}
		values := make([]uint64, 7)
		for index := range values {
			value, _ := strconv.ParseUint(fields[index+1], 10, 64)
			values[index] = value * 10
		}
		times = append(times, map[string]uint64{"user": values[0], "nice": values[1], "sys": values[2], "idle": values[3], "irq": values[5] + values[6]})
	}
	count := len(times)
	if count == 0 {
		return nil, fmt.Errorf("no CPU entries in /proc/stat")
	}
	result := make([]nodeCPUInfo, count)
	for index := range result {
		modelIndex := index
		if modelIndex >= len(models) {
			modelIndex = len(models) - 1
		}
		result[index] = nodeCPUInfo{model: models[modelIndex], speed: speeds[modelIndex], times: times[index]}
	}
	return result, nil
}

func readNodeProcessMetrics() (nodeProcessMetrics, error) {
	usage := syscall.Rusage{}
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return nodeProcessMetrics{}, err
	}
	statm, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return nodeProcessMetrics{}, err
	}
	fields := strings.Fields(string(statm))
	if len(fields) < 2 {
		return nodeProcessMetrics{}, fmt.Errorf("invalid /proc/self/statm")
	}
	residentPages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return nodeProcessMetrics{}, err
	}
	return nodeProcessMetrics{
		userCPU:   time.Duration(usage.Utime.Sec)*time.Second + time.Duration(usage.Utime.Usec)*time.Microsecond,
		systemCPU: time.Duration(usage.Stime.Sec)*time.Second + time.Duration(usage.Stime.Usec)*time.Microsecond,
		maxRSS:    uint64(usage.Maxrss), rss: residentPages * uint64(os.Getpagesize()),
		minorPageFault: int64(usage.Minflt), majorPageFault: int64(usage.Majflt), fsRead: int64(usage.Inblock), fsWrite: int64(usage.Oublock),
		voluntarySwitches: int64(usage.Nvcsw), involuntarySwitches: int64(usage.Nivcsw),
	}, nil
}
