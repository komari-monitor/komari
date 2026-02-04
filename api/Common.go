package api

import (
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/komari-monitor/komari/common"
	"github.com/patrickmn/go-cache"

	"strconv"

	"github.com/komari-monitor/komari/config"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/utils"
)

var (
	Records = cache.New(1*time.Minute, 1*time.Minute)
)

type TerminalSession struct {
	UUID        string
	UserUUID    string
	Browser     *websocket.Conn
	Agent       *websocket.Conn
	RequesterIp string
}

var TerminalSessionsMutex = &sync.Mutex{}
var TerminalSessions = make(map[string]*TerminalSession)

func SaveClientReportToDB() error {
	lastMinute := time.Now().Add(-time.Minute).Unix()
	var records []models.Record
	var gpuRecords []models.GPURecord

	// 遍历所有客户端记录
	for uuid, x := range Records.Items() {
		if uuid == "" {
			continue
		}

		reports, ok := x.Object.([]common.Report)
		if !ok {
			log.Printf("Invalid report type for UUID %s", uuid)
			continue
		}

		// 过滤一分钟前的记录
		var filtered []common.Report
		for _, r := range reports {
			if r.UpdatedAt.Unix() >= lastMinute {
				filtered = append(filtered, r)
			}
		}

		// 更新缓存
		Records.Set(uuid, filtered, cache.DefaultExpiration)

		// 计算平均报告并添加到记录列表
		if len(filtered) > 0 {
			r := utils.AverageReport(uuid, time.Now(), filtered, 0.3)
			records = append(records, r)

			// 使用与其他数据相同的聚合逻辑处理GPU数据
			gpuAggregated := utils.AverageGPUReports(uuid, time.Now(), filtered, 0.3)
			gpuRecords = append(gpuRecords, gpuAggregated...)
		}
	}

	// 批量插入数据库前去重（client与time共同构成唯一键）
	db := dbcore.GetDBInstance()

	if len(records) > 0 {
		unique := make(map[string]models.Record)
		for _, rec := range records {
			key := rec.Client + "_" + strconv.FormatInt(rec.Time.ToTime().Unix(), 10)
			unique[key] = rec
		}
		var deduped []models.Record
		for _, rec := range unique {
			deduped = append(deduped, rec)
		}
		if err := db.Model(&models.Record{}).Create(&deduped).Error; err != nil {
			log.Printf("Failed to save records to database: %v", err)
			return err
		}
	}

	// 批量插入GPU记录
	if len(gpuRecords) > 0 {
		// GPU记录也需要去重，防止重复插入
		gpuUnique := make(map[string]models.GPURecord)
		for _, rec := range gpuRecords {
			key := rec.Client + "_" + strconv.Itoa(rec.DeviceIndex) + "_" + strconv.FormatInt(rec.Time.ToTime().Unix(), 10)
			gpuUnique[key] = rec
		}
		var gpuDeduped []models.GPURecord
		for _, rec := range gpuUnique {
			gpuDeduped = append(gpuDeduped, rec)
		}
		if err := db.Model(&models.GPURecord{}).Create(&gpuDeduped).Error; err != nil {
			log.Printf("Failed to save GPU records to database: %v", err)
			return err
		}
	}

	return nil
}

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
		"version": utils.CurrentVersion,
		"hash":    utils.VersionHash,
	})
}

func isApiKeyValid(apiKey string) bool {
	apiKeyConfig, err := config.GetAs[string](config.ApiKeyKey, "")
	if err != nil {
		return false
	}

	if apiKeyConfig == "" || len(apiKeyConfig) < 12 {
		return false
	}
	return apiKey == "Bearer "+apiKeyConfig
}
