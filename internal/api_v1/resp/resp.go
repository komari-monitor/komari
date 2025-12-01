package resp

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/internal/conf"
)

type Response struct {
	Status  string      `json:"status"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// Respond sends a standardized JSON response.
func Respond(c *gin.Context, httpStatus int, status string, message string, data interface{}) {
	c.JSON(httpStatus, Response{Status: status, Message: message, Data: data})
}

// RespondSuccess sends a success response with data.
func RespondSuccess(c *gin.Context, data interface{}) {
	Respond(c, http.StatusOK, "success", "", data)
}

// RespondSuccessMessage sends a success response with message and data.
func RespondSuccessMessage(c *gin.Context, message string, data interface{}) {
	Respond(c, http.StatusOK, "success", message, data)
}

// RespondError sends an error response with message.
func RespondError(c *gin.Context, httpStatus int, message string) {
	Respond(c, httpStatus, "error", message, nil)
}
func GetVersion(c *gin.Context) {
	RespondSuccess(c, gin.H{
		"version": conf.Version,
		"hash":    conf.CommitHash,
	})
}
