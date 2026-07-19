package dbcore

import (
	"archive/zip"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/config"
	"github.com/komari-monitor/komari/pkg/migrations"
	logutil "github.com/komari-monitor/komari/utils/log"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// ZipDirectoryExcluding 将 srcDir 打包为 dstZip，exclude 是绝对路径集合需要排除。
// 目录项被排除时使用 SkipDir 跳过整个子树。
func ZipDirectoryExcluding(srcDir, dstZip string, exclude map[string]struct{}) error {
	// 规范化排除路径为绝对路径
	normExclude := make(map[string]struct{}, len(exclude))
	for p := range exclude {
		abs, _ := filepath.Abs(p)
		normExclude[abs] = struct{}{}
	}

	out, err := os.Create(dstZip)
	if err != nil {
		return err
	}
	defer out.Close()

	zw := zip.NewWriter(out)
	defer zw.Close()

	absSrc, _ := filepath.Abs(srcDir)
	walkErr := filepath.Walk(absSrc, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// 排除 backup.zip 本身
		if _, ok := normExclude[path]; ok {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// 计算 zip 内相对路径
		rel, err := filepath.Rel(absSrc, path)
		if err != nil {
			return err
		}
		// 根目录跳过
		if rel == "." {
			return nil
		}
		// 替换为正斜杠
		zipName := filepath.ToSlash(rel)

		if info.IsDir() {
			_, err := zw.Create(zipName + "/")
			return err
		}
		// 普通文件
		fh, err := os.Open(path)
		if err != nil {
			return err
		}
		w, err := zw.Create(zipName)
		if err != nil {
			fh.Close()
			return err
		}
		if _, err := io.Copy(w, fh); err != nil {
			fh.Close()
			return err
		}
		fh.Close()
		return nil
	})
	if walkErr != nil {
		return walkErr
	}
	return zw.Close()
}

// removeAllInDirExcept 删除 dir 下除 exclude 指定绝对路径外的所有文件和文件夹
func removeAllInDirExcept(dir string, exclude map[string]struct{}) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	normExclude := make(map[string]struct{}, len(exclude))
	for p := range exclude {
		abs, _ := filepath.Abs(p)
		normExclude[abs] = struct{}{}
	}
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		full := filepath.Join(absDir, e.Name())
		if _, ok := normExclude[full]; ok {
			continue
		}
		if err := os.RemoveAll(full); err != nil {
			return err
		}
	}
	return nil
}

// unzipToDir 将 zipPath 解压到 dstDir，包含路径遍历保护
func unzipToDir(zipPath, dstDir string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()

	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return err
	}
	absDst, _ := filepath.Abs(dstDir)

	for _, f := range zr.File {
		// 构造目标路径并做路径遍历保护
		cleanName := filepath.Clean(f.Name)
		targetPath := filepath.Join(absDst, cleanName)
		if !strings.HasPrefix(targetPath, absDst+string(os.PathSeparator)) && targetPath != absDst {
			return fmt.Errorf("illegal file path in zip: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(targetPath)
		if err != nil {
			rc.Close()
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			out.Close()
			rc.Close()
			return err
		}
		out.Close()
		rc.Close()
	}
	return nil
}

var (
	instance *gorm.DB
	once     sync.Once
	initErr  error
)

// SystemVersionKey 是记录“上次启动所用版本标识”的配置键（存于 configs 表）。
// 取代旧的 ./data/.komari-version 文件：版本标识随配置库一起备份/恢复，
// 也避免额外的裸文件依赖。
const SystemVersionKey = "system_version"

// versionID 是当前构建的版本标识，由 SetVersionID 在 Initialize 前注入。
var versionID string

// dbFileExistedAtStartup 记录本次进程启动、打开数据库之前 komari.db 是否已存在，
// 用于区分“全新安装”与“从旧版升级（无版本标记）”。在 doInitialize 打开数据库
// 之前采集。
var dbFileExistedAtStartup bool

// SetVersionID 设置当前构建的版本标识（通常为 CurrentVersion+"-"+VersionHash），
// 用于版本升级检测与自动备份。应在 Initialize() 之前调用；为空则跳过升级备份。
func SetVersionID(id string) {
	versionID = id
}

// resolveDatabaseFile 返回当前使用的 SQLite 数据库文件路径。
func resolveDatabaseFile() string {
	dbFile := flags.DatabaseFile
	if dbFile == "" {
		dbFile = "./data/komari.db"
	}
	return dbFile
}

// backupOnVersionUpgrade 在检测到版本升级时，把当前 ./data 打包到
// ./data/backup/upgrade-{time}.zip，便于升级（含 metrics 迁移）异常时回滚。
//
// 版本标识存放于配置库（configs 表，键 system_version），因此本函数必须在
// config.SetDb 之后、一次性 metrics 迁移（InitStores）之前调用。
//
// 触发规则：
//   - versionID 为空：跳过（未注入版本，如部分测试场景）。
//   - 配置中无版本且启动前无数据库文件：全新安装，仅写版本，不备份。
//   - 配置中无版本但启动前已有数据库文件：从无版本标记的旧稳定版升级，备份。
//   - 配置中版本与当前不同：版本升级，备份。
//   - 配置中版本与当前一致：无需备份。
//
// 备份失败不阻止启动，但打印明确错误；备份成功（或无需备份）后写入/更新版本。
func backupOnVersionUpgrade() {
	if versionID == "" {
		return
	}

	prevVersion, readErr := config.GetAs[string](SystemVersionKey)
	prevVersion = strings.TrimSpace(prevVersion)
	versionRecorded := readErr == nil && prevVersion != ""

	// 版本未变化，无需备份。
	if versionRecorded && prevVersion == versionID {
		return
	}

	// 全新安装：配置中无版本且启动前无数据库文件，直接写版本不备份。
	if !versionRecorded && !dbFileExistedAtStartup {
		writeVersionMarker()
		return
	}

	// 需要备份（升级或从旧稳定版首次带版本标记启动）。
	// 先做一次 WAL checkpoint，确保 komari.db 主文件包含最新数据，
	// 避免备份出的库缺少仍留在 -wal 中的写入。
	if instance != nil {
		instance.Exec("PRAGMA wal_checkpoint(TRUNCATE);")
	}

	backupDir := filepath.Join(".", "data", "backup")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		log.Printf("[upgrade-backup] failed to create backup dir: %v", err)
		return
	}
	tsName := time.Now().UTC().Format("20060102-150405")
	bakPath := filepath.Join(backupDir, fmt.Sprintf("upgrade-%s.zip", tsName))
	backupZipPath := filepath.Join(".", "data", "backup", "upload", "backup.zip")
	exclude := map[string]struct{}{
		backupZipPath: {}, // 已由 backupDir 的 SkipDir 覆盖，保留以显式声明意图
		backupDir:     {}, // 避免把历史归档打包进升级备份
	}
	if zipErr := ZipDirectoryExcluding("./data", bakPath, exclude); zipErr != nil {
		log.Printf("[upgrade-backup] failed to backup ./data before upgrade (from %q to %q): %v", prevVersion, versionID, zipErr)
		return
	}
	log.Printf("[upgrade-backup] ./data backed up to %s before upgrade (from %q to %q)", bakPath, prevVersion, versionID)

	writeVersionMarker()
}

// writeVersionMarker 将当前 versionID 写入配置库。
func writeVersionMarker() {
	if err := config.Set(SystemVersionKey, versionID); err != nil {
		log.Printf("[upgrade-backup] failed to persist version marker: %v", err)
	}
}

func buildSQLiteDSN(databaseFile string) string {
	if databaseFile == "" {
		databaseFile = "./data/komari.db"
	}

	params := "_busy_timeout=5000&_txlock=immediate&_journal_mode=WAL&_synchronous=NORMAL"
	separator := "?"
	if strings.Contains(databaseFile, "?") {
		separator = "&"
	}

	if strings.HasPrefix(databaseFile, "file:") {
		return databaseFile + separator + params
	}

	if databaseFile == ":memory:" {
		return "file::memory:?cache=shared&" + params
	}

	return "file:" + filepath.ToSlash(databaseFile) + separator + params
}

// Initialize 显式初始化数据库连接与表结构，仅执行一次。
// 与 GetDBInstance 不同，Initialize 返回错误而非直接退出进程，
// 便于启动生命周期统一处理错误、以及在测试/CLI 命令中做隔离。
func Initialize() error {
	once.Do(func() {
		initErr = doInitialize()
	})
	return initErr
}

// GetDBInstance 返回全局数据库实例。
// 为兼容既有大量调用点，这里保留“出错即退出”的语义；
// 需要错误处理的启动流程应优先调用 Initialize()。
func GetDBInstance() *gorm.DB {
	if err := Initialize(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	return instance
}

// Close 关闭底层数据库连接，供关闭流程调用。
func Close() error {
	if instance == nil {
		return nil
	}
	sqlDB, err := instance.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func doInitialize() error {
	var err error

	// 在数据库初始化前执行：如果存在 ./data/backup/upload/backup.zip，则进行恢复。
	// 恢复标记由上传接口写入，恢复后自动删除，不会在每次启动时误触发。
	//
	// 流程：
	//   1. 检测 ./data/backup/upload/backup.zip
	//   2. 创建 pre-restore 备份（当前数据 → ./data/backup/pre-restore-{time}.zip）
	//   3. 确认 pre-restore 创建成功
	//   4. 清空 data/ 并解压 backup.zip
	//   5. 删除 backup.zip 和标记文件
	func() {
		backupZipPath := filepath.Join(".", "data", "backup", "upload", "backup.zip")
		backupDir := filepath.Join(".", "data", "backup")

		if _, statErr := os.Stat(backupZipPath); statErr != nil {
			return
		}

		// 1) 创建恢复前备份
		if err := os.MkdirAll(backupDir, 0755); err != nil {
			log.Printf("[restore] failed to create backup dir: %v", err)
			return
		}

		tsName := time.Now().UTC().Format("20060102-150405")
		bakPath := filepath.Join(backupDir, fmt.Sprintf("pre-restore-%s.zip", tsName))
		if zipErr := ZipDirectoryExcluding("./data", bakPath, map[string]struct{}{
			backupDir: {}, // 避免把整个 backup/ 归档打包进 pre-restore
		}); zipErr != nil {
			log.Printf("[restore] failed to create pre-restore backup: %v", zipErr)
			return
		}

		// 确认 pre-restore 文件存在且非空
		info, statErr := os.Stat(bakPath)
		if statErr != nil || info.Size() == 0 {
			log.Printf("[restore] pre-restore backup verification failed: stat=%v", statErr)
			return
		}
		log.Printf("[restore] pre-restore backup created: %s (%d bytes)", bakPath, info.Size())

		// 2) 清空 data/（排除 backup/ 归档目录）
		if delErr := removeAllInDirExcept("./data", map[string]struct{}{
			backupDir: {},
		}); delErr != nil {
			log.Printf("[restore] failed to cleanup data dir: %v", delErr)
			return
		}

		// 3) 解压 backup.zip 到 ./data
		if unzipErr := unzipToDir(backupZipPath, "./data"); unzipErr != nil {
			log.Printf("[restore] failed to unzip backup zip: %v", unzipErr)
			return
		}
		log.Printf("[restore] backup extracted to ./data")

		// 4) 清理
		if rmErr := os.Remove(backupZipPath); rmErr != nil {
			log.Printf("[restore] failed to remove backup.zip: %v", rmErr)
		}
		if rmErr := os.Remove("./data/komari-backup-markup"); rmErr != nil {
			log.Printf("[restore] failed to remove markup: %v", rmErr)
		}
	}()

	// 记录“打开数据库之前”komari.db 是否已存在，用于区分全新安装与旧版升级。
	// 必须在（可能的）恢复逻辑之后、gorm.Open 之前采集：恢复会解压出旧库，
	// gorm.Open 会创建空库。
	if _, statErr := os.Stat(resolveDatabaseFile()); statErr == nil {
		dbFileExistedAtStartup = true
	}

	logConfig := &gorm.Config{
		Logger:  logutil.NewGormLogger(),
		NowFunc: func() time.Time { return time.Now().UTC() },
	}

	// 根据数据库类型选择不同的连接方式
	switch flags.ApplyDatabaseTypeNormalization() {
	case flags.DatabaseTypeSQLite:
		// SQLite 连接
		// 通过 DSN 传入 _busy_timeout / _txlock 等参数，确保连接池中的每一条连接
		// 都生效：
		//   - _busy_timeout=5000：遇到写锁时最多等待 5s 再返回，避免瞬时
		//     "database is locked" 直接失败（仅靠后续 PRAGMA Exec 只作用于
		//     当时执行该语句的单条连接，池内其它连接不生效）。
		//   - _txlock=immediate：事务一开始即获取写锁，避免「先 SELECT 后写」
		//     的锁升级在并发写入下产生死锁式的立即 SQLITE_BUSY。
		//   - _journal_mode=WAL / _synchronous=NORMAL：与下方 PRAGMA 保持一致，
		//     在 DSN 层为所有连接预设。
		dsn := buildSQLiteDSN(flags.DatabaseFile)
		instance, err = gorm.Open(sqlite.Open(dsn), logConfig)
		if err != nil {
			return fmt.Errorf("failed to connect to SQLite3 database: %w", err)
		}
		log.Printf("Using SQLite database file: %s", flags.DatabaseFile)
		if sqlDB, dbErr := instance.DB(); dbErr == nil {
			// SQLite 同一时刻只允许一个写者；限制连接数可避免连接池层面的写写竞争。
			// 负载历史每分钟会执行包含读和写的事务，若连接池允许多个连接，容易与
			// ping 结果等短写入撞锁并导致整批负载记录回滚。
			sqlDB.SetMaxOpenConns(1)
			sqlDB.SetMaxIdleConns(1)
			sqlDB.SetConnMaxLifetime(0)
		} else {
			log.Printf("Failed to access underlying sql.DB for SQLite tuning: %v", dbErr)
		}
		instance.Exec("PRAGMA wal = ON;")
		if err := instance.Exec("PRAGMA journal_mode = WAL;").Error; err != nil {
			log.Printf("Failed to enable WAL mode for SQLite: %v", err)
		}
		instance.Exec("PRAGMA synchronous = NORMAL;")
		instance.Exec("PRAGMA busy_timeout = 5000;")
		instance.Exec("PRAGMA cache_size = -65536;")
		instance.Exec("PRAGMA temp_store = MEMORY;")
		instance.Exec("PRAGMA wal_checkpoint(TRUNCATE);")
	default:
		return fmt.Errorf("unsupported database type: %s (supported: %s)", flags.DatabaseType, flags.SupportedDatabaseTypes())
	}
	if err := migrations.Run(migrations.Context{DB: instance}); err != nil {
		return fmt.Errorf("failed to run startup migrations: %w", err)
	}
	config.SetDb(instance)

	// 配置库就绪后、执行后续 AutoMigrate 与一次性 metrics 迁移之前：
	// 基于配置中的版本标记检测升级并自动备份 ./data，便于回滚。
	backupOnVersionUpgrade()

	// 自动迁移模型
	//
	// 注意：负载/GPU/ping 历史监控数据运行期全部走 metric store（默认 SQLite
	// ./data/metrics.db，或配置的 MySQL/PostgreSQL）。旧的 records /
	// records_long_term / gpu_records / ping_records 表不再建表、不再写入。
	// 若升级时旧表仍存在，会在 pkg/migrations.RunMetricStoreMigrations 中先导入再清理。
	// models.Record / models.PingRecord / models.GPURecord 结构体仍作为
	// metric store 的读写 DTO 和旧表导入 DTO 保留在 models 包中。

	err = instance.AutoMigrate(
		&models.User{},
		&models.Client{},
		&models.Log{},
		&models.Clipboard{},
		&models.LoadNotification{},
		&models.OfflineNotification{},
		&models.TrafficReportNotification{},
		&models.PingTask{},
		&models.OidcProvider{},
		&models.MessageSenderProvider{},
		&models.ThemeConfiguration{},
	)
	if err != nil {
		return fmt.Errorf("failed to create tables: %w", err)
	}
	if err := instance.AutoMigrate(
		&models.Session{},
	); err != nil {
		log.Printf("Failed to create Session table, it may already exist: %v", err)
	}
	if err := instance.AutoMigrate(
		&models.Task{},
		&models.TaskResult{},
	); err != nil {
		log.Printf("Failed to create Task and TaskResult table, it may already exist: %v", err)
	}

	return nil
}
