//go:build windows

package metrics

import (
	"fmt"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

type windowsMemoryStatus struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

type windowsProcessMemoryCounters struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
}

type windowsIOCounters struct {
	readOperationCount  uint64
	writeOperationCount uint64
	otherOperationCount uint64
	readTransferCount   uint64
	writeTransferCount  uint64
	otherTransferCount  uint64
}

type windowsProcessorPerformance struct {
	idleTime       int64
	kernelTime     int64
	userTime       int64
	dpcTime        int64
	interruptTime  int64
	interruptCount uint32
	_              uint32
}

var (
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	psapi                    = windows.NewLazySystemDLL("psapi.dll")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetProcessIOCounters = kernel32.NewProc("GetProcessIoCounters")
	procGetProcessMemoryInfo = psapi.NewProc("GetProcessMemoryInfo")
)

func ReadSystem() (System, error) {
	memory := windowsMemoryStatus{length: uint32(unsafe.Sizeof(windowsMemoryStatus{}))}
	if result, _, callErr := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&memory))); result == 0 {
		return System{}, fmt.Errorf("GlobalMemoryStatusEx: %w", callErr)
	}
	version := windows.RtlGetVersion()
	release := fmt.Sprintf("%d.%d.%d", version.MajorVersion, version.MinorVersion, version.BuildNumber)
	cpus, err := windowsCPUs()
	if err != nil {
		return System{}, err
	}
	return System{
		Release: release, Version: release, Uptime: windows.DurationSinceBoot().Seconds(),
		TotalMem: memory.totalPhys, FreeMem: memory.availPhys, CPUs: cpus,
	}, nil
}

func windowsCPUs() ([]CPUInfo, error) {
	count := runtime.NumCPU()
	if count < 1 {
		count = 1
	}
	metrics := make([]windowsProcessorPerformance, count)
	entrySize := uint32(unsafe.Sizeof(windowsProcessorPerformance{}))
	var returned uint32
	err := windows.NtQuerySystemInformation(
		windows.SystemProcessorPerformanceInformation,
		unsafe.Pointer(&metrics[0]),
		uint32(len(metrics))*entrySize,
		&returned,
	)
	if err != nil && returned > uint32(len(metrics))*entrySize {
		metrics = make([]windowsProcessorPerformance, (returned+entrySize-1)/entrySize)
		err = windows.NtQuerySystemInformation(
			windows.SystemProcessorPerformanceInformation,
			unsafe.Pointer(&metrics[0]),
			uint32(len(metrics))*entrySize,
			&returned,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("NtQuerySystemInformation(SystemProcessorPerformanceInformation): %w", err)
	}
	if actual := int(returned / entrySize); actual > 0 && actual < len(metrics) {
		metrics = metrics[:actual]
	}
	result := make([]CPUInfo, len(metrics))
	for index, metric := range metrics {
		model, speed := windowsCPUIdentity(index)
		result[index] = CPUInfo{Model: model, Speed: speed, Times: map[string]uint64{
			"user": windows100nsMilliseconds(metric.userTime), "nice": 0,
			"sys":  windows100nsMilliseconds(metric.kernelTime - metric.idleTime),
			"idle": windows100nsMilliseconds(metric.idleTime), "irq": windows100nsMilliseconds(metric.interruptTime),
		}}
	}
	return result, nil
}

func windowsCPUIdentity(index int) (string, int) {
	model, speed := runtime.GOARCH, 0
	path := fmt.Sprintf(`HARDWARE\DESCRIPTION\System\CentralProcessor\%d`, index)
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE)
	if err != nil {
		return model, speed
	}
	defer key.Close()
	if value, _, valueErr := key.GetStringValue("ProcessorNameString"); valueErr == nil {
		model = value
	}
	if value, _, valueErr := key.GetIntegerValue("~MHz"); valueErr == nil {
		speed = int(value)
	}
	return model, speed
}

func windows100nsMilliseconds(value int64) uint64 {
	if value <= 0 {
		return 0
	}
	return uint64(value / 10_000)
}

func ReadProcess() (Process, error) {
	handle := windows.CurrentProcess()
	creation, exit, kernel, user := windows.Filetime{}, windows.Filetime{}, windows.Filetime{}, windows.Filetime{}
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return Process{}, err
	}
	memory := windowsProcessMemoryCounters{cb: uint32(unsafe.Sizeof(windowsProcessMemoryCounters{}))}
	if result, _, callErr := procGetProcessMemoryInfo.Call(uintptr(handle), uintptr(unsafe.Pointer(&memory)), uintptr(memory.cb)); result == 0 {
		return Process{}, fmt.Errorf("GetProcessMemoryInfo: %w", callErr)
	}
	ioCounters := windowsIOCounters{}
	_, _, _ = procGetProcessIOCounters.Call(uintptr(handle), uintptr(unsafe.Pointer(&ioCounters)))
	return Process{
		UserCPU: windowsFiletimeDuration(user), SystemCPU: windowsFiletimeDuration(kernel),
		MaxRSS: uint64(memory.peakWorkingSetSize) / 1024, RSS: uint64(memory.workingSetSize),
		MinorPageFault: int64(memory.pageFaultCount), FSRead: int64(ioCounters.readOperationCount),
		FSWrite: int64(ioCounters.writeOperationCount),
	}, nil
}

func windowsFiletimeDuration(value windows.Filetime) time.Duration {
	ticks := uint64(value.HighDateTime)<<32 | uint64(value.LowDateTime)
	return time.Duration(ticks * 100)
}
