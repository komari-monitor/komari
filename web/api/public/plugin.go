package public

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/internal/plugin"
)

// ServePluginFile serves a public iframe page declared in a plugin manifest
// (visibility=public). It is reachable without authentication; the plugin
// must be enabled and the requested file must be listed as a public page.
func ServePluginFile(c *gin.Context) {
	name := strings.TrimPrefix(c.Param("filepath"), "/")
	full, err := plugin.ResolvePublicFile(c.Param("short"), name)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	c.File(full)
}
