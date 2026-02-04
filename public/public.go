package public

import (
	"embed"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/config"
)

//go:embed dist
var PublicFS embed.FS

var DistFS fs.FS
var RawIndexFile string

var IndexFile string

func initIndex() {
	err := os.MkdirAll("./data/theme", 0755)
	if err != nil {
		log.Println("Failed to create theme directory:", err)
		return
	}
	dist, err := fs.Sub(PublicFS, "dist")
	if err != nil {
		log.Println("Failed to create dist subdirectory:", err)
	}
	DistFS = dist

	indexFile, err := dist.Open("index.html")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			log.Println("index.html not exist, you may forget to put dist of frontend to public/dist")
		}
		log.Println("Failed to open index.html:", err)
	}
	defer func() {
		_ = indexFile.Close()
	}()
	index, err := io.ReadAll(indexFile)
	if err != nil {
		log.Println("Failed to read index.html:", err)
	}
	RawIndexFile = string(index)
}
func UpdateIndex() {
	IndexFile = applyCustomizations(RawIndexFile)
}

// applyCustomizations 应用自定义内容到HTML字符串
func applyCustomizations(htmlContent string) string {
	var titleReplacement string
	if Sitename, err := config.GetAs[string](config.SitenameKey, "Komari Monitor"); err != nil || Sitename == "Komari Monitor" {
		titleReplacement = "<title>Komari Monitor</title>"
	} else {
		titleReplacement = fmt.Sprintf("<title>%s - Komari Monitor</title>", html.EscapeString(Sitename))
	}

	cfg, err := config.GetMany(map[string]any{
		config.DescriptionKey: "A simple server monitor tool.",
		config.CustomHeadKey:  "",
		config.CustomBodyKey:  "",
	})
	if err != nil {
		return htmlContent
	}
	replaceMap := map[string]string{
		"<title>Komari Monitor</title>": titleReplacement,
		"A simple server monitor tool.": cfg[config.DescriptionKey].(string),
		"</head>":                       cfg[config.CustomHeadKey].(string) + "</head>",
		"</body>":                       cfg[config.CustomBodyKey].(string) + "</body>",
	}

	updated := htmlContent
	for k, v := range replaceMap {
		updated = strings.Replace(updated, k, v, -1)
	}
	return updated
}

func Static(r *gin.RouterGroup, noRoute func(handlers ...gin.HandlerFunc)) {
	initIndex()

	// Serve favicon.ico: use local file if exists, fallback to embedded
	r.GET("/favicon.ico", func(c *gin.Context) {
		if _, err := os.Stat("data/favicon.ico"); err == nil {
			c.File("data/favicon.ico")
			return
		}
		f, err := DistFS.Open("favicon.ico")
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		defer f.Close()
		data, err := io.ReadAll(f)
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Data(http.StatusOK, "image/x-icon", data)
	})
	// r.GET("/manifest.json", func(c *gin.Context) {
	// 	cfg, err := config.Get()
	// 	if cfg.Theme == "default" || cfg.Theme == "" || err != nil {
	// 		// 使用默认主题的manifest.json
	// 		f, err := DistFS.Open("manifest.json")
	// 		if err != nil {
	// 			c.Status(http.StatusNotFound)
	// 			return
	// 		}
	// 		defer f.Close()
	// 		data, err := io.ReadAll(f)
	// 		if err != nil {
	// 			c.Status(http.StatusInternalServerError)
	// 			return
	// 		}
	// 		c.Data(http.StatusOK, "application/json", data)
	// 	} else {
	// 		// 使用自定义主题的manifest.json
	// 		themePath := filepath.Join("./data/theme", cfg.Theme, "dist", "manifest.json")
	// 		if _, err := os.Stat(themePath); os.IsNotExist(err) {
	// 			c.JSON(http.StatusNotFound, gin.H{"error": "Manifest not found"})
	// 		} else {
	// 			c.File(themePath)
	// 		}
	// 	}
	// })

	// Serve theme files from data/theme directory (for theme previews and static assets)
	r.Static("/themes", "./data/theme")

	// 使用传入的noRoute函数来处理未匹配的路由
	noRoute(func(c *gin.Context) {
		path := c.Request.URL.Path

		// 对于admin页面，直接使用embedded文件
		if strings.HasPrefix(path, "/admin") || strings.HasPrefix(path, "/terminal") {
			serveFromEmbedded(c, path)
			return
		}

		// 获取当前主题配置
		Theme, err := config.GetAs[string](config.ThemeKey, "default")
		if err != nil || Theme == "default" || Theme == "" {
			// 使用默认主题（embedded文件）
			serveFromEmbedded(c, path)
			return
		}

		// 使用自定义主题
		serveFromTheme(c, path, Theme)
	})
}

// serveFromEmbedded 从嵌入的文件系统服务文件
func serveFromEmbedded(c *gin.Context, path string) {
	// 处理静态资源文件夹
	// folders := []string{"assets", "images", "streamer", "static"}
	// for _, folder := range folders {
	// 	if strings.HasPrefix(path, "/"+folder+"/") {
	// 		c.Header("Cache-Control", "public, max-age=15552000")
	// 		sub, err := fs.Sub(DistFS, folder)
	// 		if err == nil {
	// 			relativePath := strings.TrimPrefix(path, "/"+folder+"/")
	// 			file, err := sub.Open(relativePath)
	// 			if err == nil {
	// 				defer file.Close()
	// 				data, err := io.ReadAll(file)
	// 				if err == nil {
	// 					// 设置正确的Content-Type
	// 					contentType := getContentType(path)
	// 					c.Header("Content-Type", contentType)
	// 					c.Data(http.StatusOK, contentType, data)
	// 					return
	// 				}
	// 			}
	// 		}
	// 	}
	// }
	// 如果文件存在，直接返回文件内容
	cleanPath := strings.TrimPrefix(path, "/")
	file, err := DistFS.Open(cleanPath)
	if err == nil {
		defer file.Close()
		data, err := io.ReadAll(file)
		if err == nil {
			contentType := getContentType(path)
			c.Header("Content-Type", contentType)
			// 静态资源设置缓存
			if strings.Contains(path, "/assets/") || strings.Contains(path, "/static/") {
				c.Header("Cache-Control", "public, max-age=15552000")
			}
			c.Data(http.StatusOK, contentType, data)
			return
		}
	}

	// 处理HTML页面
	if c.Request.Method != "GET" && c.Request.Method != "POST" {
		c.Status(405)
		return
	}

	c.Header("Content-Type", "text/html")
	c.Status(200)

	if strings.HasPrefix(path, "/admin") || strings.HasPrefix(path, "/terminal") {
		c.Writer.WriteString(RawIndexFile)
	} else {
		c.Writer.WriteString(IndexFile)
	}

	c.Writer.Flush()
	c.Writer.WriteHeaderNow()
}

// serveFromTheme 从主题目录服务文件
func serveFromTheme(c *gin.Context, path string, themeName string) {
	themeDir := filepath.Join("./data/theme", themeName, "dist")

	// 构建完整的文件路径
	var filePath string
	if path == "/" || path == "" {
		filePath = filepath.Join(themeDir, "index.html")
	} else {
		filePath = filepath.Join(themeDir, strings.TrimPrefix(path, "/"))
	}

	// 检查文件是否存在
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		// 如果文件不存在，尝试serve index.html（用于SPA路由）
		if !strings.Contains(path, ".") {
			indexPath := filepath.Join(themeDir, "index.html")
			if _, err := os.Stat(indexPath); err == nil {
				serveThemeIndexWithCustomizations(c, indexPath)
				return
			}
		}
		// 回退到默认主题
		serveFromEmbedded(c, path)
		return
	}

	// 设置缓存头
	if strings.Contains(path, "/assets/") || strings.Contains(path, "/static/") {
		c.Header("Cache-Control", "public, max-age=15552000")
	}

	// 如果是index.html文件，需要处理自定义内容
	if strings.HasSuffix(filePath, "index.html") {
		serveThemeIndexWithCustomizations(c, filePath)
		return
	}

	c.File(filePath)
}

// serveThemeIndexWithCustomizations 服务主题的index.html并应用自定义内容
func serveThemeIndexWithCustomizations(c *gin.Context, indexPath string) {
	// 读取主题的index.html文件
	data, err := os.ReadFile(indexPath)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}

	// 使用通用的自定义内容应用函数
	content := applyCustomizations(string(data))

	c.Header("Content-Type", "text/html")
	c.Data(http.StatusOK, "text/html", []byte(content))
}

// getContentType 根据文件扩展名返回Content-Type
func getContentType(path string) string {
	if strings.HasSuffix(path, ".css") {
		return "text/css"
	} else if strings.HasSuffix(path, ".js") {
		return "application/javascript"
	} else if strings.HasSuffix(path, ".png") {
		return "image/png"
	} else if strings.HasSuffix(path, ".jpg") || strings.HasSuffix(path, ".jpeg") {
		return "image/jpeg"
	} else if strings.HasSuffix(path, ".gif") {
		return "image/gif"
	} else if strings.HasSuffix(path, ".svg") {
		return "image/svg+xml"
	} else if strings.HasSuffix(path, ".ico") {
		return "image/x-icon"
	} else if strings.HasSuffix(path, ".woff") {
		return "font/woff"
	} else if strings.HasSuffix(path, ".woff2") {
		return "font/woff2"
	}
	return "application/octet-stream"
}
