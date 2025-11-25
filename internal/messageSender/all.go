package messageSender

import (
	_ "github.com/komari-monitor/komari/internal/messageSender/bark"
	_ "github.com/komari-monitor/komari/internal/messageSender/email"
	_ "github.com/komari-monitor/komari/internal/messageSender/empty"
	_ "github.com/komari-monitor/komari/internal/messageSender/javascript"
	_ "github.com/komari-monitor/komari/internal/messageSender/serverchan3"
	_ "github.com/komari-monitor/komari/internal/messageSender/serverchanturbo"
	_ "github.com/komari-monitor/komari/internal/messageSender/telegram"
	_ "github.com/komari-monitor/komari/internal/messageSender/webhook"
)

func All() {
}
