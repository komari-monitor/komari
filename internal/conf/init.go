package conf

import (
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// InstallGuideFS 用于存储安装引导页面的静态文件系统，由外部包注入以避免循环引用
var InstallGuideFS fs.FS

func installGuide() {
	// 未来支持
	/*
		listen := flags.Listen
		if envListen := os.Getenv("LISTEN"); envListen != "" {
			listen = envListen
		}
		if listen == "" {
			listen = "0.0.0.0:25774"
		}

		gin.SetMode(gin.ReleaseMode)
		r := gin.New()
		r.Use(gin.Recovery())

		if InstallGuideFS == nil {
			panic("InstallGuideFS is not set, please inject it before starting the server")
		}

		// 注册 API handlers
		api := r.Group("/api/init")
		api.POST("/set_https", handleSetHTTPS)

		// 处理所有请求
		r.NoRoute(func(c *gin.Context) {
			path := c.Request.URL.Path

			cleanPath := strings.TrimPrefix(path, "/")
			file, err := InstallGuideFS.Open(cleanPath)
			if err == nil {
				defer file.Close()
				data, err := io.ReadAll(file)
				if err == nil {
					c.Data(http.StatusOK, getContentType(path), data)
					return
				}
			}

			// 文件不存在，如果是 GET 请求返回 index.html（SPA 路由）
			if c.Request.Method == "GET" && strings.HasPrefix(path, "/init") {
				indexFile, err := InstallGuideFS.Open("index.html")
				if err == nil {
					defer indexFile.Close()
					data, err := io.ReadAll(indexFile)
					if err == nil {
						c.Header("Content-Type", "text/html")
						c.Data(http.StatusOK, "text/html", data)
						return
					}
				}
			}

			// 其他情况返回 404
			c.Redirect(302, "/init")
		})

		// 启动服务器（阻塞）
		slog.Info("Starting install guide server...", slog.String("listen", listen))
		if err := r.Run(listen); err != nil {
			panic("failed to start install guide server: " + err.Error())
		}
	*/

}

// getContentType 根据路径获取Content-Type
func getContentType(path string) string {
	if strings.HasSuffix(path, ".html") {
		return "text/html"
	}
	if strings.HasSuffix(path, ".css") {
		return "text/css"
	}
	if strings.HasSuffix(path, ".js") {
		return "application/javascript"
	}
	if strings.HasSuffix(path, ".json") {
		return "application/json"
	}
	if strings.HasSuffix(path, ".png") {
		return "image/png"
	}
	if strings.HasSuffix(path, ".jpg") || strings.HasSuffix(path, ".jpeg") {
		return "image/jpeg"
	}
	if strings.HasSuffix(path, ".gif") {
		return "image/gif"
	}
	if strings.HasSuffix(path, ".svg") {
		return "image/svg+xml"
	}
	if strings.HasSuffix(path, ".ico") {
		return "image/x-icon"
	}
	if strings.HasSuffix(path, ".woff") {
		return "font/woff"
	}
	if strings.HasSuffix(path, ".woff2") {
		return "font/woff2"
	}
	if strings.HasSuffix(path, ".ttf") {
		return "font/ttf"
	}
	return "application/octet-stream"
}

// handleSetHTTPS 处理 HTTPS 配置
func handleSetHTTPS(c *gin.Context) {
	var req struct {
		Enabled bool   `json:"enabled"`
		CertURL string `json:"cert_url,omitempty"`
		KeyURL  string `json:"key_url,omitempty"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// TODO: 实现 HTTPS 配置逻辑
	slog.Info("Setting HTTPS configuration", slog.Bool("enabled", req.Enabled))

	c.JSON(http.StatusOK, gin.H{
		"message": "HTTPS configuration updated",
		"enabled": req.Enabled,
	})
}
