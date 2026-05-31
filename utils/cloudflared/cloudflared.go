package cloudflared

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/komari-monitor/komari/pkg/config"
	"github.com/komari-monitor/komari/utils/secureconfig"
)

type RuntimeStatus struct {
	Installed       bool     `json:"installed"`
	Running         bool     `json:"running"`
	Message         string   `json:"message"`
	ErrorMessage    string   `json:"errorMessage"`
	Logs            []string `json:"logs"`
	PID             int      `json:"pid,omitempty"`
	BinaryPath      string   `json:"binaryPath,omitempty"`
	TokenStored     bool     `json:"tokenStored"`
	EnvTokenPresent bool     `json:"envTokenPresent"`
}

type manager struct {
	mu            sync.RWMutex
	cmd           *exec.Cmd
	done          chan struct{}
	running       bool
	stopRequested bool
	message       string
	errorMessage  string
	logs          []string
	maxLogLines   int
}

var defaultManager = &manager{
	maxLogLines: 80,
	message:     "cloudflared is not running",
}

func Status() RuntimeStatus {
	return defaultManager.status()
}

func Start(token string) error {
	return defaultManager.start(token)
}

func Stop() error {
	return defaultManager.stop()
}

func Shutdown() {
	if err := defaultManager.stop(); err != nil {
		log.Printf("failed to stop cloudflared: %v", err)
	}
}

func SaveToken(token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return config.Set(config.CloudflareTunnelTokenKey, "")
	}

	encrypted, err := secureconfig.EncryptString(token)
	if err != nil {
		return err
	}

	return config.Set(config.CloudflareTunnelTokenKey, encrypted)
}

func RemoveToken() error {
	if defaultManager.status().Running {
		return errors.New("stop cloudflared before removing the token")
	}
	return config.Set(config.CloudflareTunnelTokenKey, "")
}

func LoadToken() (token string, err error) {
	if envToken := strings.TrimSpace(os.Getenv("KOMARI_CLOUDFLARED_TOKEN")); envToken != "" {
		return envToken, nil
	}

	return loadStoredToken()
}

func AutoStart(envToken string) error {
	token := strings.TrimSpace(envToken)
	if token == "" {
		var err error
		token, err = LoadToken()
		if err != nil {
			return err
		}
	} else {
		if err := SaveToken(token); err != nil {
			return err
		}
		log.Println("[cloudflared] using token from KOMARI_CLOUDFLARED_TOKEN")
	}

	if token == "" {
		return nil
	}

	if err := Start(token); err != nil {
		defaultManager.setError(err.Error())
		return err
	}

	return nil
}

func (m *manager) start(token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return errors.New("cloudflared is already running")
	}

	if strings.TrimSpace(token) == "" {
		var err error
		token, err = LoadToken()
		if err != nil {
			return fmt.Errorf("failed to load Cloudflare Tunnel token: %w", err)
		}
	}
	if strings.TrimSpace(token) == "" {
		return errors.New("cloudflare tunnel token is not configured")
	}

	binaryPath, installed := resolveBinaryPath()
	if !installed {
		return errors.New("cloudflared is not installed; install it manually or use the Docker image with built-in cloudflared")
	}

	cmd := exec.Command(binaryPath, "tunnel", "--no-autoupdate", "run")
	cmd.Env = append(os.Environ(), "TUNNEL_TOKEN="+token)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	m.cmd = cmd
	m.done = make(chan struct{})
	m.running = true
	m.stopRequested = false
	m.errorMessage = ""
	m.message = "cloudflared is running"
	m.appendLogLocked(fmt.Sprintf("started cloudflared (pid=%d)", cmd.Process.Pid))

	go m.scanPipe(stdout, false)
	go m.scanPipe(stderr, true)
	go m.waitProcess(cmd, m.done)

	return nil
}

func (m *manager) stop() error {
	m.mu.RLock()
	cmd := m.cmd
	done := m.done
	running := m.running
	m.mu.RUnlock()

	if !running || cmd == nil || cmd.Process == nil {
		return nil
	}

	m.mu.Lock()
	m.stopRequested = true
	m.message = "stopping cloudflared..."
	m.mu.Unlock()

	var signalErr error
	if runtime.GOOS == "windows" {
		signalErr = cmd.Process.Kill()
	} else {
		signalErr = cmd.Process.Signal(syscall.SIGTERM)
	}
	if signalErr != nil {
		return signalErr
	}

	if done != nil {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
			}
		}
	}

	return nil
}

func (m *manager) waitProcess(cmd *exec.Cmd, done chan struct{}) {
	err := cmd.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()

	if err != nil {
		if m.stopRequested {
			m.message = "cloudflared stopped"
			m.errorMessage = ""
			m.appendLogLocked("cloudflared stopped")
		} else {
			m.message = "cloudflared exited unexpectedly"
			m.errorMessage = err.Error()
			m.appendLogLocked("cloudflared exited unexpectedly: " + err.Error())
		}
	} else {
		m.message = "cloudflared stopped"
		if !m.stopRequested {
			m.errorMessage = ""
		}
		m.appendLogLocked("cloudflared stopped")
	}

	m.cmd = nil
	m.running = false
	m.stopRequested = false
	close(done)
}

func (m *manager) scanPipe(reader io.ReadCloser, isErr bool) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		m.mu.Lock()
		if isErr || looksLikeError(line) {
			m.errorMessage = line
		}
		m.message = line
		m.appendLogLocked(line)
		m.mu.Unlock()
	}
}

func (m *manager) status() RuntimeStatus {
	binaryPath, installed := resolveBinaryPath()

	m.mu.RLock()
	defer m.mu.RUnlock()

	status := RuntimeStatus{
		Installed:       installed,
		Running:         m.running,
		Message:         m.message,
		ErrorMessage:    m.errorMessage,
		Logs:            append([]string{}, m.logs...),
		BinaryPath:      binaryPath,
		EnvTokenPresent: strings.TrimSpace(os.Getenv("KOMARI_CLOUDFLARED_TOKEN")) != "",
	}
	if m.cmd != nil && m.cmd.Process != nil {
		status.PID = m.cmd.Process.Pid
	}

	token, err := loadStoredToken()
	if err == nil && strings.TrimSpace(token) != "" {
		status.TokenStored = true
	}

	return status
}

func (m *manager) setError(message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errorMessage = message
	m.message = message
	m.appendLogLocked(message)
}

func (m *manager) appendLogLocked(line string) {
	m.logs = append(m.logs, line)
	if len(m.logs) > m.maxLogLines {
		m.logs = m.logs[len(m.logs)-m.maxLogLines:]
	}
}

func loadStoredToken() (token string, err error) {
	defer func() {
		if recover() != nil {
			token = ""
			err = nil
		}
	}()

	raw, err := config.GetAs[string](config.CloudflareTunnelTokenKey, "")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}

	token, err = secureconfig.DecryptString(raw)
	if err == nil {
		return token, nil
	}

	return raw, nil
}

func resolveBinaryPath() (string, bool) {
	candidates := []string{}
	if envPath := strings.TrimSpace(os.Getenv("KOMARI_CLOUDFLARED_BIN")); envPath != "" {
		candidates = append(candidates, envPath)
	}

	candidates = append(candidates, "cloudflared")
	if runtime.GOOS == "windows" {
		candidates = append(candidates,
			filepath.Join("data", "cloudflared.exe"),
			filepath.Join("data", "bin", "cloudflared.exe"),
		)
	} else {
		candidates = append(candidates,
			filepath.Join("data", "cloudflared"),
			filepath.Join("data", "bin", "cloudflared"),
			"/usr/local/bin/cloudflared",
			"/usr/bin/cloudflared",
		)
	}

	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}

		if path, err := exec.LookPath(candidate); err == nil {
			return path, true
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			if abs, absErr := filepath.Abs(candidate); absErr == nil {
				return abs, true
			}
			return candidate, true
		}
	}

	return "", false
}

func looksLikeError(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "error") ||
		strings.Contains(lower, "failed") ||
		strings.Contains(lower, "invalid") ||
		strings.Contains(lower, "unauthorized")
}
