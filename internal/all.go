package internal

import (
	// Import all internal packages to ensure their init() functions are executed
	_ "github.com/komari-monitor/komari/internal/api_rpc"
	_ "github.com/komari-monitor/komari/internal/api_v1"
	_ "github.com/komari-monitor/komari/internal/client"
	_ "github.com/komari-monitor/komari/internal/cloudflared"
	_ "github.com/komari-monitor/komari/internal/common"
	_ "github.com/komari-monitor/komari/internal/conf"
	_ "github.com/komari-monitor/komari/internal/database"
	_ "github.com/komari-monitor/komari/internal/eventType"
	_ "github.com/komari-monitor/komari/internal/geoip"
	_ "github.com/komari-monitor/komari/internal/jsruntime"
	_ "github.com/komari-monitor/komari/internal/log"
	_ "github.com/komari-monitor/komari/internal/messageSender"
	_ "github.com/komari-monitor/komari/internal/mjpeg"
	_ "github.com/komari-monitor/komari/internal/nezha"
	_ "github.com/komari-monitor/komari/internal/notifier"
	_ "github.com/komari-monitor/komari/internal/oauth"
	_ "github.com/komari-monitor/komari/internal/patch"
	_ "github.com/komari-monitor/komari/internal/pingSchedule"
	_ "github.com/komari-monitor/komari/internal/plugin"
	_ "github.com/komari-monitor/komari/internal/renewal"
	_ "github.com/komari-monitor/komari/internal/restore"
	_ "github.com/komari-monitor/komari/internal/ws"
)

func All() {}
