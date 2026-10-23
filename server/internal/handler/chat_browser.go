package handler

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ChatBrowser authorizes every frame and input against the current workspace,
// session creator and private-agent policy before forwarding to its runtime.
func (h *Handler) ChatBrowser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	session, ok := h.gatePublicChatSessionForUser(w, r, userID, ctxWorkspaceID(r.Context()), chi.URLParam(r, "sessionId"))
	if !ok {
		return
	}
	runtimeID := session.RuntimeID
	// A running first turn may not have pinned its resume pointer yet.
	if pending, err := h.Queries.GetPendingChatTask(r.Context(), session.ID); err == nil {
		if task, err := h.Queries.GetAgentTask(r.Context(), pending.ID); err == nil && task.RuntimeID.Valid {
			runtimeID = task.RuntimeID
		}
	}
	if !runtimeID.Valid {
		writeError(w, http.StatusConflict, "send a message first to bind this chat to a daemon")
		return
	}
	request := protocol.BrowserRequest{Action: "snapshot"}
	if r.Method == http.MethodPost {
		var input struct {
			Action string          `json:"action"`
			Params json.RawMessage `json:"params"`
			Epoch  int             `json:"epoch"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			writeError(w, http.StatusBadRequest, "invalid browser request")
			return
		}
		switch input.Action {
		case "start", "close", "handoff", "navigate", "go_back", "go_forward", "reload", "new_tab", "switch_tab", "close_tab", "input":
		default:
			writeError(w, http.StatusBadRequest, "unsupported browser action")
			return
		}
		request.Action, request.Params, request.Epoch = input.Action, input.Params, input.Epoch
	}
	request.RuntimeID, request.WorkspaceID, request.SessionID = uuidToString(runtimeID), uuidToString(session.WorkspaceID), uuidToString(session.ID)
	result, err := h.DaemonHub.CallBrowser(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}
