package cloudflared

import (
	"fmt"
	"os"
	"strings"

	"github.com/gookit/event"
	"github.com/komari-monitor/komari/internal/eventType"
)

func init() {
	event.On(eventType.ServerInitializeStart, event.ListenerFunc(func(e event.Event) error {
		if strings.ToLower(strings.ToLower(os.Getenv("KOMARI_ENABLE_CLOUDFLARED"))) == "true" {
			err := RunCloudflared()
			if err != nil {
				// Error in ServerInitializeStart will cause the process to exit
				return fmt.Errorf("failed to run cloudflared: %v", err)
			}
		}
		return nil
	}))

	event.On(eventType.ProcessExit, event.ListenerFunc(func(e event.Event) error {
		Kill()
		return nil
	}))
}
