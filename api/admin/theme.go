package admin

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/api"
	"github.com/komari-monitor/komari/config"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/public"
)

// UploadTheme 上传主题
func UploadTheme(c *gin.Context) {
	// 读取上传的文件内容
	data, err := io.ReadAll(c.Request.Body)
	if err != nil || len(data) == 0 {
		api.RespondError(c, http.StatusBadRequest, "请选择要上传的主题文件")
		return
	}

	// 临时文件名
	tempFile := filepath.Join(os.TempDir(), "uploaded_theme.zip")
	if err := os.WriteFile(tempFile, data, 0644); err != nil {
		api.RespondError(c, http.StatusInternalServerError, "保存文件失败: "+err.Error())
		return
	}
	defer os.Remove(tempFile)

	// 检查文件扩展名（这里假定上传的就是zip）
	if !strings.HasSuffix(strings.ToLower(tempFile), ".zip") {
		api.RespondError(c, http.StatusBadRequest, "只支持ZIP格式的主题文件")
		return
	}

	// 解压ZIP文件并验证
	themeInfo, err := extractAndValidateTheme(tempFile)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, err.Error())
		return
	}

	api.RespondSuccessMessage(c, "主题上传成功", themeInfo)
}

// ListThemes 列出所有主题
func ListThemes(c *gin.Context) {
	dataDir := "./data/theme"

	// 确保主题目录存在
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		api.RespondSuccess(c, []models.Theme{})
		return
	}

	entries, err := os.ReadDir(dataDir)
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "读取主题目录失败: "+err.Error())
		return
	}

	var themes []models.Theme
	defaultTheme, err := public.PublicFS.ReadFile("defaultTheme/komari-theme.json")
	if err == nil {
		dt := models.Theme{}
		err := json.Unmarshal(defaultTheme, &dt)
		if err == nil {
			themes = append(themes, dt)
		}

	}
	for _, entry := range entries {
		if entry.IsDir() {
			themeConfigPath := filepath.Join(dataDir, entry.Name(), "komari-theme.json")
			if themeInfo, err := loadThemeConfig(themeConfigPath); err == nil {
				themes = append(themes, themeInfo)
			}
		}
	}

	api.RespondSuccess(c, themes)
}

// DeleteTheme 删除主题
func DeleteTheme(c *gin.Context) {
	var req struct {
		Short string `json:"short" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		api.RespondError(c, http.StatusBadRequest, "参数错误: "+err.Error())
		return
	}

	if req.Short == "default" {
		api.RespondError(c, http.StatusBadRequest, "默认主题不能删除")
		return
	}

	themeDir := filepath.Join("./data/theme", req.Short)

	// 检查主题是否存在
	if _, err := os.Stat(themeDir); os.IsNotExist(err) {
		api.RespondError(c, http.StatusNotFound, "主题不存在")
		return
	}

	// 删除主题目录
	if err := os.RemoveAll(themeDir); err != nil {
		api.RespondError(c, http.StatusInternalServerError, "删除主题失败: "+err.Error())
		return
	}

	api.RespondSuccessMessage(c, "主题删除成功", nil)
}

// SetTheme 设置主题
func SetTheme(c *gin.Context) {
	themeName := c.Query("theme")
	if themeName == "" {
		api.RespondError(c, http.StatusBadRequest, "主题名称不能为空")
		return
	}

	// 如果不是default主题，检查主题是否存在
	if themeName != "default" {
		themeDir := filepath.Join("./data/theme", themeName)
		themeConfigPath := filepath.Join(themeDir, "komari-theme.json")

		if _, err := os.Stat(themeConfigPath); os.IsNotExist(err) {
			api.RespondError(c, http.StatusNotFound, "主题不存在")
			return
		}
	}

	if err := config.Set("theme", themeName); err != nil {
		api.RespondError(c, http.StatusInternalServerError, "更新主题设置失败: "+err.Error())
		return
	}

	api.RespondSuccessMessage(c, "主题设置成功", gin.H{"theme": themeName})
}

// extractAndValidateTheme 解压并验证主题
func extractAndValidateTheme(zipPath string) (models.Theme, error) {
	var themeInfo models.Theme

	// 打开ZIP文件
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return themeInfo, fmt.Errorf("无法打开ZIP文件: %v", err)
	}
	defer r.Close()

	// 查找komari-theme.json文件
	var themeConfigFile *zip.File
	for _, f := range r.File {
		if f.Name == "komari-theme.json" {
			themeConfigFile = f
			break
		}
	}

	if themeConfigFile == nil {
		return themeInfo, fmt.Errorf("主题配置文件 komari-theme.json 不存在")
	}

	// 读取主题配置
	rc, err := themeConfigFile.Open()
	if err != nil {
		return themeInfo, fmt.Errorf("无法读取主题配置文件: %v", err)
	}
	defer rc.Close()

	configData, err := io.ReadAll(rc)
	if err != nil {
		return themeInfo, fmt.Errorf("读取主题配置失败: %v", err)
	}

	if err := json.Unmarshal(configData, &themeInfo); err != nil {
		return themeInfo, fmt.Errorf("主题配置格式错误: %v", err)
	}

	// 验证必填字段
	if themeInfo.Name == "" || themeInfo.Short == "" {
		return themeInfo, fmt.Errorf("主题配置缺少必填字段（name、short）")
	}

	// 验证Short字段格式（只允许字母、数字、下划线、连字符）
	if !isValidThemeShort(themeInfo.Short) {
		return themeInfo, fmt.Errorf("主题short字段格式无效，只允许字母、数字、下划线和连字符")
	}

	// 创建主题目录
	themeDir := filepath.Join("./data/theme", themeInfo.Short)

	// 如果目录已存在，先删除
	if _, err := os.Stat(themeDir); err == nil {
		if err := os.RemoveAll(themeDir); err != nil {
			return themeInfo, fmt.Errorf("删除原有主题失败: %v", err)
		}
	}

	if err := os.MkdirAll(themeDir, 0755); err != nil {
		return themeInfo, fmt.Errorf("创建主题目录失败: %v", err)
	}

	// 解压文件到主题目录
	for _, f := range r.File {
		path := filepath.Join(themeDir, f.Name)

		// 安全检查，防止路径遍历攻击
		if !strings.HasPrefix(path, filepath.Clean(themeDir)+string(os.PathSeparator)) {
			continue
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(path, f.FileInfo().Mode())
			continue
		}

		// 创建目录
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return themeInfo, fmt.Errorf("创建目录失败: %v", err)
		}

		// 解压文件
		rc, err := f.Open()
		if err != nil {
			return themeInfo, fmt.Errorf("打开压缩文件失败: %v", err)
		}

		outFile, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.FileInfo().Mode())
		if err != nil {
			rc.Close()
			return themeInfo, fmt.Errorf("创建文件失败: %v", err)
		}

		_, err = io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()

		if err != nil {
			return themeInfo, fmt.Errorf("解压文件失败: %v", err)
		}
	}

	return themeInfo, nil
}

// loadThemeConfig 加载主题配置
func loadThemeConfig(configPath string) (models.Theme, error) {
	var themeInfo models.Theme

	data, err := os.ReadFile(configPath)
	if err != nil {
		return themeInfo, err
	}

	if err := json.Unmarshal(data, &themeInfo); err != nil {
		return themeInfo, err
	}

	return themeInfo, nil
}

// isValidThemeShort 验证主题short字段格式
func isValidThemeShort(short string) bool {
	if short == "" || short == "default" {
		return false
	}

	for _, r := range short {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}

	return true
}

// downloadThemeFromURL 从URL下载主题文件
func downloadThemeFromURL(url string) ([]byte, error) {
	// 发送HTTP GET请求
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("下载主题文件失败: %v", err)
	}
	defer resp.Body.Close()

	// 检查响应状态码
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载主题文件失败，HTTP状态码: %d", resp.StatusCode)
	}

	// 读取响应内容
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取主题文件内容失败: %v", err)
	}

	// 检查文件大小
	if len(data) == 0 {
		return nil, errors.New("下载的主题文件为空")
	}

	return data, nil
}

// getGitHubReleaseDownloadURL 从GitHub API获取最新release的下载链接
// 该函数通过GitHub API获取指定仓库最新release的资源下载链接
// 参考API: https://api.github.com/repos/{owner}/{repo}/releases/latest
// 参数:
//   - owner: GitHub仓库所有者
//   - repo: GitHub仓库名称
//
// 返回:
//   - 最新release的第一个资源的下载链接
//   - 错误信息（如果有）
func getGitHubReleaseDownloadURL(owner, repo string) (string, error) {
	if owner == "" || repo == "" {
		return "", errors.New("GitHub仓库所有者和仓库名称不能为空")
	}

	// 构建GitHub API URL
	// 使用GitHub API获取最新release信息
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)

	// 发送HTTP GET请求
	resp, err := http.Get(apiURL)
	if err != nil {
		return "", fmt.Errorf("获取GitHub release信息失败: %v", err)
	}
	defer resp.Body.Close()

	// 检查响应状态码
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("获取GitHub release信息失败，HTTP状态码: %d", resp.StatusCode)
	}

	// 解析JSON响应
	// GitHub API返回的JSON包含assets数组，每个asset包含browser_download_url字段
	var releaseInfo struct {
		Assets []struct {
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&releaseInfo); err != nil {
		return "", fmt.Errorf("解析GitHub API响应失败: %v", err)
	}

	// 检查是否有可下载的资源
	if len(releaseInfo.Assets) == 0 {
		return "", errors.New("GitHub release中没有可下载的资源")
	}

	// 返回第一个资源的下载链接
	// 相当于shell命令: curl -s https://api.github.com/repos/owner/repo/releases/latest | jq -r ".assets[0].browser_download_url"
	return releaseInfo.Assets[0].BrowserDownloadURL, nil
}

// isGitHubRepoURL 检查URL是否是GitHub仓库地址
// 支持的格式:
// - https://github.com/owner/repo
// - https://github.com/owner/repo.git
// - https://www.github.com/owner/repo
// - http://github.com/owner/repo
// 返回:
//   - 是否是GitHub仓库URL
//   - 仓库所有者
//   - 仓库名称
func isGitHubRepoURL(urlStr string) (bool, string, string) {
	if urlStr == "" {
		return false, "", ""
	}

	// 检查URL是否包含github.com
	if !strings.Contains(strings.ToLower(urlStr), "github.com") {
		return false, "", ""
	}

	// 解析URL
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return false, "", ""
	}

	// 检查主机名是否是github.com或www.github.com
	hostname := strings.ToLower(parsedURL.Host)
	if hostname != "github.com" && hostname != "www.github.com" {
		return false, "", ""
	}

	// 解析路径部分，提取owner和repo
	// 路径格式应该是 /owner/repo 或 /owner/repo.git
	path := strings.TrimPrefix(parsedURL.Path, "/")
	parts := strings.Split(path, "/")

	if len(parts) < 2 {
		return false, "", ""
	}

	owner := parts[0]
	repo := parts[1]

	// 如果repo以.git结尾，去掉这个后缀
	repo = strings.TrimSuffix(repo, ".git")

	return true, owner, repo
}

// UpdateTheme 更新主题
// 支持四种更新方式：
// 1. 使用主题原有URL下载更新
// 2. 提供新的直接下载URL进行更新
// 3. 提供GitHub仓库信息，从最新release下载更新
// 4. 如果主题URL是GitHub仓库地址，自动获取最新release
func UpdateTheme(c *gin.Context) {
	var req struct {
		Short    string `json:"short" binding:"required"` // 主题短名称
		URL      string `json:"url"`                      // 新的URL地址（可选）
		GitOwner string `json:"git_owner"`                // GitHub仓库所有者（可选）
		GitRepo  string `json:"git_repo"`                 // GitHub仓库名称（可选）
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		api.RespondError(c, http.StatusBadRequest, "参数错误: "+err.Error())
		return
	}

	// 检查主题是否存在
	themeDir := filepath.Join("./data/theme", req.Short)
	themeConfigPath := filepath.Join(themeDir, "komari-theme.json")

	if _, err := os.Stat(themeConfigPath); os.IsNotExist(err) {
		api.RespondError(c, http.StatusNotFound, "主题不存在")
		return
	}

	// 加载现有主题配置
	themeInfo, err := loadThemeConfig(themeConfigPath)
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "读取主题配置失败: "+err.Error())
		return
	}

	// 方式1和方式4: 尝试从原始URL下载主题
	// 如果原始URL是GitHub仓库地址，则自动获取最新release
	var themeData []byte
	// 不保存下载链接，更新后由主题覆盖
	//var downloadURL string
	// var err2 error

	if themeInfo.URL != "" {
		// 检查原始URL是否是GitHub仓库地址
		// 例如: https://github.com/owner/repo
		isGitHub, owner, repo := isGitHubRepoURL(themeInfo.URL)
		if isGitHub {
			// 方式4: 如果原始URL是GitHub仓库地址，自动获取最新release
			// 这是本次需求的核心功能：当主题文件中现有的url地址如果是github仓库的路径，则直接引用该url地址去下载最新的release
			gitHubURL, err := getGitHubReleaseDownloadURL(owner, repo)
			if err == nil {
				// 使用获取到的GitHub release下载链接下载主题
				themeData, _ = downloadThemeFromURL(gitHubURL)
				//if err2 == nil {
				// 注意：这里我们保存的是release的下载链接，而不是GitHub仓库地址
				// 这样做是为了在下载成功后，将这个具体的release下载链接保存到主题配置中
				// 但在下次更新时，我们仍然会检测到这是一个GitHub仓库，并获取最新的release
				// downloadURL = gitHubURL
				//}
			}
		} else {
			// 原始URL不是GitHub仓库地址，直接尝试下载（方式1）
			themeData, _ = downloadThemeFromURL(themeInfo.URL)
			//if err2 == nil {
			// downloadURL = themeInfo.URL
			//}
		}
	}

	// 如果原始URL下载失败，尝试其他方式下载
	if themeData == nil || len(themeData) == 0 {
		// 方式3: 如果提供了GitHub仓库信息，尝试从GitHub最新release下载
		// 这种方式允许用户只需提供owner和repo信息，系统会自动获取最新release的下载链接
		if req.GitOwner != "" && req.GitRepo != "" {
			// 从GitHub API获取下载链接
			// 相当于: DOWNLOAD_URL=$(curl -s https://api.github.com/repos/owner/repo/releases/latest | jq -r ".assets[0].browser_download_url")
			gitHubURL, err := getGitHubReleaseDownloadURL(req.GitOwner, req.GitRepo)
			if err != nil {
				api.RespondError(c, http.StatusBadRequest, "从GitHub获取下载链接失败: "+err.Error())
				return
			}

			// 使用获取到的链接下载主题
			themeData, err = downloadThemeFromURL(gitHubURL)
			if err != nil {
				api.RespondError(c, http.StatusBadRequest, "从GitHub下载主题失败: "+err.Error())
				return
			}
			// 保存下载链接，稍后更新到主题配置中
			// downloadURL = gitHubURL
		} else if req.URL != "" {
			// 方式2: 如果提供了新URL，尝试从新URL下载
			// 检查新URL是否是GitHub仓库地址
			isGitHub, owner, repo := isGitHubRepoURL(req.URL)
			if isGitHub {
				// 如果新URL是GitHub仓库地址，获取最新release
				// 这里也应用了自动检测GitHub仓库并下载最新release的功能
				gitHubURL, err := getGitHubReleaseDownloadURL(owner, repo)
				if err != nil {
					api.RespondError(c, http.StatusBadRequest, "从GitHub获取下载链接失败: "+err.Error())
					return
				}

				// 使用获取到的链接下载主题
				themeData, err = downloadThemeFromURL(gitHubURL)
				if err != nil {
					api.RespondError(c, http.StatusBadRequest, "从GitHub下载主题失败: "+err.Error())
					return
				}
				// 保存GitHub仓库URL，而不是release下载链接，以便将来可以获取最新版本
				// 这是一个重要的设计决策：我们保存的是GitHub仓库URL，而不是具体的release下载链接
				// 这样在下次更新时，系统会再次检测到这是GitHub仓库，并自动获取最新的release
				// downloadURL = req.URL
			} else {
				// 新URL不是GitHub仓库地址，直接尝试下载
				themeData, err = downloadThemeFromURL(req.URL)
				if err != nil {
					api.RespondError(c, http.StatusBadRequest, "从新URL下载主题失败: "+err.Error())
					return
				}
				// downloadURL = req.URL
			}
		}
	}

	// 如果没有成功下载主题数据
	if themeData == nil || len(themeData) == 0 {
		api.RespondError(c, http.StatusBadRequest, "无法下载主题，请提供有效的URL或GitHub仓库信息")
		return
	}

	// 到这里，我们已经成功获取了主题数据，可能是通过以下四种方式之一：
	// 1. 原始URL直接下载
	// 2. 原始URL是GitHub仓库，自动获取最新release下载
	// 3. 用户提供的新URL下载
	// 4. 用户提供的GitHub仓库信息，获取最新release下载

	// 临时文件名
	tempFile := filepath.Join(os.TempDir(), "downloaded_theme.zip")
	if err := os.WriteFile(tempFile, themeData, 0644); err != nil {
		api.RespondError(c, http.StatusInternalServerError, "保存文件失败: "+err.Error())
		return
	}
	defer os.Remove(tempFile)

	// 解压ZIP文件并验证
	updatedThemeInfo, err := extractAndValidateTheme(tempFile)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, err.Error())
		return
	}

	// 如果下载URL与原始URL不同，更新主题配置中的URL
	// if downloadURL != themeInfo.URL {
	// 	updatedThemeInfo.URL = downloadURL

	// 	// 更新主题配置文件
	// 	updatedConfigPath := filepath.Join("./data/theme", updatedThemeInfo.Short, "komari-theme.json")
	// 	updatedConfigData, err := json.MarshalIndent(updatedThemeInfo, "", "  ")
	// 	if err != nil {
	// 		api.RespondError(c, http.StatusInternalServerError, "生成主题配置失败: "+err.Error())
	// 		return
	// 	}

	// 	if err := os.WriteFile(updatedConfigPath, updatedConfigData, 0644); err != nil {
	// 		api.RespondError(c, http.StatusInternalServerError, "更新主题配置文件失败: "+err.Error())
	// 		return
	// 	}
	// }

	api.RespondSuccessMessage(c, "主题更新成功", updatedThemeInfo)
}

func UpdateThemeSettings(c *gin.Context) {
	theme := c.Query("theme")
	if theme == "" {
		api.RespondError(c, http.StatusBadRequest, "主题名称不能为空")
		return
	}

	var req map[string]any

	err := c.ShouldBindJSON(&req)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, "参数错误: "+err.Error())
		return
	}
	db := dbcore.GetDBInstance()

	data, err := json.Marshal(&req)
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "生成主题配置失败: "+err.Error())
		return
	}

	var themeCfg models.ThemeConfiguration
	db.Where("short = ?", theme).
		Assign(models.ThemeConfiguration{Short: theme, Data: string(data)}).
		FirstOrCreate(&themeCfg)
	api.RespondSuccess(c, nil)
}
