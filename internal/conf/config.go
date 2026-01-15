package conf

import (
	"encoding/json"
	"fmt"
	"os"

	"log/slog"

	"github.com/gookit/event"
	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/internal/eventType"
)

// 返回默认配置对象
func Default() Config {
	return Config{
		Site: Site{
			Sitename:    "Komari",
			Description: "Komari Monitor, a simple server monitoring tool.",
			AllowCors:   false,
			Theme:       "default",
		},
		GeoIp: GeoIp{
			GeoIpEnabled:  true,
			GeoIpProvider: GeoIp_IPInfo,
		},
		Notification: Notification{
			NotificationEnabled:        true,
			TrafficLimitPercentage:     80.00,
			NotificationTemplate:       "{{emoji}}{{emoji}}{{emoji}}\n{{event}}\n{{client}}\n{{message}}\n{{time}}",
			NotificationMethod:         "empty",
			ExpireNotificationEnabled:  true,
			ExpireNotificationLeadDays: 7,
			LoginNotification:          true,
		},
		Login: Login{
			OAuthEnabled:  false,
			OAuthProvider: "github",
		},
		Record: Record{
			RecordEnabled:          true,
			RecordPreserveTime:     720,
			PingRecordPreserveTime: 24,
		},
		Database: Database{
			DatabaseType: "sqlite",
			DatabaseFile: "./data/komari.db",
		},
		Listen:     "0.0.0.0:25774",
		Extensions: map[string]interface{}{},
	}
}

// 强制覆盖当前内存中的配置并写入配置文件
func Override(cst Config) error {
	ensureExtensionsDefaults(&cst)

	b, err := json.MarshalIndent(cst, "", "  ")
	if err != nil {
		return err
	}

	oldConf := *Conf
	Conf = &cst

	err, _ = event.Trigger(eventType.ConfigUpdated, event.M{
		"old": oldConf,
		"new": cst,
	})
	if err != nil {
		// 撤回配置修改
		Conf = &oldConf
		b, _ = json.MarshalIndent(oldConf, "", "  ")

		//TODO: 循环引用，解耦dbcore与其他数据库模块
		//auditlog.EventLog("error", fmt.Sprintf("Configuration update reverted due to error in ConfigUpdated event: %v", err))
		slog.Error("Configuration update reverted due to error in ConfigUpdated event.", slog.Any("error", err))
	}
	if err := os.WriteFile(flags.ConfigFile, b, 0644); err != nil {
		return err
	}
	return err
}

// 保存部分配置更改到文件，会对V1的旧字段进行转换
func SavePartial(cst map[string]interface{}) error {
	// 将当前内存中的配置转换为通用 map，便于合并
	baseBytes, err := json.Marshal(Conf)
	if err != nil {
		return err
	}
	var base map[string]interface{}
	if err := json.Unmarshal(baseBytes, &base); err != nil {
		return err
	}

	// 兼容旧版扁平字段：把扁平键映射到新版分组结构
	normalized := normalizePartialMap(cst)

	// 深度合并（normalized 覆盖 base）
	merged := deepMerge(base, normalized)

	// 回写到强类型 Config
	mergedBytes, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	var newConf Config
	if err := json.Unmarshal(mergedBytes, &newConf); err != nil {
		return err
	}

	// 更新内存并落盘
	return Override(newConf)
}

func EditAndTrigger(fn func()) error {
	oldConf := *Conf
	fn()
	event.Trigger(eventType.ConfigUpdated, event.M{
		"old": oldConf,
		"new": *Conf,
	})
	return nil
}

// 保存完整配置到文件 同Override
func SaveFull(cst Config) error {
	return Override(cst)
}

// 以 v1 API 格式获取配置对象,使用 Conf 直接获取对象引用
func GetWithV1Format() (V1Struct, error) {
	return Conf.ToV1Format(), nil
}

// 以 v1 API 格式保存配置对象
func Save(cst V1Struct) error {
	cfg := cst.ToConfig()
	return Override(cfg)
}

// 语义等同于 SavePartial，保持对旧数据格式兼容
func Update(cst map[string]interface{}) error {
	// Update 的语义等同于 SavePartial，保持对旧数据格式兼容
	return SavePartial(cst)
}

// 将V1字段的输入映射为Config结构。
// 包含 site/login/... 等嵌套结构，则原样保留并与映射结果合并。
func normalizePartialMap(in map[string]interface{}) map[string]interface{} {
	if in == nil {
		return map[string]interface{}{}
	}

	// 先复制一份，避免修改入参
	out := make(map[string]interface{})
	for k, v := range in {
		out[k] = v
	}

	// 准备确保分组 map 存在
	ensureGroup := func(name string) map[string]interface{} {
		v, ok := out[name]
		if !ok || v == nil {
			m := map[string]interface{}{}
			out[name] = m
			return m
		}
		if m, ok := v.(map[string]interface{}); ok {
			return m
		}
		// 若类型非 map，则覆盖为 map
		m := map[string]interface{}{}
		out[name] = m
		return m
	}

	site := ensureGroup("site")
	login := ensureGroup("login")
	geo := ensureGroup("geo_ip")
	notif := ensureGroup("notification")
	record := ensureGroup("record")
	extensions := ensureGroup("extensions")

	// 扁平 -> 分组字段映射表
	move := func(flatKey string, group map[string]interface{}, groupKey string) {
		if v, ok := out[flatKey]; ok {
			group[groupKey] = v
			delete(out, flatKey)
		}
	}

	// Site
	move("sitename", site, "sitename")
	move("description", site, "description")
	move("allow_cors", site, "allow_cors")
	move("theme", site, "theme")
	move("private_site", site, "private_site")
	move("script_domain", site, "script_domain")
	move("send_ip_addr_to_guest", site, "send_ip_addr_to_guest")
	move("eula_accepted", site, "eula_accepted")
	move("custom_head", site, "custom_head")
	move("custom_body", site, "custom_body")

	// Login
	move("api_key", login, "api_key")
	move("auto_discovery_key", login, "auto_discovery_key")
	move("o_auth_enabled", login, "o_auth_enabled")
	move("o_auth_provider", login, "o_auth_provider")
	move("disable_password_login", login, "disable_password_login")

	// GeoIP
	move("geo_ip_enabled", geo, "geo_ip_enabled")
	move("geo_ip_provider", geo, "geo_ip_provider")

	// Notification
	move("notification_enabled", notif, "notification_enabled")
	move("notification_method", notif, "notification_method")
	move("notification_template", notif, "notification_template")
	move("expire_notification_enabled", notif, "expire_notification_enabled")
	move("expire_notification_lead_days", notif, "expire_notification_lead_days")
	move("login_notification", notif, "login_notification")
	move("traffic_limit_percentage", notif, "traffic_limit_percentage")

	// Record
	move("record_enabled", record, "record_enabled")
	move("record_preserve_time", record, "record_preserve_time")
	move("ping_record_preserve_time", record, "ping_record_preserve_time")

	// Nezha 兼容字段
	nezha := map[string]interface{}{}
	move("nezha_compat_enabled", nezha, "nezha_compat_enabled")
	move("nezha_compat_listen", nezha, "nezha_compat_listen")
	extensions["nezha"] = nezha

	return out
}

// = 以src更新dst，返回合并结果
func deepMerge(dst, src map[string]interface{}) map[string]interface{} {
	if dst == nil {
		dst = map[string]interface{}{}
	}
	for k, v := range src {
		if v == nil {
			// 忽略空覆盖，避免误删值
			continue
		}
		if dv, ok := dst[k]; ok {
			dm, dIsMap := dv.(map[string]interface{})
			sm, sIsMap := v.(map[string]interface{})
			if dIsMap && sIsMap {
				dst[k] = deepMerge(dm, sm)
				continue
			}
		}
		dst[k] = v
	}
	return dst
}

// FromEvent 从事件对象中提取旧配置和新配置 returns (old,new,error)。
func FromEvent(e event.Event) (Config, Config, error) {
	oldVal := e.Get("old")
	newVal := e.Get("new")

	oldConf, ok := oldVal.(Config)
	if !ok {
		return Config{}, Config{}, fmt.Errorf("FromEvent: 'old' key value is not of type Config. Got: %T", oldVal)
	}

	newConf, ok := newVal.(Config)
	if !ok {
		return Config{}, Config{}, fmt.Errorf("FromEvent: 'new' key value is not of type Config. Got: %T", newVal)
	}

	return oldConf, newConf, nil
}
