package terminal

import (
	"sync"

	"github.com/gorilla/websocket"
)

type TerminalSession struct {
	UUID        string
	UserUUID    string
	Browser     *websocket.Conn
	Agent       *websocket.Conn
	RequesterIp string
}

var TerminalSessionsMutex = &sync.Mutex{}
var TerminalSessions = make(map[string]*TerminalSession)

func CloseClientSessions(uuid string) {
	var sessions []*TerminalSession
	TerminalSessionsMutex.Lock()
	for id, session := range TerminalSessions {
		if session.UUID != uuid {
			continue
		}
		delete(TerminalSessions, id)
		sessions = append(sessions, session)
	}
	TerminalSessionsMutex.Unlock()

	for _, session := range sessions {
		if session.Browser != nil {
			_ = session.Browser.Close()
		}
		if session.Agent != nil {
			_ = session.Agent.Close()
		}
	}
}
