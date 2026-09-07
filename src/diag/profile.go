package diag

import (
	"bytes"
	"context"
	"fmt"
	"runtime"
	"runtime/pprof"
	"sync"
	"time"
)

var (
	cpuMu sync.Mutex
)

func CaptureCPUProfile(ctx context.Context, seconds int) ([]byte, error) {
	if seconds <= 0 {
		seconds = 10
	}
	if seconds > 60 {
		seconds = 60
	}

	if !cpuMu.TryLock() {
		return nil, fmt.Errorf("CPU profile is already running, please try again later")
	}
	defer cpuMu.Unlock()

	var buf bytes.Buffer
	if err := pprof.StartCPUProfile(&buf); err != nil {
		return nil, fmt.Errorf("failed to start CPU profile: %w", err)
	}

	timer := time.NewTimer(time.Duration(seconds) * time.Second)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		pprof.StopCPUProfile()
		return nil, ctx.Err()
	case <-timer.C:
		pprof.StopCPUProfile()
	}

	return buf.Bytes(), nil
}

func CaptureHeapProfile() ([]byte, error) {
	runtime.GC()
	var buf bytes.Buffer
	if err := pprof.WriteHeapProfile(&buf); err != nil {
		return nil, fmt.Errorf("failed to write heap profile: %w", err)
	}
	return buf.Bytes(), nil
}
