package terminal

import (
	"sync"

	"github.com/komari-monitor/komari/web/connection"
)

type TerminalSession struct {
	UUID        string
	UserUUID    string
	Browser     *connection.SafeConn
	Agent       *connection.SafeConn
	RequesterIp string
}

var TerminalSessionsMutex = &sync.Mutex{}
var TerminalSessions = make(map[string]*TerminalSession)
