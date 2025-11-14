package main

import (
	"log"
	"log/slog"

	"github.com/komari-monitor/komari/cmd"
	logutil "github.com/komari-monitor/komari/internal/log"
	"github.com/komari-monitor/komari/internal/version"
)

func main() {
	if version.VersionHash == "unknown" {
		logutil.SetupGlobalLogger(slog.LevelDebug)
	} else {
		logutil.SetupGlobalLogger(slog.LevelInfo)
	}

	log.Printf("Komari Monitor %s (hash: %s)", version.CurrentVersion, version.VersionHash)

	cmd.Execute()
}
