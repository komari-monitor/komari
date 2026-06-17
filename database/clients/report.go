package clients

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	v1 "github.com/komari-monitor/komari/protocol/v1"
	"github.com/komari-monitor/komari/utils"

	"gorm.io/gorm"
)

var reportSaveLocks sync.Map

func getReportSaveLock(clientUUID string) *sync.Mutex {
	lock, _ := reportSaveLocks.LoadOrStore(clientUUID, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func getLatestTrafficRecord(tx *gorm.DB, clientUUID string) (*models.Record, error) {
	var record models.Record
	err := tx.Where("client = ?", clientUUID).Order("time DESC").First(&record).Error
	if err == nil {
		return &record, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	err = tx.Table("records_long_term").Where("client = ?", clientUUID).Order("time DESC").First(&record).Error
	if err == nil {
		return &record, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	return nil, nil
}

// Report 表示客户端报告数据
// SaveReport 保存报告数据
func SaveReport(uuid string, data map[string]interface{}) (err error) {

	report, err := ParseReport(data)
	if err != nil {
		return err
	}
	err = SaveClientReport(uuid, report)
	if err != nil {

		return err
	}
	return nil

}

func GetClientUUIDByToken(token string) (clientUUID string, err error) {
	db := dbcore.GetDBInstance()
	var client models.Client
	err = db.Where("token = ?", token).First(&client).Error
	if err != nil {
		return "", err
	}
	return client.UUID, nil
}

func ParseReport(data map[string]interface{}) (report v1.Report, err error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return v1.Report{}, err
	}
	err = json.Unmarshal(jsonData, &report)
	if err != nil {
		return v1.Report{}, err
	}
	return report, nil
}

// 检查数据防止异常数据导致数据库损坏
func ReportVerify(report v1.Report) error {
	// 防止输入不合理范围
	if report.CPU.Usage < 0 || report.CPU.Usage > 100 {
		return fmt.Errorf("CPU.Usage must be between 0 and 100")
	}

	if report.Load.Load1 < 0 || report.Load.Load1 > 1000 {
		return fmt.Errorf("Load.Load1 must be non-negative, got %.2f", report.Load.Load1)
	}

	checkFloat64 := func(name string, val float64) error {
		if val > math.MaxFloat64-1 || val < -math.MaxFloat64+1 {
			return fmt.Errorf("%s value exceeds float64 range: %g", name, val)
		}
		return nil
	}

	// [float64] 防止数据溢出
	if err := checkFloat64("CPU.Usage", report.CPU.Usage); err != nil {
		return err
	}
	if err := checkFloat64("Load.Load1", report.Load.Load1); err != nil {
		return err
	}

	checkInt64 := func(name string, val int64) error {
		if val < 0 {
			return fmt.Errorf("%s must be non-negative, got %d", name, val)
		}
		if val > math.MaxInt64-1 {
			return fmt.Errorf("%s exceeds int64 max limit: %d", name, val)
		}
		return nil
	}

	// [int64] 防止数据溢出
	// Ram 验证
	if err := checkInt64("Ram.Used", report.Ram.Used); err != nil {
		return err
	}
	if err := checkInt64("Ram.Total", report.Ram.Total); err != nil {
		return err
	}
	// Swap 验证
	if err := checkInt64("Swap.Used", report.Swap.Used); err != nil {
		return err
	}
	if err := checkInt64("Swap.Total", report.Swap.Total); err != nil {
		return err
	}
	// Disk 验证
	if err := checkInt64("Disk.Used", report.Disk.Used); err != nil {
		return err
	}
	if err := checkInt64("Disk.Total", report.Disk.Total); err != nil {
		return err
	}
	// Network 验证
	if err := checkInt64("Network.Up", report.Network.Up); err != nil {
		return err
	}
	if err := checkInt64("Network.Down", report.Network.Down); err != nil {
		return err
	}
	if err := checkInt64("Network.TotalUp", report.Network.TotalUp); err != nil {
		return err
	}
	if err := checkInt64("Network.TotalDown", report.Network.TotalDown); err != nil {
		return err
	}
	// 拒绝所有负数Int
	if report.Process < 0 {
		return fmt.Errorf("Process must be non-negative: %d", report.Process)
	}
	if report.Connections.TCP < 0 {
		return fmt.Errorf("Connections.TCP must be non-negative: %d", report.Connections.TCP)
	}
	if report.Connections.UDP < 0 {
		return fmt.Errorf("Connections.UDP must be non-negative: %d", report.Connections.UDP)
	}
	return nil
}

// SaveClientReport 保存客户端报告到 Record 表
func SaveClientReport(clientUUID string, report v1.Report) (err error) {
	db := dbcore.GetDBInstance()
	lock := getReportSaveLock(clientUUID)
	lock.Lock()
	defer lock.Unlock()

	if err := ReportVerify(report); err != nil {
		return fmt.Errorf("failed to save Record: %w", err)
	}

	// 保存GPU详细记录到独立表
	currentTime := time.Now()
	if report.GPU != nil && len(report.GPU.DetailedInfo) > 0 {
		for idx, gpu := range report.GPU.DetailedInfo {
			gpuRecord := models.GPURecord{
				Client:      clientUUID,
				Time:        models.FromTime(currentTime),
				DeviceIndex: idx,
				DeviceName:  gpu.Name,
				MemTotal:    gpu.MemoryTotal,
				MemUsed:     gpu.MemoryUsed,
				Utilization: float32(gpu.Utilization),
				Temperature: gpu.Temperature,
			}
			if err := db.Create(&gpuRecord).Error; err != nil {
				return fmt.Errorf("failed to save GPU record: %w", err)
			}
		}
	}

	// 计算平均GPU使用率，用于向后兼容
	averageGPUUsage := float32(0)
	if report.GPU != nil && len(report.GPU.DetailedInfo) > 0 {
		averageGPUUsage = float32(report.GPU.AverageUsage)
	}

	Record := models.Record{
		Client:         clientUUID,
		Time:           models.FromTime(currentTime),
		Cpu:            float32(report.CPU.Usage),
		Gpu:            averageGPUUsage, // 使用平均GPU使用率
		Ram:            report.Ram.Used,
		RamTotal:       report.Ram.Total,
		Swap:           report.Swap.Used,
		SwapTotal:      report.Swap.Total,
		Load:           float32(report.Load.Load1), // 使用 Load1 作为主要负载指标
		Temp:           0,                          // Report 未提供 TEMP，设为 0（与原 nil 行为类似）
		Disk:           report.Disk.Used,
		DiskTotal:      report.Disk.Total,
		NetIn:          report.Network.Down,
		NetOut:         report.Network.Up,
		NetTotalUp:     report.Network.TotalUp,
		NetTotalDown:   report.Network.TotalDown,
		TrafficUp:      0,
		TrafficDown:    0,
		Process:        report.Process,
		Connections:    report.Connections.TCP,
		ConnectionsUdp: report.Connections.UDP,
		//Uptime:         report.Uptime,
	}

	// 使用事务确保 Record 和 ClientsInfo 一致性
	err = db.Transaction(func(tx *gorm.DB) error {
		previous, err := getLatestTrafficRecord(tx, clientUUID)
		if err != nil {
			return fmt.Errorf("failed to load previous Record: %w", err)
		}
		if previous != nil {
			Record.TrafficUp = utils.ComputeTrafficDelta(report.Network.TotalUp, previous.NetTotalUp)
			Record.TrafficDown = utils.ComputeTrafficDelta(report.Network.TotalDown, previous.NetTotalDown)
		}

		// 保存 Record
		if err := tx.Create(&Record).Error; err != nil {
			return fmt.Errorf("failed to save Record: %w", err)
		}
		return nil
	})

	if err != nil {
		return err
	}

	return nil
}

/*
// getString 从 map 中获取字符串
func getString(data map[string]interface{}, key string) string {
	if val, ok := data[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

// getInt 从 map 中获取整数
func getInt(data map[string]interface{}, key string) int {
	if val, ok := data[key]; ok {
		if num, ok := val.(float64); ok {
			return int(num)
		}
	}
	return 0
}

// getInt64 从 map 中获取 int64
func getInt64(data map[string]interface{}, key string) int64 {
	if val, ok := data[key]; ok {
		if num, ok := val.(float64); ok {
			return int64(num)
		}
	}
	return 0
}

// getFloat 从 map 中获取 float64
func getFloat(data map[string]interface{}, key string) float64 {
	if val, ok := data[key]; ok {
		if num, ok := val.(float64); ok {
			return num
		}
	}
	return 0.0
}
*/
