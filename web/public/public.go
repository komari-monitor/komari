package public

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/config"
)

//go:embed defaultTheme
var PublicFS embed.FS

// 常量定义
const (
	DataDir      = "./data"
	ThemesDir    = "theme"
	FaviconFile  = "favicon.ico"
	DefaultTheme = "default"

	// 主题内部结构定义
	DistDir   = "dist"       // 静态资源存放目录
	IndexFile = "index.html" // 相对于 DistDir
)

func init() {
	_ = os.MkdirAll("./data/theme", 0755)
}

// isSafePath 验证路径是否在指定的基础目录内，防止路径穿透攻击
func isSafePath(basePath, targetPath string) bool {
	// 获取基础目录的绝对路径
	absBase, err := filepath.Abs(basePath)
	if err != nil {
		return false
	}

	// 清理目标路径，移除 ../ 等
	cleanTarget := filepath.Clean(targetPath)

	// 拼接完整路径
	fullPath := filepath.Join(absBase, cleanTarget)

	// 获取绝对路径
	absTarget, err := filepath.Abs(fullPath)
	if err != nil {
		return false
	}

	// 检查目标路径是否以基础路径开头
	// 使用 filepath.Rel 更可靠地检查路径关系
	rel, err := filepath.Rel(absBase, absTarget)
	if err != nil {
		return false
	}

	// 如果相对路径以 .. 开头，说明目标在基础目录之外
	return !strings.HasPrefix(rel, "..") && rel != ".."
}

// Static 注册静态资源和 SPA 路由处理
func Static(r *gin.RouterGroup, noRoute func(handlers ...gin.HandlerFunc)) {
	// 初始化嵌入式文件系统，指向 defaultTheme 根目录
	// 假设 defaultTheme 内部结构也是: dist/, theme.json 等
	defaultThemeFS, err := fs.Sub(PublicFS, "defaultTheme")
	if err != nil {
		panic("you may forget to put dist of frontend to public/defaultTheme/dist")
	}

	getConfig := func() map[string]any {
		cfg, _ := config.GetMany(map[string]any{
			config.DescriptionKey: "A simple server monitor tool.",
			config.CustomHeadKey:  "",
			config.CustomBodyKey:  "",
			config.SitenameKey:    "Komari Monitor",
			config.ThemeKey:       DefaultTheme,
		})
		return cfg
	}

	// 核心逻辑：获取文件内容
	// filePath: 相对于主题根目录的路径 (例如 "theme.json" 或 "dist/assets/a.js")
	// 返回: content, contentType, exists
	getFileContent := func(themeID string, relativePath string) ([]byte, string, bool) {
		cleanPath := strings.TrimPrefix(relativePath, "/")

		cleanPath = filepath.Clean(cleanPath)

		if themeID != DefaultTheme {
			if strings.Contains(themeID, "..") || strings.Contains(themeID, "/") || strings.Contains(themeID, "\\") {
				return nil, "", false
			}

			themeBasePath := filepath.Join(DataDir, ThemesDir, themeID)

			if !isSafePath(themeBasePath, cleanPath) {
				return nil, "", false
			}

			localPath := filepath.Join(themeBasePath, cleanPath)
			// 检查文件是否存在且不是目录
			if info, err := os.Stat(localPath); err == nil && !info.IsDir() {
				content, err := os.ReadFile(localPath)
				if err == nil {
					return content, mime.TypeByExtension(filepath.Ext(localPath)), true
				}
			}
			// 本地文件不存在，或读取失败 -> 继续向下回退
		}

		// 2. 尝试从嵌入式 defaultTheme/{cleanPath} 读取
		// fs.ReadFile 处理 embed 路径时使用 "/"
		embedPath := filepath.ToSlash(cleanPath)

		if strings.Contains(embedPath, "..") {
			return nil, "", false
		}

		if content, err := fs.ReadFile(defaultThemeFS, embedPath); err == nil {
			return content, mime.TypeByExtension(filepath.Ext(embedPath)), true
		}

		return nil, "", false
	}

	// 核心逻辑：渲染 Index.html
	serveIndex := func(c *gin.Context) {
		reqPath := c.Request.URL.Path
		cfg := getConfig()

		currentTheme := cfg[config.ThemeKey].(string)
		shouldReplace := true

		// 特殊页面：强制使用 default 主题，且不进行内容替换
		if strings.HasPrefix(reqPath, "/admin") || strings.HasPrefix(reqPath, "/terminal") {
			currentTheme = DefaultTheme
			shouldReplace = false
		}

		// 获取 dist/index.html (相对于主题根目录)
		targetFile := path.Join(DistDir, IndexFile)
		content, _, exists := getFileContent(currentTheme, targetFile)

		if !exists {
			c.String(http.StatusNotFound, "Index file missing (checked %s/dist/index.html and default).", currentTheme)
			return
		}

		// 如果不替换，直接返回原始内容
		if !shouldReplace {
			c.Data(http.StatusOK, "text/html; charset=utf-8", content)
			return
		}

		// 执行 HTML 内容替换
		htmlStr := string(content)
		replacer := strings.NewReplacer(
			"<title>Komari Monitor</title>", "<title>"+cfg[config.SitenameKey].(string)+"</title>",
			"A simple server monitor tool.", cfg[config.DescriptionKey].(string),
			"</head>", cfg[config.CustomHeadKey].(string)+"</head>",
			"</body>", cfg[config.CustomBodyKey].(string)+"</body>",
		)

		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(replacer.Replace(htmlStr)))
	}

	// ================= 路由定义 =================

	// 1. Favicon 优先策略
	r.GET("/favicon.ico", func(c *gin.Context) {
		// 优先：./data/favicon.ico
		localFavicon := filepath.Join(DataDir, FaviconFile)
		if _, err := os.Stat(localFavicon); err == nil {
			c.File(localFavicon)
			return
		}

		// 其次：当前主题的 dist/favicon.ico 或 theme_root/favicon.ico ?
		// 通常构建后的资源在 dist 中，这里假设优先找 dist 内的，如果你的 favicon 在根目录，去掉 DistDir 拼接即可
		cfg := getConfig()
		themeFaviconPath := path.Join(DistDir, FaviconFile)
		content, mimeType, exists := getFileContent(cfg[config.ThemeKey].(string), themeFaviconPath)
		if exists {
			c.Data(http.StatusOK, mimeType, content)
			return
		}

		c.Status(http.StatusNotFound)
	})

	// 2. 静态资源路由 /themes/:id/*path
	// 允许访问 /themes/MyTheme/theme.json 和 /themes/MyTheme/dist/assets/a.js
	r.GET("/themes/:id/*path", func(c *gin.Context) {
		themeID := c.Param("id")
		// c.Param("path") 包含了开头的 /，getFileContent 会处理
		filePath := c.Param("path")

		content, mimeType, exists := getFileContent(themeID, filePath)
		if exists {
			c.Data(http.StatusOK, mimeType, content)
			return
		}
		c.Status(http.StatusNotFound)
	})

	// 3. SPA 路由 (noRoute)
	noRoute(func(c *gin.Context) {
		if c.Request.Method != http.MethodGet {
			c.Status(http.StatusNotFound)
			return
		}
		//
		func() {
			tempKey := c.Query("temp_key")
			if tempKey == "" {
				return
			}

			tempKeyExpireTime, err := config.GetAs[int64]("tempory_share_token_expire_at", 0)
			if err != nil {
				return
			}
			allowTempKey, err := config.GetAs[string]("tempory_share_token", "")
			if err != nil {
				return
			}

			if allowTempKey == "" || tempKey != allowTempKey {
				return
			}
			now := time.Now().Unix()
			if tempKeyExpireTime < now {
				return
			}
			expireSeconds := int(tempKeyExpireTime - now)
			if expireSeconds > 0 {
				c.SetCookie(
					"temp_key",    // key
					tempKey,       // value
					expireSeconds, // maxAge（秒）
					"/",           // path
					"",            // domain
					false,         // secure
					false,         // httpOnly
				)
			}
		}()
		reqPath := c.Request.URL.Path
		cfg := getConfig()
		currentTheme := cfg[config.ThemeKey].(string)

		// SPA 静态资源回退
		distPath := path.Join(DistDir, reqPath)

		content, mimeType, exists := getFileContent(currentTheme, distPath)
		if exists {
			c.Data(http.StatusOK, mimeType, content)
			return
		}

		// 如果资源不存在，且路径包含扩展名 (如 .js, .css, .png)，则返回 404
		// 避免将 index.html 作为 js 文件返回导致 "Failed to fetch dynamically imported module"
		//ext := filepath.Ext(reqPath)
		//if ext != "" && ext != ".html" {
		//	c.Status(http.StatusNotFound)
		//	return
		//}

		// 路由 (如 /dashboard, /settings) -> 返回 index.html
		serveIndex(c)
	})
}
