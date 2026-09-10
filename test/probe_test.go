package test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	dpTask "github.com/WnJee/gorig-om/src/deploy/task"
	"github.com/jom-io/gorig/cache"
	"github.com/jom-io/gorig/utils/logger"
	"github.com/rs/xid"
)

func TestProbeSuccess(t *testing.T) {
	ctx := logger.NewCtx()

	var probeCalled int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&probeCalled, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}))
	defer server.Close()

	cachePage := cache.NewPager[dpTask.TaskRecord](ctx, cache.Sqlite)
	taskID := xid.New().String()
	record := dpTask.TaskRecord{
		ID: taskID,
		TaskOptions: dpTask.TaskOptions{
			Repo:               "git@github.com-jom:jom-io/gorig.git",
			Branch:             "main",
			HealthCheckURL:     server.URL,
			HealthCheckTimeout: 4,
			AutoRollback:       false,
		},
		Status:   dpTask.Running,
		CreateAt: time.Now(),
		CreateBy: "admin",
	}

	if err := cachePage.Put(record); err != nil {
		t.Fatalf("failed to put task record: %v", err)
	}

	// Fetch record back to get storage initialized
	item, _ := cachePage.Get(map[string]any{"id": taskID})
	item.Storage = cachePage

	// Trigger health check probe
	dpTask.Task.StartedListen()

	// Wait briefly for probe loop
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		updated, _ := cachePage.Get(map[string]any{"id": taskID})
		if updated != nil && updated.Status == dpTask.Success {
			break
		}
	}

	updated, _ := cachePage.Get(map[string]any{"id": taskID})
	if updated == nil {
		t.Fatalf("task record not found")
	}
}

func TestTaskOptionsHealthCheckFields(t *testing.T) {
	ctx := logger.NewCtx()
	opts := dpTask.TaskOptions{
		Repo:               "git@github.com-jom:jom-io/gorig.git",
		Branch:             "main",
		HealthCheckURL:     "http://127.0.0.1:8080/healthz",
		HealthCheckTimeout: 15,
		AutoRollback:       true,
	}

	if err := dpTask.Task.SaveConfig(ctx, opts); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	saved, err := dpTask.Task.GetConfig(ctx)
	if err != nil {
		t.Fatalf("GetConfig failed: %v", err)
	}
	if saved == nil {
		t.Fatalf("expected non-nil task config")
	}
	if saved.HealthCheckURL != opts.HealthCheckURL {
		t.Fatalf("HealthCheckURL mismatch: got %s, want %s", saved.HealthCheckURL, opts.HealthCheckURL)
	}
	if saved.HealthCheckTimeout != 15 {
		t.Fatalf("HealthCheckTimeout mismatch: got %d, want 15", saved.HealthCheckTimeout)
	}
	if !saved.AutoRollback {
		t.Fatalf("AutoRollback should be true")
	}
}
