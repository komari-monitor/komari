package cloudflared

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
)

func downloadCloudflared(downloadURL, filePath string) error {
	log.Println("downloading cloudflared...")
	resp, err := http.Get(downloadURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	out, err := os.Create(filePath)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, resp.Body)
	closeErr := out.Close() // 先关闭文件
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if runtime.GOOS != "windows" {
		err = os.Chmod(filePath, 0755)
		if err != nil {
			return err
		}
	}
	return nil
}

var cloudflaredCmd *exec.Cmd

func RunCloudflared() error {
	var (
		downloadURL string
		fileName    = "cloudflared"
	)
	if _, err := os.Stat("data"); os.IsNotExist(err) {
		os.Mkdir("data", 0755)
	}

	switch runtime.GOOS {
	case "windows":
		fileName = "cloudflared.exe"
		switch runtime.GOARCH {
		case "amd64":
			downloadURL = "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-windows-amd64.exe"
		case "386":
			downloadURL = "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-windows-386.exe"
		default:
			return fmt.Errorf("unsupported architecture: %s", runtime.GOARCH)
		}
	case "linux":
		switch runtime.GOARCH {
		case "amd64":
			downloadURL = "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64"
		case "386":
			downloadURL = "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-386"
		case "arm":
			downloadURL = "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-arm"
		case "arm64":
			downloadURL = "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-arm64"
		default:
			return fmt.Errorf("unsupported architecture: %s", runtime.GOARCH)
		}
	case "darwin":
		switch runtime.GOARCH {
		case "amd64":
			downloadURL = "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-darwin-amd64.tgz"
		case "arm64":
			downloadURL = "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-darwin-arm64.tgz"
		default:
			return fmt.Errorf("unsupported architecture: %s", runtime.GOARCH)
		}
	default:
		return fmt.Errorf("unsupported os: %s", runtime.GOOS)
	}

	filePath := "data/" + fileName
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		err := downloadCloudflared(downloadURL, filePath)
		if err != nil {
			return err
		}
	}

	args := []string{"tunnel", "--no-autoupdate", "run", "--token"}
	token := os.Getenv("KOMARI_CLOUDFLARED_TOKEN")
	args = append(args, token)

	cmd := exec.Command(filePath, args...)
	cloudflaredCmd = cmd
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
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			log.Printf("[cloudflared] %s", scanner.Text())
		}
	}()
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			log.Printf("[cloudflared] %s", scanner.Text())
		}
	}()
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("cloudflared exited with error: %v", err)
		} else {
			log.Println("cloudflared exited successfully")
		}
		os.Exit(1)
	}()
	log.Println("cloudflared started")
	return nil
}

func Kill() {
	if cloudflaredCmd != nil && cloudflaredCmd.Process != nil {
		err := cloudflaredCmd.Process.Kill()
		if err != nil {
			log.Printf("failed to kill cloudflared: %v", err)
		} else {
			log.Println("cloudflared killed")
		}
		cloudflaredCmd = nil
	}
}
