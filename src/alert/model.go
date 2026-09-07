package alert

import "time"

type ChannelType string

const (
	ChannelFeishu   ChannelType = "feishu"
	ChannelDingtalk ChannelType = "dingtalk"
	ChannelWecom    ChannelType = "wecom"
	ChannelGeneric  ChannelType = "generic"
)

type AlertLevel string

const (
	LevelInfo     AlertLevel = "info"
	LevelWarning  AlertLevel = "warning"
	LevelCritical AlertLevel = "critical"
)

type AlertType string

const (
	AlertCrash         AlertType = "crash"
	AlertMemLeak       AlertType = "mem_leak"
	AlertDeployFail    AlertType = "deploy_fail"
	AlertHighCPU       AlertType = "high_cpu"
	AlertHighDisk      AlertType = "high_disk"
	AlertHighGoroutine AlertType = "high_goroutine"
	AlertTest          AlertType = "test"
)

type AlertConfig struct {
	Enabled            bool        `json:"enabled"`
	Channel            ChannelType `json:"channel"`
	WebhookURL         string      `json:"webhookUrl"`
	Secret             string      `json:"secret"`
	CooldownMin        int         `json:"cooldownMin"`
	CPUThreshold       float64     `json:"cpuThreshold"`
	MemThreshold       float64     `json:"memThreshold"`
	DiskThreshold      float64     `json:"diskThreshold"`
	GoroutineThreshold int         `json:"goroutineThreshold"`
	NotifyOnCrash      bool        `json:"notifyOnCrash"`
	NotifyOnDeployFail bool        `json:"notifyOnDeployFail"`
	NotifyOnMemLeak    bool        `json:"notifyOnMemLeak"`
}

func DefaultConfig() AlertConfig {
	return AlertConfig{
		Enabled:            false,
		Channel:            ChannelFeishu,
		WebhookURL:         "",
		Secret:             "",
		CooldownMin:        10,
		CPUThreshold:       85.0,
		MemThreshold:       85.0,
		DiskThreshold:      90.0,
		GoroutineThreshold: 5000,
		NotifyOnCrash:      true,
		NotifyOnDeployFail: true,
		NotifyOnMemLeak:    true,
	}
}

type AlertEvent struct {
	Type      AlertType      `json:"type"`
	Level     AlertLevel     `json:"level"`
	Title     string         `json:"title"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
	HostName  string         `json:"hostName"`
}
