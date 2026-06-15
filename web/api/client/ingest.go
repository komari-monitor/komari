package client

import (
	"time"

	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/database/tasks"
	v1 "github.com/komari-monitor/komari/protocol/v1"
	agent_runtime "github.com/komari-monitor/komari/web/agent"
)

// ingest.go
// agent 上报数据的传输无关处理逻辑。v1 (REST/WS) 与 v2 (JSON-RPC) 两套上报入口
// 经过各自的协议解析后，统一调用这里的函数落库并更新运行时状态，消除重复。

// ingestReport 保存一次负载上报并刷新运行时状态。
// protocolVersion 标记上报所用协议（1 或 2），用于运行时区分客户端能力。
// markPresence 为 true 时按 POST 上报会话刷新在线状态（WS 连接自行管理在线状态，应传 false）。
func ingestReport(uuid string, report v1.Report, protocolVersion int, markPresence bool) error {
	report.UUID = uuid
	savedReport, err := SaveClientReport(uuid, report)
	if err != nil {
		return err
	}
	agent_runtime.SetLatestReport(uuid, &savedReport)
	agent_runtime.SetClientProtocolVersion(uuid, protocolVersion)
	if markPresence {
		refreshPostPresence(uuid)
	}
	return nil
}

// ingestBasicInfo 保存客户端基础信息。fallbackIP 在上报未携带 IP 时用作兜底。
func ingestBasicInfo(uuid string, info map[string]interface{}, fallbackIP string) error {
	if info == nil {
		info = map[string]interface{}{}
	}
	return saveClientBasicInfo(info, uuid, fallbackIP)
}

// ingestPingResult 保存一条 ping 探测结果。
func ingestPingResult(uuid string, taskID uint, value int, finishedAt time.Time) {
	tasks.SavePingRecord(models.PingRecord{
		Client: uuid,
		TaskId: taskID,
		Value:  value,
		Time:   models.FromTime(finishedAt),
	})
}
