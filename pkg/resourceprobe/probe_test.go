package resourceprobe

import (
	"context"
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
