//go:build windows

package jsruntime

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

var (
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	psapi                    = windows.NewLazySystemDLL("psapi.dll")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetSystemTimes       = kernel32.NewProc("GetSystemTimes")
	procGetProcessIOCounters = kernel32.NewProc("GetProcessIoCounters")
	procGetProcessMemoryInfo = psapi.NewProc("GetProcessMemoryInfo")
)

func readNodeSystemMetrics() (nodeSystemMetrics, error) {
	memory := windowsMemoryStatus{length: uint32(unsafe.Sizeof(windowsMemoryStatus{}))}
	if result, _, callErr := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&memory))); result == 0 {
		return nodeSystemMetrics{}, fmt.Errorf("GlobalMemoryStatusEx: %w", callErr)
	}
	version := windows.RtlGetVersion()
	release := fmt.Sprintf("%d.%d.%d", version.MajorVersion, version.MinorVersion, version.BuildNumber)
	cpus, err := windowsCPUs()
	if err != nil {
		return nodeSystemMetrics{}, err
	}
	return nodeSystemMetrics{
		release: release, version: release, uptime: windows.DurationSinceBoot().Seconds(),
		totalMem: memory.totalPhys, freeMem: memory.availPhys, cpus: cpus,
	}, nil
}

func windowsCPUs() ([]nodeCPUInfo, error) {
	model, speed := runtime.GOARCH, 0
	if key, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\CentralProcessor\0`, registry.QUERY_VALUE); err == nil {
		defer key.Close()
		if value, _, valueErr := key.GetStringValue("ProcessorNameString"); valueErr == nil {
			model = value
		}
		if value, _, valueErr := key.GetIntegerValue("~MHz"); valueErr == nil {
			speed = int(value)
		}
	}
	idle, kernel, user := windows.Filetime{}, windows.Filetime{}, windows.Filetime{}
	if result, _, callErr := procGetSystemTimes.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user))); result == 0 {
		return nil, fmt.Errorf("GetSystemTimes: %w", callErr)
	}
	count := runtime.NumCPU()
	if count < 1 {
		count = 1
	}
	perCPU := func(value int64) uint64 {
		if value <= 0 {
			return 0
		}
		return uint64(value/int64(time.Millisecond)) / uint64(count)
	}
	idleDuration := windowsFiletimeDuration(idle)
	kernelDuration := windowsFiletimeDuration(kernel)
	userDuration := windowsFiletimeDuration(user)
	result := make([]nodeCPUInfo, count)
	for index := range result {
		result[index] = nodeCPUInfo{model: model, speed: speed, times: map[string]uint64{
			"user": perCPU(int64(userDuration)), "nice": 0, "sys": perCPU(int64(kernelDuration - idleDuration)),
			"idle": perCPU(int64(idleDuration)), "irq": 0,
		}}
	}
	return result, nil
}

func readNodeProcessMetrics() (nodeProcessMetrics, error) {
	handle := windows.CurrentProcess()
	creation, exit, kernel, user := windows.Filetime{}, windows.Filetime{}, windows.Filetime{}, windows.Filetime{}
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return nodeProcessMetrics{}, err
	}
	memory := windowsProcessMemoryCounters{cb: uint32(unsafe.Sizeof(windowsProcessMemoryCounters{}))}
	if result, _, callErr := procGetProcessMemoryInfo.Call(uintptr(handle), uintptr(unsafe.Pointer(&memory)), uintptr(memory.cb)); result == 0 {
		return nodeProcessMetrics{}, fmt.Errorf("GetProcessMemoryInfo: %w", callErr)
	}
	ioCounters := windowsIOCounters{}
	_, _, _ = procGetProcessIOCounters.Call(uintptr(handle), uintptr(unsafe.Pointer(&ioCounters)))
	return nodeProcessMetrics{
		userCPU: windowsFiletimeDuration(user), systemCPU: windowsFiletimeDuration(kernel),
		maxRSS: uint64(memory.peakWorkingSetSize) / 1024, rss: uint64(memory.workingSetSize),
		minorPageFault: int64(memory.pageFaultCount), fsRead: int64(ioCounters.readOperationCount),
		fsWrite: int64(ioCounters.writeOperationCount),
	}, nil
}

func windowsFiletimeDuration(value windows.Filetime) time.Duration {
	ticks := uint64(value.HighDateTime)<<32 | uint64(value.LowDateTime)
	return time.Duration(ticks * 100)
}
