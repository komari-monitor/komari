package gotify

import (
	"github.com/komari-monitor/komari/utils/messageSender/factory"
)

type Addition struct {
	ServerURL string `json:"server_url" required:"true" help:"Gotify server URL, e.g. https://push.example.com"`
	Token     string `json:"token" required:"true" help:"Application token"`
	Priority  string `json:"priority" default:"5" help:"Message priority (0-10). Default is 5."`
}

func init() {
	factory.RegisterMessageSender(func() factory.IMessageSender {
		return &GotifySender{}
	})
}
