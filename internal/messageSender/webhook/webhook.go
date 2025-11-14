package webhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/komari-monitor/komari/internal/messageSender/factory"
)

type WebhookSender struct {
	Addition
}

func (w *WebhookSender) GetName() string {
	return "webhook"
}

func (w *WebhookSender) GetConfiguration() factory.Configuration {
	return &w.Addition
}

func (w *WebhookSender) Init() error {
	return nil
}

func (w *WebhookSender) Destroy() error {
	return nil
}

func (w *WebhookSender) SendTextMessage(message, title string) error {
	if w.Addition.URL == "" {
		return fmt.Errorf("webhook URL is not configured")
	}

	method := strings.ToUpper(w.Addition.Method)
	if method == "" {
		method = "GET" // 默认使用 GET
	}

	client := &http.Client{
		Timeout: 30 * time.Second, // 固定30秒超时
	}

	var req *http.Request
	var err error

	switch method {
	case "POST":
		req, err = w.createPOSTRequest(message, title)
	case "GET":
		req, err = w.createGETRequest(message, title)
	default:
		return fmt.Errorf("unsupported HTTP method: %s", method)
	}

	if err != nil {
		return fmt.Errorf("failed to create request: %v", err)
	}

	// 解析并设置自定义头部
	if w.Addition.Headers != "" {
		var headers map[string]string
		if err := json.Unmarshal([]byte(w.Addition.Headers), &headers); err == nil {
			for key, value := range headers {
				req.Header.Set(key, value)
			}
		}
	}

	// 设置基本认证
	if w.Addition.Username != "" && w.Addition.Password != "" {
		req.SetBasicAuth(w.Addition.Username, w.Addition.Password)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send webhook request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("webhook request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

func (w *WebhookSender) createPOSTRequest(message, title string) (*http.Request, error) {
	contentType := w.Addition.ContentType
	if contentType == "" {
		contentType = "application/json"
	}

	// 用户自定义模板，按 Content-Type 决定如何替换占位符
	var body string
	if isJSONContentType(contentType) {
		body = w.replaceTemplateJSON(w.Addition.Body, message, title)
	} else {
		body = w.replaceTemplate(w.Addition.Body, message, title)
	}

	req, err := http.NewRequest("POST", w.Addition.URL, bytes.NewBufferString(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", contentType)
	return req, nil
}

func (w *WebhookSender) createGETRequest(message, title string) (*http.Request, error) {
	URL := w.replaceTemplate(w.Addition.URL, message, title)
	u, err := url.Parse(URL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %v", err)
	}

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, err
	}

	return req, nil
}

// replaceTemplate 替换模板中的 {{message}} 和 {{title}} 占位符（不做转义）
func (w *WebhookSender) replaceTemplate(template, message, title string) string {
	result := template
	result = strings.ReplaceAll(result, "{{message}}", message)
	result = strings.ReplaceAll(result, "{{title}}", title)
	return result
}

// replaceTemplateJSON 在 JSON 场景下替换占位符，使用 \uXXXX 形式进行转义（尤其换行/控制字符）
func (w *WebhookSender) replaceTemplateJSON(template, message, title string) string {
	result := template
	result = strings.ReplaceAll(result, "{{message}}", jsonUnicodeEscapeString(message))
	result = strings.ReplaceAll(result, "{{title}}", jsonUnicodeEscapeString(title))
	return result
}

// isJSONContentType 判断是否为 JSON 内容类型
func isJSONContentType(ct string) bool {
	return strings.Contains(strings.ToLower(ct), "application/json")
}

// jsonUnicodeEscapeString 将字符串按 JSON 规则进行转义，并尽量使用 \uXXXX 形式（尤其控制字符、引号和反斜杠）
// 注意：该结果适合放入 JSON 字符串字面量的引号内部。
func jsonUnicodeEscapeString(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString("\\u0022")
		case '\\':
			b.WriteString("\\u005C")
		case '\b':
			b.WriteString("\\u0008")
		case '\f':
			b.WriteString("\\u000C")
		case '\n':
			b.WriteString("\\u000A")
		case '\r':
			b.WriteString("\\u000D")
		case '\t':
			b.WriteString("\\u0009")
		default:
			if r < 0x20 {
				// 其他控制字符
				b.WriteString(fmt.Sprintf("\\u%04X", r))
			} else {
				// 其余字符按原样输出（UTF-8），JSON 允许非 ASCII 字符
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}
