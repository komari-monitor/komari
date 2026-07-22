package server

import (
	logger "github.com/komari-monitor/komari/utils/log"

	"github.com/komari-monitor/komari/internal/config"
)

// reloadHandler 是单个配置热重载处理器。
//
// name 仅用于日志定位，handler 接收配置变更事件，自行判断关心的 key 是否变化。
// 每个 handler 相互独立：一个 panic 不应影响其它 handler。
type reloadHandler struct {
	name    string
	handler func(config.ConfigEvent)
}

// ReloadManager 统一收敛所有配置热重载逻辑。
//
// 过去 config.Subscribe 分散在 cmd/server.go、cors.go 等多处，导致：
//   - 谁监听哪些 key 不清晰；
//   - 某个 handler panic 会连带影响同一订阅回调里的其它逻辑；
//   - 关闭时无法统一停止。
//
// ReloadManager 把这些 handler 注册在一处，用单个 config.Subscribe 分发，
// 并对每个 handler 做 panic 隔离。
type ReloadManager struct {
	handlers []reloadHandler
	started  bool
}

// NewReloadManager 创建一个空的热重载管理器。
func NewReloadManager() *ReloadManager {
	return &ReloadManager{}
}

// Register 注册一个命名的热重载处理器。必须在 Start 之前调用。
func (m *ReloadManager) Register(name string, handler func(config.ConfigEvent)) {
	if handler == nil {
		return
	}
	m.handlers = append(m.handlers, reloadHandler{name: name, handler: handler})
}

// Start 向 config 订阅一次，之后每个事件会分发给全部已注册 handler。
// 重复调用无副作用。
func (m *ReloadManager) Start() {
	if m.started {
		return
	}
	m.started = true
	config.Subscribe(func(event config.ConfigEvent) {
		for _, h := range m.handlers {
			m.dispatch(h, event)
		}
	})
}

// dispatch 执行单个 handler，并隔离 panic，避免一个 handler 影响其它。
func (m *ReloadManager) dispatch(h reloadHandler, event config.ConfigEvent) {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("reload", "config reload handler %q panicked: %v", h.name, r)
		}
	}()
	h.handler(event)
}
