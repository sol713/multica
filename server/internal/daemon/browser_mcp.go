package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

const browserToolSchema = `{"type":"object","properties":{"action":{"type":"string","enum":["navigate","dom_tree","click","type","fill","press","scroll","go_back","go_forward","reload","new_tab","switch_tab","close_tab","list_tabs","screenshot"]},"params":{"type":"object","properties":{"url":{"type":"string"},"ref":{"type":"string"},"selector":{"type":"string"},"text":{"type":"string"},"key":{"type":"string"},"x":{"type":"integer"},"y":{"type":"integer"},"direction":{"type":"string","enum":["up","down","left","right"]},"amount":{"type":"integer"},"tab_id":{"type":"string"}},"additionalProperties":false}},"required":["action"],"additionalProperties":false}`

func browserMCPProviderSupported(provider string) bool {
	return providerSupportsRemoteMCPBroker(provider) || provider == "dsh" || provider == "opencode" || provider == "opencode-v2"
}

func (d *Daemon) startChatBrowserMCP(ctx context.Context, task Task, provider string) (json.RawMessage, *http.Server, error) {
	if !browserEnabled() || task.ChatSessionID == "" || !browserMCPProviderSupported(provider) {
		return nil, nil, nil
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}
	token, err := randomBrokerToken()
	if err != nil {
		_ = listener.Close()
		return nil, nil, err
	}
	handler := &browserMCPHandler{browser: &d.browser, workspaceID: task.WorkspaceID, sessionID: task.ChatSessionID, path: "/" + token}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	go func() { <-ctx.Done(); _ = server.Close() }()
	config, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"multica-browser": map[string]any{
		"type": "http", "url": "http://" + listener.Addr().String() + handler.path,
	}}})
	return config, server, nil
}

type browserMCPHandler struct {
	browser                      *browserProcess
	workspaceID, sessionID, path string
}

func (h *browserMCPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != h.path || r.Method != http.MethodPost {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	body, err := io.ReadAll(r.Body)
	var request pluginHookMCPRequest
	if err != nil || json.Unmarshal(body, &request) != nil || request.JSONRPC != "2.0" {
		writePluginHookMCPError(w, nil, -32700, "invalid browser MCP request")
		return
	}
	if len(request.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	switch request.Method {
	case "initialize":
		writePluginHookMCPResult(w, request.ID, map[string]any{
			"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{"name": "multica-browser", "version": "1"},
		})
	case "ping":
		writePluginHookMCPResult(w, request.ID, map[string]any{})
	case "tools/list":
		writePluginHookMCPResult(w, request.ID, map[string]any{"tools": []map[string]any{{
			"name": "browser", "description": "Use the chat's shared Octop browser. The user watches this same session in the Browser panel. Navigate to HTTP(S) URLs including localhost. Use dom_tree to get refs, then click/fill/type/press/scroll. Use screenshot to see the viewport, list_tabs/switch_tab to inspect tabs. When the user takes control, stop browser actions and wait for them to return control before retrying. Do not start a different browser to bypass user control.",
			"inputSchema": json.RawMessage(browserToolSchema),
		}}})
	case "tools/call":
		var call struct {
			Name      string `json:"name"`
			Arguments struct {
				Action string          `json:"action"`
				Params json.RawMessage `json:"params"`
			} `json:"arguments"`
		}
		if json.Unmarshal(request.Params, &call) != nil || call.Name != "browser" {
			writePluginHookMCPError(w, request.ID, -32602, "unknown browser tool")
			return
		}
		output, callErr := h.browser.Call(r.Context(), protocol.BrowserRequest{
			WorkspaceID: h.workspaceID, SessionID: h.sessionID, Action: call.Arguments.Action, Params: call.Arguments.Params,
		}, "agent")
		var result struct {
			Content any    `json:"content"`
			Image   string `json:"image"`
			Enabled bool   `json:"enabled"`
		}
		if callErr == nil {
			if json.Unmarshal(output, &result) != nil {
				callErr = errors.New("invalid browser response")
			}
			if result.Content == nil && result.Image == "" {
				callErr = errors.New("Octop Browser is unavailable")
			}
		}
		if callErr != nil {
			writePluginHookMCPResult(w, request.ID, map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": callErr.Error()}}})
			return
		}
		text := ""
		if value, ok := result.Content.(string); ok {
			text = value
		} else {
			raw, _ := json.Marshal(result.Content)
			text = string(raw)
		}
		content := []map[string]any{{"type": "text", "text": text}}
		if result.Image != "" {
			content = append(content, map[string]any{"type": "image", "data": result.Image, "mimeType": "image/jpeg"})
		}
		writePluginHookMCPResult(w, request.ID, map[string]any{"content": content})
	default:
		writePluginHookMCPError(w, request.ID, -32601, "unsupported method")
	}
}
