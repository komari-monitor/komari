package client

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/komari-monitor/komari/web/connection"
)

func newHeartbeatTestServer(
	t *testing.T,
	readWait time.Duration,
	writeWait time.Duration,
	ready chan<- struct{},
) (*httptest.Server, <-chan error) {
	t.Helper()

	serverDone := make(chan error, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawConn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverDone <- err
			return
		}
		conn := connection.NewSafeConn(rawConn)
		setWebSocketPingHandler(conn, readWait, writeWait)
		if ready != nil {
			close(ready)
		}
		defer func() {
			_ = conn.Close()
			serverDone <- err
		}()

		for {
			if err = conn.SetReadDeadline(time.Now().Add(readWait)); err != nil {
				return
			}
			_, _, err = conn.ReadMessage()
			if err != nil {
				return
			}
		}
	}))
	return server, serverDone
}

func dialHeartbeatTestServer(t *testing.T, server *httptest.Server) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func TestWebSocketHeartbeatRefreshesDeadlineOnPing(t *testing.T) {
	const (
		pingPeriod = 40 * time.Millisecond
		readWait   = 300 * time.Millisecond
		writeWait  = 100 * time.Millisecond
	)

	ready := make(chan struct{})
	server, serverDone := newHeartbeatTestServer(t, readWait, writeWait, ready)
	defer server.Close()

	clientConn := dialHeartbeatTestServer(t, server)
	defer clientConn.Close()
	pong := make(chan struct{}, 1)
	clientConn.SetPongHandler(func(string) error {
		select {
		case pong <- struct{}{}:
		default:
		}
		return nil
	})
	clientReadDone := make(chan struct{})
	go func() {
		defer close(clientReadDone)
		for {
			if _, _, err := clientConn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("server heartbeat handler did not start")
	}

	stopPings := make(chan struct{})
	pingsDone := make(chan struct{})
	go func() {
		defer close(pingsDone)
		ticker := time.NewTicker(pingPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-stopPings:
				return
			case <-ticker.C:
				if err := clientConn.WriteControl(
					websocket.PingMessage,
					nil,
					time.Now().Add(writeWait),
				); err != nil {
					return
				}
			}
		}
	}()

	select {
	case <-pong:
	case <-time.After(time.Second):
		t.Fatal("server did not answer the client ping")
	}
	select {
	case err := <-serverDone:
		t.Fatalf("server closed a responsive idle connection: %v", err)
	case <-time.After(4 * readWait):
	}

	close(stopPings)
	<-pingsDone
	_ = clientConn.Close()
	<-clientReadDone
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("server did not finish after client close")
	}
}

func TestWebSocketHeartbeatTimesOutWithoutPing(t *testing.T) {
	const (
		readWait  = 300 * time.Millisecond
		writeWait = 100 * time.Millisecond
	)

	server, serverDone := newHeartbeatTestServer(t, readWait, writeWait, nil)
	defer server.Close()

	clientConn := dialHeartbeatTestServer(t, server)
	defer clientConn.Close()

	select {
	case err := <-serverDone:
		netErr, ok := err.(net.Error)
		if !ok || !netErr.Timeout() {
			t.Fatalf("server read error = %v, want heartbeat timeout", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not time out an idle peer")
	}
}
