package webhook

import (
	"github.com/komari-monitor/komari/internal/messageSender/factory"
)

type Addition struct {
	URL         string `json:"url" required:"true"`
	Method      string `json:"method" default:"GET" type:"option" options:"POST,GET"`
	ContentType string `json:"content_type" default:"application/json"`
	Headers     string `json:"headers" help:"HTTP headers in JSON format"`
	Body        string `json:"body" default:"{\"message\":\"{{message}}\",\"title\":\"{{title}}\"}"` // 默认使用message和title字段
	Username    string `json:"username"`
	Password    string `json:"password"`
}

func init() {
	factory.RegisterMessageSender(func() factory.IMessageSender {
		return &WebhookSender{}
	})
}
