package install

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/database/metricstore"
	"github.com/komari-monitor/komari/database/models"
	appconfig "github.com/komari-monitor/komari/internal/config"
	logger "github.com/komari-monitor/komari/utils/log"
	"github.com/komari-monitor/komari/web/api"
	"github.com/komari-monitor/komari/web/backup"
	"gorm.io/gorm"
)

const (
	PagePath = "/install"
	APIPath  = "/api/install"
)

type Status struct {
	State    string `json:"state"`
	Required bool   `json:"required"`
}

type completeRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	Sitename    string `json:"sitename"`
	Description string `json:"description"`
	MetricDSN   string `json:"metric_dsn"`
}

type Controller struct {
	db     *gorm.DB
	active atomic.Bool
	mu     sync.Mutex
	state  string
	done   chan struct{}
}

func NewController(db *gorm.DB) *Controller {
	return &Controller{db: db, state: "ready", done: make(chan struct{})}
}

func (c *Controller) Activate() { c.active.Store(true) }

func (c *Controller) Deactivate() { c.active.Store(false) }

func (c *Controller) Done() <-chan struct{} { return c.done }

func (c *Controller) Register(r *gin.Engine) {
	g := r.Group(APIPath, c.requireActive)
	g.GET("/status", c.status)
	g.POST("/complete", c.complete)
	g.POST("/restore", c.restore)
}

func (c *Controller) requireActive(ctx *gin.Context) {
	if !c.active.Load() {
		ctx.AbortWithStatus(http.StatusNotFound)
		return
	}
	ctx.Next()
}

func (c *Controller) status(ctx *gin.Context) {
	c.mu.Lock()
	state := c.state
	c.mu.Unlock()
	api.RespondSuccess(ctx, Status{State: state, Required: state != "completed"})
}

// restore stages a verified backup before the guide creates any new data.
func (c *Controller) restore(ctx *gin.Context) {
	file, header, err := ctx.Request.FormFile("backup")
	if err != nil {
		api.RespondError(ctx, http.StatusBadRequest, fmt.Sprintf("get uploaded backup: %v", err))
		return
	}
	defer file.Close()
	if err := backup.SaveUploadedBackup(file, header.Filename); err != nil {
		api.RespondError(ctx, http.StatusBadRequest, err.Error())
		return
	}

	api.RespondSuccessMessage(ctx, "backup uploaded; restarting to restore", gin.H{})
	go func() {
		logger.InfoArgs("install", "Backup uploaded, restarting service to restore it on startup...")
		time.Sleep(2 * time.Second)
		os.Exit(0)
	}()
}

func (c *Controller) complete(ctx *gin.Context) {
	var request completeRequest
	if err := decodeJSON(ctx, &request); err != nil {
		api.RespondError(ctx, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateRequest(&request); err != nil {
		api.RespondError(ctx, http.StatusBadRequest, err.Error())
		return
	}

	c.mu.Lock()
	if c.state != "ready" {
		c.mu.Unlock()
		api.RespondError(ctx, http.StatusConflict, "installation is already completed or running")
		return
	}
	c.state = "completing"
	c.mu.Unlock()

	cfg, err := metricConfig(request)
	if err == nil {
		pingCtx, cancel := context.WithTimeout(ctx.Request.Context(), 15*time.Second)
		err = metricstore.TestConnection(pingCtx, cfg)
		cancel()
	}
	if err != nil {
		c.fail()
		api.RespondError(ctx, http.StatusBadRequest, fmt.Sprintf("monitoring database connection failed: %v", err))
		return
	}

	if err := c.createAccountAndSettings(&request, cfg); err != nil {
		c.fail()
		api.RespondError(ctx, http.StatusInternalServerError, "failed to save installation settings")
		return
	}

	c.mu.Lock()
	c.state = "completed"
	c.mu.Unlock()
	go func() {
		time.Sleep(500 * time.Millisecond)
		c.Deactivate()
		close(c.done)
	}()
	api.RespondSuccessMessage(ctx, "installation completed", gin.H{})
}

func (c *Controller) fail() {
	c.mu.Lock()
	c.state = "ready"
	c.mu.Unlock()
}

func (c *Controller) createAccountAndSettings(request *completeRequest, cfg *metricstore.MetricStoreConfig) error {
	var count int64
	if err := c.db.Model(&models.User{}).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("installation is already completed")
	}
	user, err := accounts.CreateAccountWithDB(c.db, request.Username, request.Password)
	if err != nil {
		return err
	}
	settings := map[string]any{
		appconfig.SitenameKey:         request.Sitename,
		appconfig.DescriptionKey:      request.Description,
		metricstore.MetricDBDriverKey: cfg.Driver,
		metricstore.MetricDBDSNKey:    cfg.DSN,
	}
	if err := appconfig.SetMany(settings); err != nil {
		_ = accounts.DeleteAccountByUsernameWithDB(c.db, user.Username)
		return err
	}
	return nil
}

func validateRequest(request *completeRequest) error {
	request.Username = strings.TrimSpace(request.Username)
	request.Sitename = strings.TrimSpace(request.Sitename)
	request.Description = strings.TrimSpace(request.Description)
	request.MetricDSN = strings.TrimSpace(request.MetricDSN)
	if request.Username == "" || utf8.RuneCountInString(request.Username) > 64 {
		return fmt.Errorf("username must be between 1 and 64 characters")
	}
	passwordLength := utf8.RuneCountInString(request.Password)
	if passwordLength < 8 || passwordLength > 256 {
		return fmt.Errorf("password must be between 8 and 256 characters")
	}
	if !hasStrongPassword(request.Password) {
		return fmt.Errorf("password must contain uppercase, lowercase letters, and numbers")
	}
	if request.Sitename == "" || utf8.RuneCountInString(request.Sitename) > 100 {
		return fmt.Errorf("site name must be between 1 and 100 characters")
	}
	if utf8.RuneCountInString(request.Description) > 1000 {
		return fmt.Errorf("site description must be at most 1000 characters")
	}
	if request.MetricDSN == "" {
		return fmt.Errorf("monitoring database DSN is required")
	}
	return nil
}

func hasStrongPassword(password string) bool {
	var upper, lower, digit bool
	for _, char := range password {
		upper = upper || unicode.IsUpper(char)
		lower = lower || unicode.IsLower(char)
		digit = digit || unicode.IsDigit(char)
	}
	return upper && lower && digit
}

func metricConfig(request completeRequest) (*metricstore.MetricStoreConfig, error) {
	dsn := request.MetricDSN
	driver, ok := metricstore.InferDriverFromDSN(dsn)
	if !ok {
		return nil, fmt.Errorf("cannot infer monitoring database type from DSN")
	}
	return &metricstore.MetricStoreConfig{Driver: string(driver), DSN: dsn}, nil
}

func decodeJSON(ctx *gin.Context, target any) error {
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, 1<<20)
	decoder := json.NewDecoder(ctx.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	return nil
}
