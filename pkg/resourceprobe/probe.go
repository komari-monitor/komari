package resourceprobe

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const (
	minimumMemoryBytes      = uint64(1536 * 1024 * 1024)
	minimumDiskFreeBytes    = uint64(15 * 1024 * 1024 * 1024)
	minimumCPUOpsPerSecond  = 30_000_000
	minimumWriteBytesSecond = 5 * 1024 * 1024
	minimumWriteIOPS        = 1000
	cpuBenchmarkDuration    = 750 * time.Millisecond
	diskBenchmarkDuration   = 8 * time.Second
	diskBenchmarkFileSize   = int64(64 * 1024 * 1024)
	diskBlockSize           = int64(4096)
)

type Result struct {
	LowResource         bool
	MemoryBytes         uint64
	DiskFreeBytes       uint64
	CPUOpsPerSecond     float64
	WriteBytesPerSecond float64
	WriteIOPS           float64
	Reasons             []string
}

// Detect measures only when the low-resource setting has never been saved.
// The caller should impose an outer deadline; the built-in benchmarks finish
// well inside one minute under normal conditions.
func Detect(ctx context.Context, dataDir string) Result {
	result := Result{}
	result.MemoryBytes, _ = systemMemoryBytes()
	result.DiskFreeBytes, _ = systemDiskFreeBytes(dataDir)

	if result.MemoryBytes > 0 && result.MemoryBytes < minimumMemoryBytes {
		result.Reasons = append(result.Reasons, fmt.Sprintf("memory=%dMiB", result.MemoryBytes/(1024*1024)))
	}
	if result.DiskFreeBytes > 0 && result.DiskFreeBytes < minimumDiskFreeBytes {
		result.Reasons = append(result.Reasons, fmt.Sprintf("disk_free=%dMiB", result.DiskFreeBytes/(1024*1024)))
	}
	if len(result.Reasons) > 0 {
		result.LowResource = true
		return result
	}

	result.CPUOpsPerSecond = benchmarkSingleCore(ctx, cpuBenchmarkDuration)
	if result.CPUOpsPerSecond > 0 && result.CPUOpsPerSecond < minimumCPUOpsPerSecond {
		result.Reasons = append(result.Reasons, fmt.Sprintf("cpu=%.0fops/s", result.CPUOpsPerSecond))
	}

	var err error
	result.WriteBytesPerSecond, result.WriteIOPS, err = benchmarkRandomWrite(ctx, dataDir, diskBenchmarkDuration)
	if err != nil {
		result.Reasons = append(result.Reasons, "disk_benchmark="+err.Error())
	} else {
		if result.WriteBytesPerSecond < minimumWriteBytesSecond {
			result.Reasons = append(result.Reasons, fmt.Sprintf("random_write=%.2fMiB/s", result.WriteBytesPerSecond/(1024*1024)))
		}
		if result.WriteIOPS < minimumWriteIOPS {
			result.Reasons = append(result.Reasons, fmt.Sprintf("random_write=%.0fIOPS", result.WriteIOPS))
		}
	}
	result.LowResource = len(result.Reasons) > 0
	return result
}

var cpuBenchmarkSink uint64

func benchmarkSingleCore(ctx context.Context, duration time.Duration) float64 {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	start := time.Now()
	deadline := start.Add(duration)
	var operations uint64
	x := uint64(0x9e3779b97f4a7c15)
	for time.Now().Before(deadline) {
		for range 100_000 {
			x ^= x << 13
			x ^= x >> 7
			x ^= x << 17
			operations++
		}
		select {
		case <-ctx.Done():
			cpuBenchmarkSink = x
			return float64(operations) / time.Since(start).Seconds()
		default:
		}
	}
	cpuBenchmarkSink = x
	return float64(operations) / time.Since(start).Seconds()
}

func benchmarkRandomWrite(ctx context.Context, dataDir string, duration time.Duration) (float64, float64, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return 0, 0, err
	}
	f, err := os.CreateTemp(dataDir, ".komari-resource-probe-*")
	if err != nil {
		return 0, 0, err
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	if err := f.Truncate(diskBenchmarkFileSize); err != nil {
		return 0, 0, err
	}

	block := make([]byte, diskBlockSize)
	var state uint64 = 0x243f6a8885a308d3
	blocks := uint64(diskBenchmarkFileSize / diskBlockSize)
	start := time.Now()
	deadline := start.Add(duration)
	var writes uint64
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return 0, 0, ctx.Err()
		default:
		}
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		offset := int64(state%blocks) * diskBlockSize
		if _, err := f.WriteAt(block, offset); err != nil {
			return 0, 0, err
		}
		if err := f.Sync(); err != nil {
			return 0, 0, err
		}
		writes++
	}
	elapsed := time.Since(start).Seconds()
	if elapsed <= 0 || writes == 0 {
		return 0, 0, errors.New("no writes completed")
	}
	return float64(writes*uint64(diskBlockSize)) / elapsed, float64(writes) / elapsed, nil
}

func absoluteDir(path string) (string, error) {
	if path == "" {
		path = "."
	}
	return filepath.Abs(path)
}
