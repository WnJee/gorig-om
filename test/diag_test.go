package test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jom-io/gorig-om/src/diag"
)

func TestDiagGoroutinesClustering(t *testing.T) {
	// Spawn dummy goroutines waiting on a channel
	stopCh := make(chan struct{})
	var wg sync.WaitGroup

	const dummyCount = 10
	for i := 0; i < dummyCount; i++ {
		wg.Add(1)
		go func() {
			wg.Done()
			<-stopCh
		}()
	}
	wg.Wait()
	time.Sleep(50 * time.Millisecond)

	result, err := diag.DumpAndClusterGoroutines()
	close(stopCh)

	if err != nil {
		t.Fatalf("DumpAndClusterGoroutines failed: %v", err)
	}
	if result == nil {
		t.Fatalf("expected non-nil cluster result")
	}
	if result.TotalGoroutines < dummyCount {
		t.Fatalf("expected at least %d goroutines, got %d", dummyCount, result.TotalGoroutines)
	}
	if len(result.Groups) == 0 {
		t.Fatalf("expected at least 1 group")
	}

	foundChannelWait := false
	for _, grp := range result.Groups {
		if grp.Count >= dummyCount && strings.Contains(grp.State, "chan receive") {
			foundChannelWait = true
			break
		}
	}
	if !foundChannelWait {
		t.Logf("Groups found: %+v", result.Groups)
	}
}

func TestDiagProfiles(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Test CPU Profile (1 second)
	cpuData, err := diag.CaptureCPUProfile(ctx, 1)
	if err != nil {
		t.Fatalf("CaptureCPUProfile failed: %v", err)
	}
	if len(cpuData) == 0 {
		t.Fatalf("expected non-empty CPU profile data")
	}

	// Test Heap Profile
	heapData, err := diag.CaptureHeapProfile()
	if err != nil {
		t.Fatalf("CaptureHeapProfile failed: %v", err)
	}
	if len(heapData) == 0 {
		t.Fatalf("expected non-empty Heap profile data")
	}
}
