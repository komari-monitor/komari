package bark

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/komari-monitor/komari/internal/messageSender/factory"
)

type BarkSender struct {
	Addition
}

func (b *BarkSender) GetName() string {
	return "bark"
}

func (b *BarkSender) GetConfiguration() factory.Configuration {
	return &b.Addition
}

func (b *BarkSender) Init() error {
	// 验证 ServerURL 格式
	if b.Addition.ServerURL != "" {
		if _, err := url.Parse(b.Addition.ServerURL); err != nil {
			return fmt.Errorf("invalid server URL: %v", err)
		}
	}
	return nil
}

func (b *BarkSender) Destroy() error {
	return nil
}

func (b *BarkSender) SendTextMessage(message, title string) error {
	if b.Addition.DeviceKey == "" {
		return fmt.Errorf("device key is required")
	}

	if message == "" {
		return fmt.Errorf("message is empty")
	}

	// 准备请求数据
	payload := map[string]interface{}{
		"body":       message,
		"device_key": b.Addition.DeviceKey,
	}

	// 如果有标题，添加标题
	if title != "" {
		payload["title"] = title
	}

	// 添加可选参数
	if b.Addition.Icon != "" {
		payload["icon"] = b.Addition.Icon
	}

	if b.Addition.Level != "" {
		payload["level"] = b.Addition.Level
	}

	// 序列化 JSON
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %v", err)
	}

	// 构建请求 URL
	serverURL := b.Addition.ServerURL
	if serverURL == "" {
		serverURL = "https://api.day.app"
	}

	// 确保 URL 以正确的格式结尾
	serverURL = strings.TrimRight(serverURL, "/")
	requestURL := fmt.Sprintf("%s/push", serverURL)

	// 发送 HTTP POST 请求
	resp, err := http.Post(requestURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	// 检查响应状态
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bark API returned non-OK status: %d", resp.StatusCode)
	}

	// 解析响应
	var result struct {
		Code    int         `json:"code"`
		Message string      `json:"message"`
		Data    interface{} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		// 如果解析失败，认为发送成功（某些 Bark 服务器可能返回纯文本）
		return nil
	}

	// 检查 Bark API 响应
	if result.Code != 200 {
		return fmt.Errorf("bark API error (code: %d): %s", result.Code, result.Message)
	}

	return nil
}

// 确保实现了 IMessageSender 接口
var _ factory.IMessageSender = (*BarkSender)(nil)
