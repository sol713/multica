package daemonws

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestBrowserRelayRoundTrip(t *testing.T) {
	hub := NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{RuntimeIDs: []string{"runtime-1"}})
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for hub.RuntimeConnectionCount("runtime-1") == 0 {
		select {
		case <-ctx.Done():
			t.Fatal("not connected")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	go func() {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var message protocol.Message
		_ = json.Unmarshal(raw, &message)
		var request protocol.BrowserRequest
		_ = json.Unmarshal(message.Payload, &request)
		if request.ExpiresAt <= time.Now().UnixMilli() || request.ExpiresAt > time.Now().Add(32*time.Second).UnixMilli() {
			t.Error("browser request must carry a bounded deadline")
		}
		_ = conn.WriteJSON(protocol.Message{Type: protocol.EventDaemonBrowserResponse, Payload: mustMarshalRaw(protocol.BrowserResponse{ID: request.ID, RuntimeID: request.RuntimeID, Result: json.RawMessage(`{"enabled":true}`)})})
	}()
	result, err := hub.CallBrowser(ctx, protocol.BrowserRequest{RuntimeID: "runtime-1", Action: "snapshot"})
	if err != nil || string(result) != `{"enabled":true}` {
		t.Fatalf("result %s, error %v", result, err)
	}
}

func TestBrowserResponseCannotCrossRuntime(t *testing.T) {
	hub := NewHub()
	ch := make(chan protocol.BrowserResponse, 1)
	hub.browserPending = map[string]browserPendingCall{"request": {runtimeID: "runtime-1", response: ch}}
	c := &client{hub: hub, runtimes: map[string]struct{}{"runtime-2": {}}}
	c.handleBrowserResponse(mustMarshalRaw(protocol.BrowserResponse{ID: "request", RuntimeID: "runtime-1", Result: json.RawMessage(`{}`)}))
	hub.deliverBrowser(protocol.BrowserResponse{ID: "request", RuntimeID: "runtime-2"})
	select {
	case <-ch:
		t.Fatal("cross-runtime response accepted")
	default:
	}
}

func TestBrowserOfflineCleansPendingRequest(t *testing.T) {
	hub := NewHub()
	if _, err := hub.CallBrowser(context.Background(), protocol.BrowserRequest{RuntimeID: "offline"}); err == nil {
		t.Fatal("expected offline error")
	}
	if len(hub.browserPending) != 0 {
		t.Fatal("pending request leaked")
	}
}
