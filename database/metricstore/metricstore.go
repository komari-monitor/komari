package metricstore

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/config"
	"github.com/komari-monitor/komari/pkg/metric"
)

// 指标名称常量
const (
	MetricCPU            = "cpu.usage"
	MetricGPU            = "gpu.usage"
	MetricGPUMem         = "gpu.memory.used"
	MetricGPUMemTotal    = "gpu.memory.total"
	MetricGPUTemp        = "gpu.temperature"
	MetricRAM            = "memory.used"
	MetricRAMTotal       = "memory.total"
	MetricSwap           = "swap.used"
	MetricSwapTotal      = "swap.total"
	MetricLoad           = "load.average"
	MetricTemp           = "temperature"
	MetricDisk           = "disk.used"
	MetricDiskTotal      = "disk.total"
	MetricNetIn          = "net.in.rate"
	MetricNetOut         = "net.out.rate"
	MetricNetTotalUp     = "net.total.up"
	MetricNetTotalDown   = "net.total.down"
	MetricTrafficUp      = "traffic.up"
	MetricTrafficDown    = "traffic.down"
	MetricProcess        = "process.count"
	MetricConnections    = "connections.tcp"
	MetricConnectionsUDP = "connections.udp"
	MetricPingLatency    = "ping.latency_ms"
)

var (
	store     *metric.Store
	storeMu   sync.RWMutex
	storeOnce sync.Once
)

// MetricStoreConfig 保存 metric store 配置
type MetricStoreConfig struct {
	Enabled         bool   `json:"metric_store_enabled" default:"false"`          // 是否启用独立 metrics 数据库
	Driver          string `json:"metric_db_driver" default:"sqlite"`             // 数据库类型: sqlite, mysql, postgresql
	DSN             string `json:"metric_db_dsn" default:"./data/metrics.db"`    // 数据库连接串
	RetentionDays   int    `json:"metric_retention_days" default:"30"`            // 数据保留天数
	TablePrefix     string `json:"metric_table_prefix" default:"metric_"`         // 表名前缀
	MaxOpenConns    int    `json:"metric_max_open_conns" default:"25"`            // 最大连接数
	MaxIdleConns    int    `json:"metric_max_idle_conns" default:"5"`             // 最大空闲连接数
	MigrationStatus string `json:"metric_migration_status" default:"not_started"` // 迁移状态: not_started, in_progress, completed, failed
}

// MetricStoreConfigKeys 配置键
const (
	MetricStoreEnabledKey         = "metric_store_enabled"
	MetricDBDriverKey             = "metric_db_driver"
	MetricDBDSNKey                = "metric_db_dsn"
	MetricRetentionDaysKey        = "metric_retention_days"
	MetricTablePrefixKey          = "metric_table_prefix"
	MetricMaxOpenConnsKey         = "metric_max_open_conns"
	MetricMaxIdleConnsKey         = "metric_max_idle_conns"
	MetricMigrationStatusKey      = "metric_migration_status"
)

// InitializeStore 初始化 metric store（启动时调用）
func InitializeStore() error {
	var initErr error
	storeOnce.Do(func() {
		cfg, err := config.GetManyAs[MetricStoreConfig]()
		if err != nil {
			initErr = fmt.Errorf("failed to load metric store config: %w", err)
			return
		}

		if !cfg.Enabled {
			log.Println("Metric store is disabled, using legacy records storage")
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		driver := metric.Driver(cfg.Driver)
		if driver == "" {
			driver = metric.DriverSQLite
		}

		var metricCfg metric.Config
		switch driver {
		case metric.DriverSQLite:
			dsn := cfg.DSN
			if dsn == "" || dsn == "./data/metrics.db" {
				dsn = "file:./data/metrics.db?cache=shared&mode=rwc"
			}
			metricCfg = metric.SQLite(dsn,
				metric.WithTablePrefix(cfg.TablePrefix),
				metric.WithDefaultRetention(cfg.RetentionDays),
				metric.WithAutoMigrate(true),
				metric.WithMaxOpenConns(cfg.MaxOpenConns),
				metric.WithMaxIdleConns(cfg.MaxIdleConns),
			)
		case metric.DriverMySQL:
			metricCfg = metric.MySQL(cfg.DSN,
				metric.WithTablePrefix(cfg.TablePrefix),
				metric.WithDefaultRetention(cfg.RetentionDays),
				metric.WithAutoMigrate(true),
				metric.WithMaxOpenConns(cfg.MaxOpenConns),
				metric.WithMaxIdleConns(cfg.MaxIdleConns),
			)
		case metric.DriverPostgreSQL:
			metricCfg = metric.PostgreSQL(cfg.DSN,
				metric.WithTablePrefix(cfg.TablePrefix),
				metric.WithDefaultRetention(cfg.RetentionDays),
				metric.WithAutoMigrate(true),
				metric.WithMaxOpenConns(cfg.MaxOpenConns),
				metric.WithMaxIdleConns(cfg.MaxIdleConns),
			)
		default:
			initErr = fmt.Errorf("unsupported metric database driver: %s", cfg.Driver)
			return
		}

		s, err := metric.Open(ctx, metricCfg)
		if err != nil {
			initErr = fmt.Errorf("failed to open metric store: %w", err)
			return
		}

		// 创建所有指标定义
		if err := createMetricDefinitions(ctx, s); err != nil {
			s.Close()
			initErr = fmt.Errorf("failed to create metric definitions: %w", err)
			return
		}

		storeMu.Lock()
		store = s
		storeMu.Unlock()

		log.Printf("Metric store initialized successfully (driver=%s, retention=%d days)", cfg.Driver, cfg.RetentionDays)
	})

	return initErr
}

// GetStore 获取 metric store 实例（如果未启用返回 nil）
func GetStore() *metric.Store {
	storeMu.RLock()
	defer storeMu.RUnlock()
	return store
}

// IsEnabled 检查 metric store 是否已启用
func IsEnabled() bool {
	return GetStore() != nil
}

// CloseStore 关闭 metric store
func CloseStore() error {
	storeMu.Lock()
	defer storeMu.Unlock()

	if store != nil {
		err := store.Close()
		store = nil
		return err
	}
	return nil
}

// createMetricDefinitions 创建所有指标定义
func createMetricDefinitions(ctx context.Context, s *metric.Store) error {
	definitions := []metric.Definition{
		{Name: MetricCPU, Type: metric.TypeGauge, Unit: "%", Description: "CPU usage percentage"},
		{Name: MetricGPU, Type: metric.TypeGauge, Unit: "%", Description: "GPU usage percentage"},
		{Name: MetricGPUMem, Type: metric.TypeGauge, Unit: "bytes", Description: "GPU memory used"},
		{Name: MetricGPUMemTotal, Type: metric.TypeGauge, Unit: "bytes", Description: "GPU memory total"},
		{Name: MetricGPUTemp, Type: metric.TypeGauge, Unit: "°C", Description: "GPU temperature"},
		{Name: MetricRAM, Type: metric.TypeGauge, Unit: "bytes", Description: "RAM used"},
		{Name: MetricRAMTotal, Type: metric.TypeGauge, Unit: "bytes", Description: "RAM total"},
		{Name: MetricSwap, Type: metric.TypeGauge, Unit: "bytes", Description: "Swap used"},
		{Name: MetricSwapTotal, Type: metric.TypeGauge, Unit: "bytes", Description: "Swap total"},
		{Name: MetricLoad, Type: metric.TypeGauge, Unit: "", Description: "System load average"},
		{Name: MetricTemp, Type: metric.TypeGauge, Unit: "°C", Description: "System temperature"},
		{Name: MetricDisk, Type: metric.TypeGauge, Unit: "bytes", Description: "Disk used"},
		{Name: MetricDiskTotal, Type: metric.TypeGauge, Unit: "bytes", Description: "Disk total"},
		{Name: MetricNetIn, Type: metric.TypeGauge, Unit: "bytes/s", Description: "Network in rate"},
		{Name: MetricNetOut, Type: metric.TypeGauge, Unit: "bytes/s", Description: "Network out rate"},
		{Name: MetricNetTotalUp, Type: metric.TypeCounter, Unit: "bytes", Description: "Network total upload"},
		{Name: MetricNetTotalDown, Type: metric.TypeCounter, Unit: "bytes", Description: "Network total download"},
		{Name: MetricTrafficUp, Type: metric.TypeGauge, Unit: "bytes", Description: "Traffic upload delta"},
		{Name: MetricTrafficDown, Type: metric.TypeGauge, Unit: "bytes", Description: "Traffic download delta"},
		{Name: MetricProcess, Type: metric.TypeGauge, Unit: "count", Description: "Process count"},
		{Name: MetricConnections, Type: metric.TypeGauge, Unit: "count", Description: "TCP connections"},
		{Name: MetricConnectionsUDP, Type: metric.TypeGauge, Unit: "count", Description: "UDP connections"},
		{Name: MetricPingLatency, Type: metric.TypeGauge, Unit: "ms", Description: "Ping latency"},
	}

	for _, def := range definitions {
		if err := s.UpsertMetric(ctx, def); err != nil {
			return fmt.Errorf("failed to create metric %s: %w", def.Name, err)
		}
	}

	return nil
}

// WriteRecord 将 models.Record 写入 metric store
func WriteRecord(ctx context.Context, rec models.Record) error {
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}

	ts := rec.Time.ToTime()
	entityID := rec.Client

	points := []metric.Point{
		{MetricName: MetricCPU, EntityID: entityID, Timestamp: ts, Value: float64(rec.Cpu)},
		{MetricName: MetricGPU, EntityID: entityID, Timestamp: ts, Value: float64(rec.Gpu)},
		{MetricName: MetricRAM, EntityID: entityID, Timestamp: ts, Value: float64(rec.Ram)},
		{MetricName: MetricRAMTotal, EntityID: entityID, Timestamp: ts, Value: float64(rec.RamTotal)},
		{MetricName: MetricSwap, EntityID: entityID, Timestamp: ts, Value: float64(rec.Swap)},
		{MetricName: MetricSwapTotal, EntityID: entityID, Timestamp: ts, Value: float64(rec.SwapTotal)},
		{MetricName: MetricLoad, EntityID: entityID, Timestamp: ts, Value: float64(rec.Load)},
		{MetricName: MetricTemp, EntityID: entityID, Timestamp: ts, Value: float64(rec.Temp)},
		{MetricName: MetricDisk, EntityID: entityID, Timestamp: ts, Value: float64(rec.Disk)},
		{MetricName: MetricDiskTotal, EntityID: entityID, Timestamp: ts, Value: float64(rec.DiskTotal)},
		{MetricName: MetricNetIn, EntityID: entityID, Timestamp: ts, Value: float64(rec.NetIn)},
		{MetricName: MetricNetOut, EntityID: entityID, Timestamp: ts, Value: float64(rec.NetOut)},
		{MetricName: MetricNetTotalUp, EntityID: entityID, Timestamp: ts, Value: float64(rec.NetTotalUp)},
		{MetricName: MetricNetTotalDown, EntityID: entityID, Timestamp: ts, Value: float64(rec.NetTotalDown)},
		{MetricName: MetricTrafficUp, EntityID: entityID, Timestamp: ts, Value: float64(rec.TrafficUp)},
		{MetricName: MetricTrafficDown, EntityID: entityID, Timestamp: ts, Value: float64(rec.TrafficDown)},
		{MetricName: MetricProcess, EntityID: entityID, Timestamp: ts, Value: float64(rec.Process)},
		{MetricName: MetricConnections, EntityID: entityID, Timestamp: ts, Value: float64(rec.Connections)},
		{MetricName: MetricConnectionsUDP, EntityID: entityID, Timestamp: ts, Value: float64(rec.ConnectionsUdp)},
	}

	return s.WriteBatch(ctx, points)
}

// WriteGPURecord 将 models.GPURecord 写入 metric store
func WriteGPURecord(ctx context.Context, rec models.GPURecord) error {
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}

	ts := rec.Time.ToTime()
	entityID := rec.Client
	tags := map[string]string{
		"device_index": fmt.Sprintf("%d", rec.DeviceIndex),
		"device_name":  rec.DeviceName,
	}

	points := []metric.Point{
		{MetricName: MetricGPUMem, EntityID: entityID, Timestamp: ts, Value: float64(rec.MemUsed), Tags: tags},
		{MetricName: MetricGPUMemTotal, EntityID: entityID, Timestamp: ts, Value: float64(rec.MemTotal), Tags: tags},
		{MetricName: MetricGPU, EntityID: entityID, Timestamp: ts, Value: float64(rec.Utilization), Tags: tags},
		{MetricName: MetricGPUTemp, EntityID: entityID, Timestamp: ts, Value: float64(rec.Temperature), Tags: tags},
	}

	return s.WriteBatch(ctx, points)
}

// WritePingRecord 将 ping 记录写入 metric store
func WritePingRecord(ctx context.Context, rec models.PingRecord) error {
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}

	ts := rec.Time.ToTime()
	entityID := rec.Client
	tags := map[string]string{
		"task_id": fmt.Sprintf("%d", rec.TaskId),
	}

	point := metric.Point{
		MetricName: MetricPingLatency,
		EntityID:   entityID,
		Timestamp:  ts,
		Value:      float64(rec.Value),
		Tags:       tags,
	}

	return s.Write(ctx, point)
}

// GetRecordsByClientAndTime 从 metric store 查询记录并重构为 models.Record
func GetRecordsByClientAndTime(ctx context.Context, clientUUID string, start, end time.Time) ([]models.Record, error) {
	s := GetStore()
	if s == nil {
		return nil, fmt.Errorf("metric store not enabled")
	}

	// 查询所有相关指标
	metricNames := []string{
		MetricCPU, MetricGPU, MetricRAM, MetricRAMTotal, MetricSwap, MetricSwapTotal,
		MetricLoad, MetricTemp, MetricDisk, MetricDiskTotal, MetricNetIn, MetricNetOut,
		MetricNetTotalUp, MetricNetTotalDown, MetricTrafficUp, MetricTrafficDown,
		MetricProcess, MetricConnections, MetricConnectionsUDP,
	}

	// 按时间戳组织数据
	recordMap := make(map[int64]*models.Record)

	for _, metricName := range metricNames {
		points, err := s.Query(ctx, metric.Query{
			MetricName: metricName,
			EntityID:   clientUUID,
			Start:      start,
			End:        end,
			Order:      metric.OrderAsc,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to query metric %s: %w", metricName, err)
		}

		for _, p := range points {
			tsKey := p.Timestamp.Unix()
			if recordMap[tsKey] == nil {
				recordMap[tsKey] = &models.Record{
					Client: clientUUID,
					Time:   models.FromTime(p.Timestamp),
				}
			}
			rec := recordMap[tsKey]

			switch metricName {
			case MetricCPU:
				rec.Cpu = float32(p.Value)
			case MetricGPU:
				rec.Gpu = float32(p.Value)
			case MetricRAM:
				rec.Ram = int64(p.Value)
			case MetricRAMTotal:
				rec.RamTotal = int64(p.Value)
			case MetricSwap:
				rec.Swap = int64(p.Value)
			case MetricSwapTotal:
				rec.SwapTotal = int64(p.Value)
			case MetricLoad:
				rec.Load = float32(p.Value)
			case MetricTemp:
				rec.Temp = float32(p.Value)
			case MetricDisk:
				rec.Disk = int64(p.Value)
			case MetricDiskTotal:
				rec.DiskTotal = int64(p.Value)
			case MetricNetIn:
				rec.NetIn = int64(p.Value)
			case MetricNetOut:
				rec.NetOut = int64(p.Value)
			case MetricNetTotalUp:
				rec.NetTotalUp = int64(p.Value)
			case MetricNetTotalDown:
				rec.NetTotalDown = int64(p.Value)
			case MetricTrafficUp:
				rec.TrafficUp = int64(p.Value)
			case MetricTrafficDown:
				rec.TrafficDown = int64(p.Value)
			case MetricProcess:
				rec.Process = int(p.Value)
			case MetricConnections:
				rec.Connections = int(p.Value)
			case MetricConnectionsUDP:
				rec.ConnectionsUdp = int(p.Value)
			}
		}
	}

	// 转换为切片并排序
	records := make([]models.Record, 0, len(recordMap))
	for _, rec := range recordMap {
		records = append(records, *rec)
	}

	// 按时间排序
	for i := 0; i < len(records)-1; i++ {
		for j := i + 1; j < len(records); j++ {
			if records[i].Time.ToTime().After(records[j].Time.ToTime()) {
				records[i], records[j] = records[j], records[i]
			}
		}
	}

	return records, nil
}

// GetGPURecordsByClientAndTime 从 metric store 查询 GPU 记录
func GetGPURecordsByClientAndTime(ctx context.Context, clientUUID string, start, end time.Time) ([]models.GPURecord, error) {
	s := GetStore()
	if s == nil {
		return nil, fmt.Errorf("metric store not enabled")
	}

	// 查询 GPU 相关指标
	gpuMetrics := []string{MetricGPU, MetricGPUMem, MetricGPUMemTotal, MetricGPUTemp}

	// 按设备索引和时间组织数据
	type gpuKey struct {
		deviceIndex int
		timestamp   int64
	}
	recordMap := make(map[gpuKey]*models.GPURecord)

	for _, metricName := range gpuMetrics {
		points, err := s.Query(ctx, metric.Query{
			MetricName: metricName,
			EntityID:   clientUUID,
			Start:      start,
			End:        end,
			Order:      metric.OrderAsc,
		})
		if err != nil {
			continue // GPU 数据可能不存在
		}

		for _, p := range points {
			deviceIndex := 0
			deviceName := ""
			if idx, ok := p.Tags["device_index"]; ok {
				fmt.Sscanf(idx, "%d", &deviceIndex)
			}
			if name, ok := p.Tags["device_name"]; ok {
				deviceName = name
			}

			key := gpuKey{deviceIndex: deviceIndex, timestamp: p.Timestamp.Unix()}
			if recordMap[key] == nil {
				recordMap[key] = &models.GPURecord{
					Client:      clientUUID,
					Time:        models.FromTime(p.Timestamp),
					DeviceIndex: deviceIndex,
					DeviceName:  deviceName,
				}
			}
			rec := recordMap[key]

			switch metricName {
			case MetricGPU:
				rec.Utilization = float32(p.Value)
			case MetricGPUMem:
				rec.MemUsed = int64(p.Value)
			case MetricGPUMemTotal:
				rec.MemTotal = int64(p.Value)
			case MetricGPUTemp:
				rec.Temperature = int(p.Value)
			}
		}
	}

	// 转换为切片
	records := make([]models.GPURecord, 0, len(recordMap))
	for _, rec := range recordMap {
		records = append(records, *rec)
	}

	return records, nil
}

// GetPingRecords 从 metric store 查询 ping 记录
func GetPingRecords(ctx context.Context, clientUUID string, taskID int, start, end time.Time) ([]models.PingRecord, error) {
	s := GetStore()
	if s == nil {
		return nil, fmt.Errorf("metric store not enabled")
	}

	query := metric.Query{
		MetricName: MetricPingLatency,
		Start:      start,
		End:        end,
		Order:      metric.OrderDesc,
	}

	if clientUUID != "" {
		query.EntityID = clientUUID
	}

	if taskID >= 0 {
		query.Tags = map[string]string{"task_id": fmt.Sprintf("%d", taskID)}
	}

	points, err := s.Query(ctx, query)
	if err != nil {
		return nil, err
	}

	records := make([]models.PingRecord, 0, len(points))
	for _, p := range points {
		taskIDVal := uint(0)
		if tid, ok := p.Tags["task_id"]; ok {
			var t uint64
			fmt.Sscanf(tid, "%d", &t)
			taskIDVal = uint(t)
		}

		records = append(records, models.PingRecord{
			Client: p.EntityID,
			TaskId: taskIDVal,
			Time:   models.FromTime(p.Timestamp),
			Value:  int(p.Value),
		})
	}

	return records, nil
}

// DeleteAllRecords 删除所有记录（保留指标定义）
func DeleteAllRecords(ctx context.Context) error {
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}

	// 删除所有数据指标（但保留定义）
	metricNames := []string{
		MetricCPU, MetricGPU, MetricRAM, MetricRAMTotal, MetricSwap, MetricSwapTotal,
		MetricLoad, MetricTemp, MetricDisk, MetricDiskTotal, MetricNetIn, MetricNetOut,
		MetricNetTotalUp, MetricNetTotalDown, MetricTrafficUp, MetricTrafficDown,
		MetricProcess, MetricConnections, MetricConnectionsUDP,
		MetricGPUMem, MetricGPUMemTotal, MetricGPUTemp, MetricPingLatency,
	}

	for _, metricName := range metricNames {
		if _, err := s.DeleteBefore(ctx, metricName, time.Now().Add(24*365*time.Hour)); err != nil {
			log.Printf("Failed to delete metric %s: %v", metricName, err)
		}
	}

	return nil
}

// DeleteRecordsBefore 删除指定时间之前的记录
func DeleteRecordsBefore(ctx context.Context, before time.Time) error {
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}

	metricNames := []string{
		MetricCPU, MetricGPU, MetricRAM, MetricRAMTotal, MetricSwap, MetricSwapTotal,
		MetricLoad, MetricTemp, MetricDisk, MetricDiskTotal, MetricNetIn, MetricNetOut,
		MetricNetTotalUp, MetricNetTotalDown, MetricTrafficUp, MetricTrafficDown,
		MetricProcess, MetricConnections, MetricConnectionsUDP,
		MetricGPUMem, MetricGPUMemTotal, MetricGPUTemp, MetricPingLatency,
	}

	for _, metricName := range metricNames {
		if _, err := s.DeleteBefore(ctx, metricName, before); err != nil {
			log.Printf("Failed to delete old metric %s: %v", metricName, err)
		}
	}

	return nil
}
