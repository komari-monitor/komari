package messageSender

import (
	_ "github.com/komari-monitor/komari/utils/messageSender/bark"
	_ "github.com/komari-monitor/komari/utils/messageSender/email"
	_ "github.com/komari-monitor/komari/utils/messageSender/empty"
	_ "github.com/komari-monitor/komari/utils/messageSender/serverchan3"
	_ "github.com/komari-monitor/komari/utils/messageSender/serverchanturbo"
	_ "github.com/komari-monitor/komari/utils/messageSender/javascript"
	_ "github.com/komari-monitor/komari/utils/messageSender/telegram"
	_ "github.com/komari-monitor/komari/utils/messageSender/webhook"
)

func All() {
}
