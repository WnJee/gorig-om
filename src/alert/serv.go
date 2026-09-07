package alert

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jom-io/gorig/cache"
	"github.com/jom-io/gorig/utils/errors"
	"github.com/jom-io/gorig/utils/logger"
)

const (
	alertConfigKey = "om_alert_config"
)

var (
	servOnce sync.Once
	serv     *AlertServ
)

type AlertServ struct {
	mu         sync.RWMutex
	lastAlerts map[AlertType]time.Time
	httpClient *http.Client
}

func S() *AlertServ {
	servOnce.Do(func() {
		serv = &AlertServ{
			lastAlerts: make(map[AlertType]time.Time),
			httpClient: &http.Client{Timeout: 10 * time.Second},
		}
	})
	return serv
}

func (s *AlertServ) GetConfig() AlertConfig {
	cfg, err := cache.New[AlertConfig](cache.JSON).Get(alertConfigKey)
	if err != nil || (cfg.WebhookURL == "" && !cfg.Enabled) {
		return DefaultConfig()
	}
	return cfg
}

func (s *AlertServ) SaveConfig(cfg AlertConfig) *errors.Error {
	if cfg.CooldownMin <= 0 {
		cfg.CooldownMin = 10
	}
	if cfg.CPUThreshold <= 0 {
		cfg.CPUThreshold = 85.0
	}
	if cfg.MemThreshold <= 0 {
		cfg.MemThreshold = 85.0
	}
	if cfg.DiskThreshold <= 0 {
		cfg.DiskThreshold = 90.0
	}
	if cfg.GoroutineThreshold <= 0 {
		cfg.GoroutineThreshold = 5000
	}
	if err := cache.New[AlertConfig](cache.JSON).Set(alertConfigKey, cfg, 0); err != nil {
		return errors.Verify(fmt.Sprintf("Failed to save alert config: %v", err))
	}
	return nil
}

func (s *AlertServ) shouldThrottle(eventType AlertType, cooldownMin int) bool {
	if eventType == AlertTest {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	last, exists := s.lastAlerts[eventType]
	if !exists {
		s.lastAlerts[eventType] = time.Now()
		return false
	}

	cooldown := time.Duration(cooldownMin) * time.Minute
	if time.Since(last) < cooldown {
		return true
	}

	s.lastAlerts[eventType] = time.Now()
	return false
}

func (s *AlertServ) Send(ctx context.Context, event AlertEvent) {
	cfg := s.GetConfig()
	if !cfg.Enabled || cfg.WebhookURL == "" {
		return
	}

	// Filter based on event config
	switch event.Type {
	case AlertCrash:
		if !cfg.NotifyOnCrash {
			return
		}
	case AlertDeployFail:
		if !cfg.NotifyOnDeployFail {
			return
		}
	case AlertMemLeak:
		if !cfg.NotifyOnMemLeak {
			return
		}
	}

	if s.shouldThrottle(event.Type, cfg.CooldownMin) {
		logger.Warn(ctx, fmt.Sprintf("Alert [%s] throttled due to cooldown (%d min)", event.Type, cfg.CooldownMin))
		return
	}

	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	if event.HostName == "" {
		h, _ := os.Hostname()
		event.HostName = h
	}

	go func(evt AlertEvent, conf AlertConfig) {
		bgCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		if err := s.dispatch(bgCtx, evt, conf); err != nil {
			logger.Error(bgCtx, fmt.Sprintf("Failed to dispatch alert [%s]: %v", evt.Type, err))
		} else {
			logger.Info(bgCtx, fmt.Sprintf("Alert [%s] sent successfully via %s", evt.Type, conf.Channel))
		}
	}(event, cfg)
}

func (s *AlertServ) TestSend(ctx context.Context) *errors.Error {
	cfg := s.GetConfig()
	if cfg.WebhookURL == "" {
		return errors.Verify("Webhook URL is empty. Please configure Webhook URL first.")
	}

	h, _ := os.Hostname()
	event := AlertEvent{
		Type:      AlertTest,
		Level:     LevelInfo,
		Title:     "Gorig-OM 告警通知测试",
		Message:   "这是一条测试告警消息，表明当前 Webhook 告警通道已配置成功并正常连通。",
		Timestamp: time.Now(),
		HostName:  h,
		Details: map[string]any{
			"channel": cfg.Channel,
			"time":    time.Now().Format("2006-01-02 15:04:05"),
		},
	}

	if err := s.dispatch(ctx, event, cfg); err != nil {
		return errors.Verify(fmt.Sprintf("Failed to send test alert: %v", err))
	}
	return nil
}

func (s *AlertServ) dispatch(ctx context.Context, event AlertEvent, cfg AlertConfig) error {
	var (
		reqURL  = cfg.WebhookURL
		payload []byte
		err     error
	)

	switch cfg.Channel {
	case ChannelFeishu:
		payload, err = s.formatFeishu(event, cfg.Secret)
	case ChannelDingtalk:
		reqURL, payload, err = s.formatDingtalk(reqURL, event, cfg.Secret)
	case ChannelWecom:
		payload, err = s.formatWecom(event)
	default:
		payload, err = json.Marshal(event)
	}

	if err != nil {
		return fmt.Errorf("marshal payload error: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request error: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook responded with HTTP status %d", resp.StatusCode)
	}
	return nil
}

func (s *AlertServ) formatFeishu(event AlertEvent, secret string) ([]byte, error) {
	timeStr := event.Timestamp.Format("2006-01-02 15:04:05")
	var detailLines []string
	for k, v := range event.Details {
		detailLines = append(detailLines, fmt.Sprintf("• **%s**: %v", k, v))
	}

	content := fmt.Sprintf("**主机**: %s\n**级别**: %s\n**时间**: %s\n**说明**: %s",
		event.HostName, strings.ToUpper(string(event.Level)), timeStr, event.Message)
	if len(detailLines) > 0 {
		content += "\n**详细信息**:\n" + strings.Join(detailLines, "\n")
	}

	nowUnix := time.Now().Unix()
	body := map[string]any{
		"msg_type": "interactive",
		"card": map[string]any{
			"header": map[string]any{
				"title": map[string]any{
					"tag":     "plain_text",
					"content": fmt.Sprintf("【%s】%s", strings.ToUpper(string(event.Level)), event.Title),
				},
				"template": s.feishuHeaderColor(event.Level),
			},
			"elements": []any{
				map[string]any{
					"tag":     "markdown",
					"content": content,
				},
			},
		},
	}

	if secret != "" {
		sign, _ := s.genFeishuSign(secret, nowUnix)
		body["timestamp"] = strconv.FormatInt(nowUnix, 10)
		body["sign"] = sign
	}

	return json.Marshal(body)
}

func (s *AlertServ) feishuHeaderColor(level AlertLevel) string {
	switch level {
	case LevelCritical:
		return "red"
	case LevelWarning:
		return "orange"
	default:
		return "blue"
	}
}

func (s *AlertServ) genFeishuSign(secret string, timestamp int64) (string, error) {
	stringToSign := fmt.Sprintf("%v\n%s", timestamp, secret)
	h := hmac.New(sha256.New, []byte(stringToSign))
	return base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

func (s *AlertServ) formatDingtalk(rawURL string, event AlertEvent, secret string) (string, []byte, error) {
	targetURL := rawURL
	if secret != "" {
		timestamp := time.Now().UnixMilli()
		stringToSign := fmt.Sprintf("%d\n%s", timestamp, secret)
		h := hmac.New(sha256.New, []byte(secret))
		h.Write([]byte(stringToSign))
		sign := url.QueryEscape(base64.StdEncoding.EncodeToString(h.Sum(nil)))

		if strings.Contains(targetURL, "?") {
			targetURL = fmt.Sprintf("%s&timestamp=%d&sign=%s", targetURL, timestamp, sign)
		} else {
			targetURL = fmt.Sprintf("%s?timestamp=%d&sign=%s", targetURL, timestamp, sign)
		}
	}

	timeStr := event.Timestamp.Format("2006-01-02 15:04:05")
	var detailLines []string
	for k, v := range event.Details {
		detailLines = append(detailLines, fmt.Sprintf("- **%s**: %v", k, v))
	}

	markdownText := fmt.Sprintf("### %s\n\n> **主机**: %s\n\n> **级别**: %s\n\n> **时间**: %s\n\n**详细说明**:\n%s",
		event.Title, event.HostName, strings.ToUpper(string(event.Level)), timeStr, event.Message)
	if len(detailLines) > 0 {
		markdownText += "\n\n" + strings.Join(detailLines, "\n\n")
	}

	body := map[string]any{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"title": event.Title,
			"text":  markdownText,
		},
	}
	b, err := json.Marshal(body)
	return targetURL, b, err
}

func (s *AlertServ) formatWecom(event AlertEvent) ([]byte, error) {
	timeStr := event.Timestamp.Format("2006-01-02 15:04:05")
	color := "comment"
	if event.Level == LevelCritical {
		color = "warning"
	}

	var detailLines []string
	for k, v := range event.Details {
		detailLines = append(detailLines, fmt.Sprintf("> %s: <font color=\"comment\">%v</font>", k, v))
	}

	content := fmt.Sprintf("### <font color=\"%s\">%s</font>\n> 主机: %s\n> 级别: %s\n> 时间: %s\n\n%s",
		color, event.Title, event.HostName, strings.ToUpper(string(event.Level)), timeStr, event.Message)
	if len(detailLines) > 0 {
		content += "\n" + strings.Join(detailLines, "\n")
	}

	body := map[string]any{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"content": content,
		},
	}
	return json.Marshal(body)
}
