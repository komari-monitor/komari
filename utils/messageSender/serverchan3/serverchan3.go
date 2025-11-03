package serverchan3

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

// ServerChan3Sender 为 Server酱³ 推送实现
type ServerChan3Sender struct {
	Addition
}

// GetName 返回推送通道名称
func (s *ServerChan3Sender) GetName() string {
	return "Server酱³"
}

// GetConfiguration 返回配置结构体指针
func (s *ServerChan3Sender) GetConfiguration() factory.Configuration {
	return &s.Addition
}

// Init 初始化（当前无需处理）
func (s *ServerChan3Sender) Init() error { return nil }

// Destroy 清理（当前无需处理）
func (s *ServerChan3Sender) Destroy() error { return nil }

// SendTextMessage 发送文本消息（携带标题）。
// 简化为固定 JSON POST：内部仅组装 {"title": ..., "desp": ...}，不允许用户自定义请求体或内容类型。
func (s *ServerChan3Sender) SendTextMessage(message, title string) error {
	apiURL := strings.TrimSpace(s.Addition.APIURL)
	if apiURL == "" {
		return fmt.Errorf("未配置 Server酱³ 接口地址(api_url)")
	}

	// 简化校验：若标题与正文均为空则拒绝发送
	finalTitle := strings.TrimSpace(title)
	finalMessage := strings.TrimSpace(message)
	if finalTitle == "" && finalMessage == "" {
		return fmt.Errorf("serverchan3: 标题与正文均为空")
	}

	// 固定 JSON 载荷，包含 title、desp，并支持可选 tags（通过 | 分割）
	payload := map[string]string{
		"title": finalTitle,
		"desp":  finalMessage,
	}
	if tagsStr := strings.TrimSpace(s.Addition.Tags); tagsStr != "" {
		parts := strings.Split(tagsStr, "|")
		var cleaned []string
		for _, p := range parts {
			t := strings.TrimSpace(p)
			if t != "" {
				cleaned = append(cleaned, t)
			}
		}
		if len(cleaned) > 0 {
			// Server酱³ 校验提示需要字符串，这里按 | 拼接为字符串
			payload["tags"] = strings.Join(cleaned, "|")
		}
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("组装 JSON 失败: %v", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return fmt.Errorf("创建请求失败: %v", err)
	}
	// 固定设置 Content-Type
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("发送请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 读取响应体用于错误信息打印
		b, _ := io.ReadAll(resp.Body)
		msg := strings.TrimSpace(string(b))
		if msg == "" {
			return fmt.Errorf("接口返回非 2xx 状态码: %d", resp.StatusCode)
		}
		return fmt.Errorf("接口返回非 2xx 状态码: %d，响应: %s", resp.StatusCode, msg)
	}

	return nil
}

// 之前的模板、表单解析与手动转义工具不再需要，简化实现

// 确保实现 IMessageSender 接口
var _ factory.IMessageSender = (*ServerChan3Sender)(nil)
