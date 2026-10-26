package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestCancelInterruptsUnresponsiveSTOMPHandshake(t *testing.T) {
	upgrader := websocket.Upgrader{}
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, _, _ = conn.ReadMessage() // CONNECT is received, but no CONNECTED response.
		close(started)
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := NewStompClient(server.URL, nil)
	done := make(chan error, 1)
	go func() { done <- client.ConnectContext(ctx) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handshake did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled handshake unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("disconnect did not cancel the in-flight handshake")
	}
	if client.IsConnected() {
		t.Fatal("canceled connection reported as connected")
	}
}
