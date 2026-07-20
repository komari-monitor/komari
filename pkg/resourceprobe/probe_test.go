package resourceprobe

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestBenchmarkSingleCoreHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if got := benchmarkSingleCore(ctx, time.Minute); got < 0 {
		t.Fatalf("operations per second = %f", got)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("canceled benchmark took %s", elapsed)
	}
}

func TestBenchmarkRandomWriteUsesAlignedUnbufferedIO(t *testing.T) {
	dir := t.TempDir()
	bytesPerSecond, iops, err := benchmarkRandomWrite(context.Background(), dir, 250*time.Millisecond)
	if err != nil {
		t.Fatalf("benchmark random write: %v", err)
	}
	if bytesPerSecond <= 0 || iops <= 0 {
		t.Fatalf("invalid benchmark result: bytes/s=%f iops=%f", bytesPerSecond, iops)
	}
	t.Logf("unbuffered random write: %.2f MiB/s, %.0f IOPS", bytesPerSecond/(1024*1024), iops)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read benchmark directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("benchmark left temporary files: %#v", entries)
	}
}
