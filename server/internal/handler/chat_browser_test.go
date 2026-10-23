package handler

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestBrowserChatAuthorization(t *testing.T) {
	agentID := createHandlerTestAgent(t, "BrowserAuthorizationAgent", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, agentID)
	for _, tc := range []struct {
		name, user, session string
		want                int
	}{
		{"anonymous", "", sessionID, http.StatusUnauthorized},
		{"another creator", uuid.NewString(), sessionID, http.StatusForbidden},
		{"malformed session", testUserID, "not-a-uuid", http.StatusBadRequest},
		{"other workspace session", testUserID, uuid.NewString(), http.StatusNotFound},
		{"unbound chat", testUserID, sessionID, http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := testutil.JSONRequest(http.MethodGet, "/api/chat/sessions/"+tc.session+"/browser", nil)
			if tc.user != "" {
				req.Header.Set("X-User-ID", tc.user)
			}
			req = withURLParam(req, "sessionId", tc.session)
			req = withChatTestWorkspaceCtx(t, req)
			testutil.Call(t, testHandler.ChatBrowser, req).Want(tc.want)
		})
	}
}
