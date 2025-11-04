package serverchanturbo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/komari-monitor/komari/utils/messageSender/factory"
)

// ServerChanTurboSender 为 Server酱 Turbo 推送实现
type ServerChanTurboSender struct {
	Addition
}

// GetName 返回推送通道名称
func (s *ServerChanTurboSender) GetName() string {
	return "Server酱Turbo"
}

// GetConfiguration 返回配置结构体指针
func (s *ServerChanTurboSender) GetConfiguration() factory.Configuration {
	return &s.Addition
}

// Init 初始化（当前无需处理）
func (s *ServerChanTurboSender) Init() error { return nil }

// Destroy 清理（当前无需处理）
func (s *ServerChanTurboSender) Destroy() error { return nil }

// SendTextMessage 固定以 JSON 发送，仅组装 title 与 desp，支持可选 channel/noip/openid
func (s *ServerChanTurboSender) SendTextMessage(message, title string) error {
	apiURL := strings.TrimSpace(s.Addition.APIURL)
	if apiURL == "" {
		return fmt.Errorf("未配置 Server酱 Turbo 接口地址(api_url)")
	}

	finalTitle := strings.TrimSpace(title)
	finalMessage := strings.TrimSpace(message)
	if finalTitle == "" && finalMessage == "" {
		return fmt.Errorf("serverchanturbo: 标题与正文均为空")
	}

	payload := map[string]interface{}{
		"title": finalTitle,
		"desp":  finalMessage,
	}
	if ch := strings.TrimSpace(s.Addition.Channel); ch != "" {
		payload["channel"] = ch
	}
	if noip := strings.TrimSpace(s.Addition.NoIP); noip == "1" {
		payload["noip"] = "1"
	}
	if openid := strings.TrimSpace(s.Addition.OpenID); openid != "" {
		payload["openid"] = openid
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("serverchanturbo: 组装 JSON 失败: %v", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return fmt.Errorf("serverchanturbo: 创建请求失败: %v", err)
	}
	// 使用 JSON 传递参数，需要设置 Content-Type
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("serverchanturbo: 发送请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		msg := strings.TrimSpace(string(b))
		if msg == "" {
			return fmt.Errorf("serverchanturbo: 接口返回非 2xx 状态码: %d", resp.StatusCode)
		}
		return fmt.Errorf("serverchanturbo: 接口返回非 2xx 状态码: %d，响应: %s", resp.StatusCode, msg)
	}

	return nil
}

// 确保实现 IMessageSender 接口
var _ factory.IMessageSender = (*ServerChanTurboSender)(nil)
