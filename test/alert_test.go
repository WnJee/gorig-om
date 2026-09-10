package test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WnJee/gorig-om/src/alert"
)

func TestAlertConfig(t *testing.T) {
	s := alert.S()
	cfg := s.GetConfig()
	if cfg.CooldownMin <= 0 {
		t.Fatalf("expected positive default cooldown, got %d", cfg.CooldownMin)
	}

	newCfg := alert.AlertConfig{
		Enabled:            true,
		Channel:            alert.ChannelFeishu,
		WebhookURL:         "http://127.0.0.1:9999/webhook",
		Secret:             "test-secret",
		CooldownMin:        5,
		CPUThreshold:       80.0,
		MemThreshold:       80.0,
		DiskThreshold:      85.0,
		GoroutineThreshold: 3000,
		NotifyOnCrash:      true,
		NotifyOnDeployFail: true,
		NotifyOnMemLeak:    true,
	}

	if err := s.SaveConfig(newCfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	loaded := s.GetConfig()
	if !loaded.Enabled || loaded.WebhookURL != newCfg.WebhookURL || loaded.CooldownMin != 5 {
		t.Fatalf("config mismatch after save: %+v", loaded)
	}
}

func TestAlertDispatch(t *testing.T) {
	var receivedCount int32
	var lastPayload map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&receivedCount, 1)
		_ = json.NewDecoder(r.Body).Decode(&lastPayload)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":0,"msg":"success"}`))
	}))
	defer server.Close()

	s := alert.S()
	_ = s.SaveConfig(alert.AlertConfig{
		Enabled:     true,
		Channel:     alert.ChannelFeishu,
		WebhookURL:  server.URL,
		CooldownMin: 10,
	})

	ctx := context.Background()
	event := alert.AlertEvent{
		Type:      alert.AlertTest,
		Level:     alert.LevelCritical,
		Title:     "测试告警",
		Message:   "自动化测试告警内容",
		Timestamp: time.Now(),
		Details:   map[string]any{"module": "test"},
	}

	s.Send(ctx, event)

	// Wait briefly for async HTTP dispatch
	time.Sleep(200 * time.Millisecond)

	if atomic.LoadInt32(&receivedCount) != 1 {
		t.Fatalf("expected 1 webhook call, got %d", atomic.LoadInt32(&receivedCount))
	}
	if lastPayload == nil || lastPayload["msg_type"] != "interactive" {
		t.Fatalf("unexpected feishu payload: %+v", lastPayload)
	}
}
