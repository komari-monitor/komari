package clients

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	v1 "github.com/komari-monitor/komari/protocol/v1"
)

const (
	maxReportDiskMounts          = 128
	maxReportDiskStringLength    = 4096
	maxReportDisksBytes          = 32 * 1024
	maxReportExtensionNamespaces = 32
	maxReportExtensionsBytes     = 32 * 1024
)

var reportExtensionNamespacePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,62}[a-z0-9])?$`)

func GetClientUUIDByToken(token string) (clientUUID string, err error) {
	db := dbcore.GetDBInstance()
	var client models.Client
	err = db.Where("token = ?", token).First(&client).Error
	if err != nil {
		return "", err
	}
	return client.UUID, nil
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
	if len(report.Disks) > maxReportDiskMounts {
		return fmt.Errorf("Disks must contain at most %d entries", maxReportDiskMounts)
	}
	if len(report.Disks) > 0 {
		encoded, err := json.Marshal(report.Disks)
		if err != nil {
			return fmt.Errorf("Disks must contain valid data: %w", err)
		}
		if len(encoded) > maxReportDisksBytes {
			return fmt.Errorf("Disks must not exceed %d bytes", maxReportDisksBytes)
		}
	}
	for i, disk := range report.Disks {
		prefix := fmt.Sprintf("Disks[%d]", i)
		if len(disk.Name) > maxReportDiskStringLength ||
			len(disk.Device) > maxReportDiskStringLength ||
			len(disk.Mountpoint) > maxReportDiskStringLength ||
			len(disk.Filesystem) > maxReportDiskStringLength {
			return fmt.Errorf("%s contains a string longer than %d bytes", prefix, maxReportDiskStringLength)
		}
		if err := checkInt64(prefix+".Used", disk.Used); err != nil {
			return err
		}
		if err := checkInt64(prefix+".Total", disk.Total); err != nil {
			return err
		}
	}
	if len(report.Extensions) > maxReportExtensionNamespaces {
		return fmt.Errorf("Extensions must contain at most %d namespaces", maxReportExtensionNamespaces)
	}
	if len(report.Extensions) > 0 {
		encoded, err := json.Marshal(report.Extensions)
		if err != nil {
			return fmt.Errorf("Extensions must contain valid JSON: %w", err)
		}
		if len(encoded) > maxReportExtensionsBytes {
			return fmt.Errorf("Extensions must not exceed %d bytes", maxReportExtensionsBytes)
		}
		for namespace, raw := range report.Extensions {
			if !reportExtensionNamespacePattern.MatchString(namespace) {
				return fmt.Errorf("Extensions namespace %q is invalid", namespace)
			}
			var object map[string]json.RawMessage
			if err := json.Unmarshal(raw, &object); err != nil || object == nil {
				return fmt.Errorf("Extensions namespace %q must contain a JSON object", namespace)
			}
		}
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
