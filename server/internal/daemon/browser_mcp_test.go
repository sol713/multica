package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserMCPIsChatScopedAndOptional(t *testing.T) {
	t.Setenv("MULTICA_BROWSER_ENABLED", "1")
	d := &Daemon{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	config, server, err := d.startChatBrowserMCP(ctx, Task{WorkspaceID: "workspace", ChatSessionID: "chat"}, "dsh")
	if err != nil || server == nil || !strings.Contains(string(config), "multica-browser") {
		t.Fatalf("config %s, server %v, err %v", config, server, err)
	}
	defer server.Close()
	if cfg, srv, _ := d.startChatBrowserMCP(ctx, Task{}, "dsh"); cfg != nil || srv != nil {
		t.Fatal("non-chat task received browser")
	}
	if cfg, srv, _ := d.startChatBrowserMCP(ctx, Task{ChatSessionID: "chat"}, "unsupported"); cfg != nil || srv != nil {
		t.Fatal("unsupported provider received browser")
	}
	h := server.Handler.(*browserMCPHandler)
	if h.workspaceID != "workspace" || h.sessionID != "chat" {
		t.Fatal("task identity not bound")
	}
	for _, tc := range []struct{ path, method, want string }{
		{h.path, "tools/list", "multica-browser"},
		{h.path, "initialize", "protocolVersion"},
	} {
		request := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"`+tc.method+`"}`))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, request)
		if w.Code != 200 || !strings.Contains(w.Body.String(), tc.want) && tc.method != "tools/list" {
			t.Fatalf("response %d %s", w.Code, w.Body.String())
		}
		if !json.Valid(w.Body.Bytes()) {
			t.Fatal("invalid JSON response")
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/wrong-token", nil))
	if w.Code != http.StatusNotFound {
		t.Fatal("token path not enforced")
	}
}
