package protocol

import "encoding/json"

const (
	EventDaemonBrowserRequest  = "daemon:browser_request"
	EventDaemonBrowserResponse = "daemon:browser_response"
)

// BrowserRequest is server-authorized; profile identity never comes from tool arguments.
type BrowserRequest struct {
	ID          string          `json:"id"`
	RuntimeID   string          `json:"runtime_id"`
	WorkspaceID string          `json:"workspace_id"`
	SessionID   string          `json:"session_id"`
	Action      string          `json:"action"`
	Params      json.RawMessage `json:"params,omitempty"`
	Epoch       int             `json:"epoch,omitempty"`
	ExpiresAt   int64           `json:"expires_at,omitempty"`
}

type BrowserResponse struct {
	ID        string          `json:"id"`
	RuntimeID string          `json:"runtime_id"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
}
