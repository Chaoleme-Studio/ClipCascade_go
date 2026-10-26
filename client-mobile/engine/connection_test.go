package engine

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/clipcascade/pkg/protocol"
	"github.com/gorilla/websocket"
)

func TestLoginRejectsNonLoginResponses(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			}))
			defer server.Close()
			e := NewEngine(server.URL, "alice", "secret", false)
			if err := e.login(); err == nil {
				t.Fatal("non-login response was treated as a successful login")
			}
		})
	}
}

func TestNetworkRecoveryWakesReconnectBackoff(t *testing.T) {
	failed := make(chan struct{})
	secondAttempt := make(chan struct{})
	var mu sync.Mutex
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		attempt := attempts
		mu.Unlock()
		w.WriteHeader(http.StatusServiceUnavailable)
		if attempt == 1 {
			close(failed)
		} else if attempt == 2 {
			close(secondAttempt)
		}
	}))
	defer server.Close()
	e := NewEngine(server.URL, "alice", "secret", false)
	done := make(chan error, 1)
	go func() { done <- e.reconnectLoop() }()
	defer func() { e.Stop(); <-done }()
	select {
	case <-failed:
	case <-time.After(time.Second):
		t.Fatal("initial reconnect did not run")
	}
	e.ReconnectNow()
	select {
	case <-secondAttempt:
	case <-time.After(time.Second):
		t.Fatal("network recovery was blocked by the two-second backoff")
	}
}

func TestMobileEngineExchangesClipboardWithServer(t *testing.T) {
	upgrader := websocket.Upgrader{}
	serverResult := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "test-session", Path: "/"})
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, protocol.ConnectedFrame("1.1").Encode())
		if _, _, err := conn.ReadMessage(); err != nil { // SUBSCRIBE
			return
		}
		_, message, err := conn.ReadMessage()
		if err != nil {
			return
		}
		frame, err := protocol.ParseFrame(message)
		if err == nil && frame.Command == "SEND" {
			serverResult <- frame.Body
		}
		_, _, _ = conn.ReadMessage() // keep connection alive until Stop
	}))
	defer server.Close()
	e := NewEngine(server.URL, "alice", "secret", false)
	if err := e.Start(); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	if err := e.SendClipboard("clipboard probe", "text"); err != nil {
		t.Fatal(err)
	}
	select {
	case body := <-serverResult:
		data, err := protocol.DecodeClipboardData([]byte(body))
		if err != nil || data.Payload != "clipboard probe" || data.Type != "text" {
			t.Fatalf("wrong clipboard payload: %s, %v", body, err)
		}
	case <-time.After(time.Second):
		t.Fatal("clipboard did not reach server")
	}
}
