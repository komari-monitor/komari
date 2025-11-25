package messageSender

import (
	"testing"

	"github.com/komari-monitor/komari/internal/messageSender/factory"
)

func Test(t *testing.T) {
	senders := factory.GetAllMessageSenders()
	if len(senders) == 0 {
		t.Error("No message senders found")
		return
	}
	cfg := factory.GetSenderConfigs()
	if len(cfg) == 0 {
		t.Error("No sender configs found")
		return
	}
	LoadProvider("email", `{"host":"smtp.example.com","port":587,"username":"user","password":"pass"}`)
	cp := CurrentProvider
	if cp() == nil {
		t.Error("Current provider is nil")
		return
	}
}
