package daemonws

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type browserRelayPublisher interface {
	PublishWithID(string, string, string, []byte, string) error
}

type browserPendingCall struct {
	runtimeID string
	response  chan protocol.BrowserResponse
}

func (h *Hub) setBrowserRelay(relay browserRelayPublisher) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.browserRelay = relay
}

// CallBrowser carries bounded, authenticated viewport requests to the machine
// hosting the runtime. Cross-node replies use the same daemon-only Redis scope.
func (h *Hub) CallBrowser(ctx context.Context, request protocol.BrowserRequest) (json.RawMessage, error) {
	if h == nil {
		return nil, errors.New("daemon connection unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 32*time.Second)
	defer cancel()
	request.ID = uuid.NewString()
	deadline, _ := ctx.Deadline()
	request.ExpiresAt = deadline.UnixMilli()
	ch := make(chan protocol.BrowserResponse, 1)
	h.mu.Lock()
	if len(h.browserPending) >= 128 {
		h.mu.Unlock()
		return nil, errors.New("browser relay busy")
	}
	if h.browserPending == nil {
		h.browserPending = make(map[string]browserPendingCall)
	}
	h.browserPending[request.ID] = browserPendingCall{request.RuntimeID, ch}
	relay := h.browserRelay
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.browserPending, request.ID); h.mu.Unlock() }()
	frame, err := json.Marshal(protocol.Message{Type: protocol.EventDaemonBrowserRequest, Payload: mustMarshalRaw(request)})
	if err != nil {
		return nil, err
	}
	delivered, _ := h.notifyFrame(request.RuntimeID, frame, request.ID)
	if relay != nil {
		if err := relay.PublishWithID(realtime.ScopeDaemonRuntime, request.RuntimeID, "", frame, request.ID); err != nil && !delivered {
			return nil, errors.New("browser relay unavailable")
		}
	} else if !delivered {
		return nil, errors.New("daemon offline; start the chat's daemon first")
	}
	select {
	case response := <-ch:
		if response.Error != "" {
			return nil, errors.New(response.Error)
		}
		return response.Result, nil
	case <-ctx.Done():
		return nil, errors.New("browser request timed out; check or upgrade the daemon")
	}
}

func (h *Hub) deliverBrowser(response protocol.BrowserResponse) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	call, ok := h.browserPending[response.ID]
	if !ok || call.runtimeID != response.RuntimeID {
		return
	}
	select {
	case call.response <- response:
	default:
	}
}

func (c *client) handleBrowserResponse(raw json.RawMessage) {
	var response protocol.BrowserResponse
	if json.Unmarshal(raw, &response) != nil || response.ID == "" || !c.allowsRuntime(response.RuntimeID) {
		return
	}
	c.hub.deliverBrowser(response)
	c.hub.mu.RLock()
	relay := c.hub.browserRelay
	c.hub.mu.RUnlock()
	if relay != nil {
		frame, _ := json.Marshal(protocol.Message{Type: protocol.EventDaemonBrowserResponse, Payload: raw})
		_ = relay.PublishWithID(realtime.ScopeDaemonRuntime, response.RuntimeID, "", frame, uuid.NewString())
	}
}
