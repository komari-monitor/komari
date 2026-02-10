package gotify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/komari-monitor/komari/utils/messageSender/factory"
)

type GotifySender struct {
	Addition
}

func (g *GotifySender) GetName() string {
	return "gotify"
}

func (g *GotifySender) GetConfiguration() factory.Configuration {
	return &g.Addition
}

func (g *GotifySender) Init() error {
	if g.Addition.ServerURL != "" {
		if _, err := url.Parse(g.Addition.ServerURL); err != nil {
			return fmt.Errorf("invalid server URL: %v", err)
		}
	}
	return nil
}

func (g *GotifySender) Destroy() error {
	return nil
}

func (g *GotifySender) SendTextMessage(message, title string) error {
	if g.Addition.ServerURL == "" {
		return fmt.Errorf("server URL is required")
	}
	if g.Addition.Token == "" {
		return fmt.Errorf("token is required")
	}
	if message == "" {
		return fmt.Errorf("message is empty")
	}

	priority := 5
	if g.Addition.Priority != "" {
		p, err := strconv.Atoi(g.Addition.Priority)
		if err == nil {
			priority = p
		}
	}

	payload := map[string]interface{}{
		"message":  message,
		"priority": priority,
	}

	if title != "" {
		payload["title"] = title
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %v", err)
	}

	serverURL := strings.TrimRight(g.Addition.ServerURL, "/")
	requestURL := fmt.Sprintf("%s/message?token=%s", serverURL, g.Addition.Token)

	resp, err := http.Post(requestURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("gotify API returned non-OK status: %d", resp.StatusCode)
	}

	return nil
}

var _ factory.IMessageSender = (*GotifySender)(nil)
