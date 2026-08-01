package admin

import (
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/internal/plugin"
	"github.com/komari-monitor/komari/web/api"
)

// UploadPlugin installs a plugin ZIP package. The request body is the raw
// ZIP, mirroring UploadTheme.
func UploadPlugin(c *gin.Context) {
	data, err := io.ReadAll(c.Request.Body)
	if err != nil || len(data) == 0 {
		api.RespondError(c, http.StatusBadRequest, "请选择要上传的插件文件")
		return
	}

	tempFile, err := os.CreateTemp("", "komari-plugin-*.zip")
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "保存文件失败: "+err.Error())
		return
	}
	tempPath := tempFile.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if _, err := tempFile.Write(data); err != nil {
		_ = tempFile.Close()
		api.RespondError(c, http.StatusInternalServerError, "保存文件失败: "+err.Error())
		return
	}
	if err := tempFile.Close(); err != nil {
		api.RespondError(c, http.StatusInternalServerError, "保存文件失败: "+err.Error())
		return
	}

	info, err := plugin.InstallZip(tempPath)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, err.Error())
		return
	}
	api.RespondSuccessMessage(c, "插件上传成功", info)
}

// ServePluginFile serves a static file from an installed plugin directory,
// used by injected plugin admin pages.
func ServePluginFile(c *gin.Context) {
	name := strings.TrimPrefix(c.Param("filepath"), "/")
	full, err := plugin.ResolveFile(c.Param("short"), name)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	c.File(full)
}
