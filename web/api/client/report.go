package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/komari-monitor/komari/database/clients"
	v1 "github.com/komari-monitor/komari/protocol/v1"
	"github.com/komari-monitor/komari/utils/notifier"
	agent_runtime "github.com/komari-monitor/komari/web/agent"
	"github.com/komari-monitor/komari/web/api"
	"github.com/komari-monitor/komari/web/connection"
	report_cache "github.com/komari-monitor/komari/web/report"
)

const (
	// 如果超过这个时间没有收到任何消息，则认为连接已死
	// 因为目前server没有存agent的信息上报间隔。只有写一个默认的
	readWait        = 11 * time.Second
	postPresenceTTL = 35 * time.Second
)

// postPresenceEntry 保存单个客户端的 POST 上报会话状态
type postPresenceEntry struct {
	connID     int64
	timer      *time.Timer
	generation uint64 // 每次 Reset 递增，用于回调中判断是否为过期的旧回调
}

var (
	postPresenceMu     sync.Mutex
	postPresenceStates = make(map[string]*postPresenceEntry)
)

// refreshPostPresence 管理 HTTP POST 上报者的在线/离线状态。
// 每次 POST 刷新 TTL 定时器；定时器到期后触发离线通知。
func refreshPostPresence(uuid string) {
	postPresenceMu.Lock()
	defer postPresenceMu.Unlock()

	if entry, exists := postPresenceStates[uuid]; exists {
		// 已在线：递增 generation 使可能正在执行的旧回调失效
		entry.generation++
		entry.timer.Stop()
		// 重新创建 AfterFunc 以在闭包中捕获新的 generation
		gen := entry.generation
		entry.timer = time.AfterFunc(postPresenceTTL, func() {
			postPresenceExpired(uuid, entry.connID, gen)
		})
		agent_runtime.KeepAlivePresence(uuid, entry.connID, postPresenceTTL)
		return
	}

	// 新 POST 会话：生成 connID，标记在线，启动超时定时器
	connID := time.Now().UnixNano()
	agent_runtime.KeepAlivePresence(uuid, connID, postPresenceTTL)
	agent_runtime.SetClientProtocolVersion(uuid, 1)
	go notifier.OnlineNotification(uuid, connID)

	defaultGeneration := uint64(0)

	entry := &postPresenceEntry{
		connID:     connID,
		generation: defaultGeneration,
	}

	entry.timer = time.AfterFunc(postPresenceTTL, func() {
		postPresenceExpired(uuid, connID, defaultGeneration)
	})

	postPresenceStates[uuid] = entry
}

// postPresenceExpired 是定时器到期的回调。
// 只有当 connID 和 generation 都与当前 entry 匹配时才执行离线清理，
// 避免 timer.Reset 竞态导致过期回调错误地清除仍活跃的会话。
func postPresenceExpired(uuid string, connID int64, gen uint64) {
	postPresenceMu.Lock()
	e, ok := postPresenceStates[uuid]
	if !ok || e.connID != connID || e.generation != gen {
		postPresenceMu.Unlock()
		return
	}
	delete(postPresenceStates, uuid)
	postPresenceMu.Unlock()

	agent_runtime.SetPresence(uuid, connID, false)
	notifier.OfflineNotification(uuid, connID)
}

func UploadReport(c *gin.Context) {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Println("Failed to read request body:", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	var data map[string]interface{}
	err = json.Unmarshal(bodyBytes, &data)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}
	// Save report to database
	var report v1.Report
	err = json.Unmarshal(bodyBytes, &report)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}
	// 优先使用 body 中的 UUID，若为空则从中间件注入的上下文中获取
	uuid := report.UUID
	if uuid == "" {
		if v, ok := c.Get("client_uuid"); ok {
			uuid, _ = v.(string)
		}
	}
	if uuid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "UUID is required"})
		return
	}

	// POST 上报：落库、更新运行时状态并刷新在线状态
	if err := ingestReport(uuid, report, 1, true); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("%v", err)})
		return
	}

	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes)) // Restore the body for further use
	c.JSON(200, gin.H{"status": "success"})
}

func WebSocketReport(c *gin.Context) {
	// 升级ws
	if !api.IsWebSocketUpgrade(c) {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "Require WebSocket upgrade"})
		return
	}
	// Upgrade the HTTP connection to a WebSocket connection
	unsafeConn, err := api.UpgradeWebSocket(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "Failed to upgrade to WebSocket." + err.Error()})
		return
	}
	conn := connection.NewSafeConn(unsafeConn)
	defer conn.Close()

	_, message, err := conn.ReadMessage()
	if err != nil {
		log.Println("Error reading message:", err)
		return
	}

	// 第一次数据拿token
	data := map[string]interface{}{}
	err = json.Unmarshal(message, &data)
	if err != nil {
		conn.WriteJSON(gin.H{"status": "error", "error": "Invalid JSON"})
		return
	}
	// it should ok,token was verfied in the middleware
	token := ""
	var errMsg string

	// 优先检查查询参数中的 token
	token = c.Query("token")

	// 如果 token 为空，返回错误
	if token == "" {
		conn.WriteJSON(gin.H{"status": "error", "error": errMsg})
		return
	}

	uuid, err := clients.GetClientUUIDByToken(token)
	if err != nil {
		conn.WriteJSON(gin.H{"status": "error", "error": errMsg})
		return
	}

	// 接受新连接，并处理旧连接
	if oldConn, exists := agent_runtime.GetConnectedClients()[uuid]; exists {
		log.Printf("Client %s is reconnecting. Closing the old connection.", uuid)

		// 强制关闭旧连接。这将导致旧连接的 ReadMessage() 循环出错退出。
		go oldConn.Close()
	}
	agent_runtime.SetConnectedClients(uuid, conn)
	log.Printf("Client %s is reconnect success, connID: %d", uuid, conn.ID)
	go notifier.OnlineNotification(uuid, conn.ID)
	defer func() {
		agent_runtime.DeleteClientConditionally(uuid, conn)
		notifier.OfflineNotification(uuid, conn.ID)
	}()

	// 首先处理第一次ws conn收到的消息
	processMessage(conn, message, uuid)

	for {
		conn.SetReadDeadline(time.Now().Add(readWait))

		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("Client %s connection error: %v", uuid, err)
			}
			break // 任何读错误（包括超时）都意味着连接已断开，退出循环
		}
		processMessage(conn, message, uuid)
	}
}

// 将消息处理逻辑提取到一个函数中，方便复用
func processMessage(conn *connection.SafeConn, message []byte, uuid string) {
	type MessageType struct {
		Type string `json:"type"`
	}
	var msgType MessageType
	err := json.Unmarshal(message, &msgType)
	if err != nil {
		conn.WriteJSON(gin.H{"status": "error", "error": "Invalid JSON"})
		return
	}

	switch msgType.Type {
	case "", "report":
		report := v1.Report{}
		err = json.Unmarshal(message, &report)
		if err != nil {
			conn.WriteJSON(gin.H{"status": "error", "error": "Invalid report format"})
			return
		}
		// WS 连接自行管理在线状态，无需刷新 POST presence
		if err := ingestReport(uuid, report, 1, false); err != nil {
			conn.WriteJSON(gin.H{"status": "error", "error": fmt.Sprintf("%v", err)})
			return
		}
	case "ping_result":
		var reqBody struct {
			PingTaskID uint      `json:"task_id"`
			PingResult int       `json:"value"`
			PingType   string    `json:"ping_type"`
			FinishedAt time.Time `json:"finished_at"`
		}
		err = json.Unmarshal(message, &reqBody)
		if err != nil {
			conn.WriteJSON(gin.H{"status": "error", "error": "Invalid ping result format"})
			return
		}
		ingestPingResult(uuid, reqBody.PingTaskID, reqBody.PingResult, reqBody.FinishedAt)
	default:
		log.Printf("Unknown message type: %s", msgType.Type)
		conn.WriteJSON(gin.H{"status": "error", "error": "Unknown message type"})
	}
}

func SaveClientReport(uuid string, report v1.Report) (v1.Report, error) {
	if report.CPU.Usage < 0.01 {
		report.CPU.Usage = 0.01
	}
	return report_cache.AppendClientReport(uuid, report)
}
