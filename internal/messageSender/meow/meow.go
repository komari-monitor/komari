package meow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/komari-monitor/komari/internal/messageSender/factory"
)

type MeowSender struct {
	Addition
}

// GetName 返回消息发送器的名称
func (m *MeowSender) GetName() string {
	return "MeoW"
}

// GetConfiguration 返回配置对象的指针
func (m *MeowSender) GetConfiguration() factory.Configuration {
	return &m.Addition
}

// Init 初始化发送器，检查必要的配置
func (m *MeowSender) Init() error {
	if m.Addition.Nickname == "" {
		return fmt.Errorf("昵称不能为空")
	}
	return nil
}

// Destroy 销毁发送器，清理资源
func (m *MeowSender) Destroy() error {
	return nil
}

// SendTextMessage 发送文本消息
func (m *MeowSender) SendTextMessage(message, title string) error {
	if m.Addition.Nickname == "" {
		return fmt.Errorf("昵称不能为空")
	}

	// 处理 BaseURL，如果为空则使用默认值，并去除末尾的斜杠
	baseURL := strings.TrimRight(m.Addition.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.chuckfang.com"
	}

	// 构建请求 URL: POST /{nickname}
	reqURL := fmt.Sprintf("%s/%s", baseURL, m.Addition.Nickname)

	finalMessage := message

	// 如果配置了 msgType=html，则添加到查询参数中
	if m.Addition.MsgType == "html" {
		params := url.Values{}
		params.Set("msgType", "html")
		if m.Addition.HtmlHeight > 0 {
			params.Set("htmlHeight", fmt.Sprintf("%d", m.Addition.HtmlHeight))
		}
		// 将查询参数附加到 URL
		if strings.Contains(reqURL, "?") {
			reqURL += "&" + params.Encode()
		} else {
			reqURL += "?" + params.Encode()
		}

		// 如果是 HTML 模式，且消息中不包含常见的 HTML 标签，尝试自动将换行符转换为 <br>
		if !strings.Contains(message, "<") && !strings.Contains(message, ">") {
			finalMessage = strings.ReplaceAll(message, "\n", "<br>")
		}
	}

	// 构建请求体
	payload := map[string]string{
		"msg": finalMessage,
	}
	if title != "" {
		payload["title"] = title
	}
	if m.Addition.Url != "" {
		payload["url"] = m.Addition.Url
	}

	// 序列化 JSON
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("消息序列化失败: %v", err)
	}

	// 发送 POST 请求
	resp, err := http.Post(reqURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("发送请求失败: %v", err)
	}
	defer resp.Body.Close()

	// 检查响应状态码
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("MeoW API 返回错误状态码: %d", resp.StatusCode)
	}

	return nil
}
