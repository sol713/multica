package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// This exercises the private pipe/CDP bridge, not an installed agent CLI.
func TestBrowserLocalChromiumIntegration(t *testing.T) {
	if os.Getenv("MULTICA_RUN_BROWSER_INTEGRATION") != "1" {
		t.Skip("set MULTICA_RUN_BROWSER_INTEGRATION=1 with Python/Octop/Chrome installed")
	}
	t.Setenv("MULTICA_BROWSER_ENABLED", "1")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("BROWSER_USE_CDP_HOST", "203.0.113.1")
	t.Setenv("BROWSER_USE_CDP_WS_URL", "ws://203.0.113.1:1")
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/primary" {
			http.SetCookie(w, &http.Cookie{Name: "bridge", Value: "primary", Path: "/"})
		}
		if r.URL.Path == "/isolation" {
			if _, err := r.Cookie("bridge"); err == nil {
				t.Error("cookies crossed chat profiles")
			}
		}
		_, _ = w.Write([]byte(`<title>Octop bridge fixture</title><h1>Shared browser</h1><input aria-label="Message">`))
	}))
	defer page.Close()
	browser := &browserProcess{}
	defer browser.Close()
	workspace, chat, other := uuid.NewString(), uuid.NewString(), uuid.NewString()
	call := func(session, action, actor string, params map[string]any, epoch int) json.RawMessage {
		t.Helper()
		var raw json.RawMessage
		if params != nil {
			raw, _ = json.Marshal(params)
		}
		result, err := browser.Call(context.Background(), protocol.BrowserRequest{WorkspaceID: workspace, SessionID: session, Action: action, Params: raw, Epoch: epoch}, actor)
		if err != nil {
			t.Fatalf("%s %s: %v", actor, action, err)
		}
		return result
	}
	call(chat, "navigate", "agent", map[string]any{"url": page.URL + "/primary"}, 0)
	var snapshot struct {
		Frame string `json:"frame"`
		Epoch int    `json:"epoch"`
		Tabs  []struct {
			URL string `json:"url"`
		} `json:"tabs"`
	}
	if err := json.Unmarshal(call(chat, "snapshot", "user", nil, 0), &snapshot); err != nil {
		t.Fatal(err)
	}
	image, err := jpeg.DecodeConfig(base64.NewDecoder(base64.StdEncoding, strings.NewReader(snapshot.Frame)))
	if err != nil || image.Width != 1280 || image.Height != 800 {
		t.Fatalf("viewport %v, error %v", image, err)
	}
	if len(snapshot.Tabs) == 0 || snapshot.Tabs[0].URL != page.URL+"/primary" {
		t.Fatalf("agent navigation not visible: %+v", snapshot.Tabs)
	}
	var handoff struct {
		Epoch int `json:"epoch"`
	}
	_ = json.Unmarshal(call(chat, "handoff", "user", map[string]any{"target": "user"}, 0), &handoff)
	_, err = browser.Call(context.Background(), protocol.BrowserRequest{WorkspaceID: workspace, SessionID: chat, Action: "reload"}, "agent")
	if err == nil || !strings.Contains(err.Error(), "controlled by the user") {
		t.Fatalf("handoff failed: %v", err)
	}
	call(chat, "navigate", "user", map[string]any{"url": page.URL + "/human"}, handoff.Epoch)
	call(chat, "handoff", "user", map[string]any{"target": "agent"}, 0)
	call(chat, "navigate", "agent", map[string]any{"url": page.URL + "/returned"}, 0)
	call(other, "navigate", "agent", map[string]any{"url": page.URL + "/isolation"}, 0)
	call(other, "close", "user", nil, 0)
	call(chat, "close", "user", nil, 0)
}
