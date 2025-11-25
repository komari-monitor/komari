package main

import (
	"log"
	"log/slog"

	"github.com/komari-monitor/komari/cmd"
	"github.com/komari-monitor/komari/internal/conf"
	logutil "github.com/komari-monitor/komari/internal/log"
)

func main() {
	if conf.Version == conf.Version_Development {
		logutil.SetupGlobalLogger(slog.LevelDebug)
	} else {
		logutil.SetupGlobalLogger(slog.LevelInfo)
	}

	log.Printf("Komari Monitor %s (hash: %s)", conf.Version, conf.CommitHash)

	cmd.Execute()
}
