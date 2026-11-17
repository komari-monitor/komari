package terminal

import (
	"encoding/json"

	"github.com/komari-monitor/komari/web/connection"
)

// 终端 WebSocket 的文本帧协议（本文件是唯一权威定义）。
//
// 一条 WebSocket 上同时跑两类数据，靠帧类型区分：
//   - 二进制帧 = 终端内容（PTY 输入输出），双向原样转发，从不解析
//   - 文本帧   = 控制消息，即下面这些 type
//
// 文本帧永远不会被当成终端内容写进 xterm，控制消息也不会污染终端缓冲。
//
// 一次终端会话的生命周期：
//
//	浏览器            服务端              被控端
//	发起          >  分配 request_id
//	              <  assigned            （回传 request_id，此时可断线重连）
//	                  请求开启终端        >  打开 PTY
//	              <  ready               （转发开始，终端内容随后流动）
//
// 意外中断（abort）：
//
//	任意一方断开，服务端向仍然在线的一方发送 abort，会话保留 5 分钟等待
//	该方用同一个 request_id 重连；重连成功后重新 ready，转发继续。
//	5 分钟内没等到，发送 terminate 并清理会话。
//
// 正常结束（terminate）：
//
//	任意一方发送 terminate，服务端转发给另一方后立即关闭会话。
//	terminate 是不可恢复的终态，收到的一方不再重连。
//
// 消息一览：
//
//	浏览器 → 服务端        heartbeat / resize / terminate
//	被控端 → 服务端        heartbeat / terminate
//	服务端 → 浏览器        assigned(request_id) / ready / heartbeat / abort / terminate
//	服务端 → 被控端        heartbeat / resize / abort / terminate
//
// abort 和 terminate 不带参数：协议里只有两方，收到 abort 的一方自然就是
// 掉线方向的反面，无需再用 reason 说明是谁断了。terminate 同理——它只表达
// "到此为止"，具体原因对收到的一方没有可执行的区别。
//
// 前向兼容：两端都必须忽略不认识的文本消息、把非 JSON 文本当作终端输出。
// 新增消息类型不需要任何兼容代码；旧客户端不认识新消息时只是忽略它。
const (
	// 任意方向
	controlTypeHeartbeat = "heartbeat"
	controlTypeTerminate = "terminate"
	// 浏览器 → 被控端（透传）
	controlTypeResize = "resize"

	// 服务端 → 浏览器
	controlTypeAssigned = "assigned"
	controlTypeReady    = "ready"
	controlTypeAbort    = "abort"
)

// controlFrame 是所有控制消息的统一结构。只有 assigned 带 request_id，
// 其余消息是纯类型标记。
type controlFrame struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id,omitempty"`
}

// parseControl 解析一帧文本消息。非 JSON 或没有 type 的帧返回 ok=false，
// 调用方据此把它当作终端内容处理。未知 type 也是 ok=true，由调用方忽略。
func parseControl(data []byte) (controlFrame, bool) {
	var frame controlFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		return controlFrame{}, false
	}
	if frame.Type == "" {
		return controlFrame{}, false
	}
	return frame, true
}

// writeControl 发出一条控制消息。nil 连接表示对端已经断开，直接忽略；
// 写入失败由对应的读循环发现并收尾。
func writeControl(conn *connection.SafeConn, frame controlFrame) {
	if conn == nil {
		return
	}
	_ = conn.WriteJSON(frame)
}

func notifyAssigned(conn *connection.SafeConn, requestID string) {
	writeControl(conn, controlFrame{Type: controlTypeAssigned, RequestID: requestID})
}

func notifyReady(conn *connection.SafeConn) {
	writeControl(conn, controlFrame{Type: controlTypeReady})
}

func notifyAbort(conn *connection.SafeConn) {
	writeControl(conn, controlFrame{Type: controlTypeAbort})
}

func notifyTerminate(conn *connection.SafeConn) {
	writeControl(conn, controlFrame{Type: controlTypeTerminate})
}
