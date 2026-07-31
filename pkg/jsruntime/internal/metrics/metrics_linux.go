//go:build linux

package metrics

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

func ReadSystem() (System, error) {
	info := syscall.Sysinfo_t{}
	if err := syscall.Sysinfo(&info); err != nil {
		return System{}, err
	}
	unit := uint64(info.Unit)
	if unit == 0 {
		unit = 1
	}
	release, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return System{}, err
	}
	version, err := os.ReadFile("/proc/sys/kernel/version")
	if err != nil {
		return System{}, err
	}
	cpus, err := linuxCPUs()
	if err != nil {
		return System{}, err
	}
	const loadScale = 1 << 16
	return System{
		Release: strings.TrimSpace(string(release)), Version: strings.TrimSpace(string(version)), Uptime: float64(info.Uptime),
		LoadAvg:  [3]float64{float64(info.Loads[0]) / loadScale, float64(info.Loads[1]) / loadScale, float64(info.Loads[2]) / loadScale},
		TotalMem: uint64(info.Totalram) * unit, FreeMem: uint64(info.Freeram) * unit, CPUs: cpus,
	}, nil
}

func linuxCPUs() ([]CPUInfo, error) {
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
	result := make([]CPUInfo, count)
	for index := range result {
		modelIndex := index
		if modelIndex >= len(models) {
			modelIndex = len(models) - 1
		}
		result[index] = CPUInfo{Model: models[modelIndex], Speed: speeds[modelIndex], Times: times[index]}
	}
	return result, nil
}

func ReadProcess() (Process, error) {
	usage := syscall.Rusage{}
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return Process{}, err
	}
	statm, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return Process{}, err
	}
	fields := strings.Fields(string(statm))
	if len(fields) < 2 {
		return Process{}, fmt.Errorf("invalid /proc/self/statm")
	}
	residentPages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return Process{}, err
	}
	return Process{
		UserCPU:   time.Duration(usage.Utime.Sec)*time.Second + time.Duration(usage.Utime.Usec)*time.Microsecond,
		SystemCPU: time.Duration(usage.Stime.Sec)*time.Second + time.Duration(usage.Stime.Usec)*time.Microsecond,
		MaxRSS:    uint64(usage.Maxrss), RSS: residentPages * uint64(os.Getpagesize()),
		MinorPageFault: int64(usage.Minflt), MajorPageFault: int64(usage.Majflt), FSRead: int64(usage.Inblock), FSWrite: int64(usage.Oublock),
		VoluntarySwitches: int64(usage.Nvcsw), InvoluntarySwitches: int64(usage.Nivcsw),
	}, nil
}
