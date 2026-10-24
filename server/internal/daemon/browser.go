package daemon

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

//go:embed browser_sidecar.py
var browserSidecar string

type browserProcess struct {
	mu       sync.Mutex
	writeMu  sync.Mutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	pending  map[string]chan protocol.BrowserResponse
	closed   bool
	requests browserRequestGuard
}

func browserEnabled() bool { return os.Getenv("MULTICA_BROWSER_ENABLED") == "1" }

func (b *browserProcess) startLocked() error {
	if b.closed {
		return errors.New("browser service stopped")
	}
	if b.cmd != nil {
		return nil
	}
	python := os.Getenv("MULTICA_BROWSER_PYTHON")
	if python == "" {
		python = "python3"
	}
	cmd := exec.Command(python, "-u", "-c", browserSidecar)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "BROWSER_USE_CDP_HOST=") && !strings.HasPrefix(value, "BROWSER_USE_CDP_WS_URL=") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, "BROWSER_USE_CDP_HOST=127.0.0.1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return err
	}
	// Never copy browser stdout/stderr into daemon task logs: URLs can contain credentials.
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return fmt.Errorf("cannot start Octop Browser: configure MULTICA_BROWSER_PYTHON and install octop-browser==1.0.0: %w", err)
	}
	b.cmd, b.stdin = cmd, stdin
	b.pending = make(map[string]chan protocol.BrowserResponse)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), 3<<20)
		for scanner.Scan() {
			var response protocol.BrowserResponse
			if json.Unmarshal(scanner.Bytes(), &response) != nil {
				continue
			}
			b.mu.Lock()
			if ch := b.pending[response.ID]; ch != nil {
				select {
				case ch <- response:
				default:
				}
			}
			b.mu.Unlock()
		}
		_ = cmd.Wait()
		b.mu.Lock()
		defer b.mu.Unlock()
		for id, ch := range b.pending {
			select {
			case ch <- protocol.BrowserResponse{ID: id, Error: "Octop Browser stopped; check Python, octop-browser==1.0.0 and Chrome installation"}:
			default:
			}
		}
		b.cmd, b.stdin = nil, nil
	}()
	return nil
}

func (b *browserProcess) Call(ctx context.Context, request protocol.BrowserRequest, actor string) (json.RawMessage, error) {
	if !browserEnabled() {
		return json.RawMessage(`{"enabled":false,"active":false}`), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 28*time.Second)
	defer cancel()
	if request.ExpiresAt != 0 {
		var cancelDeadline context.CancelFunc
		ctx, cancelDeadline = context.WithDeadline(ctx, time.UnixMilli(request.ExpiresAt))
		defer cancelDeadline()
	}
	deadline, _ := ctx.Deadline()
	request.ExpiresAt = deadline.UnixMilli()
	request.ID = uuid.NewString()
	body, _ := json.Marshal(struct {
		protocol.BrowserRequest
		Actor string `json:"actor"`
	}{request, actor})
	ch := make(chan protocol.BrowserResponse, 1)
	b.writeMu.Lock()
	b.mu.Lock()
	err := b.startLocked()
	if err == nil {
		b.pending[request.ID] = ch
	}
	stdin := b.stdin
	b.mu.Unlock()
	if err == nil {
		_, err = stdin.Write(append(body, '\n'))
	}
	b.writeMu.Unlock()
	defer func() { b.mu.Lock(); delete(b.pending, request.ID); b.mu.Unlock() }()
	if err != nil {
		return nil, err
	}
	select {
	case response := <-ch:
		if response.Error != "" {
			return nil, errors.New(response.Error)
		}
		return response.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *browserProcess) Close() {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	b.mu.Lock()
	b.closed = true
	if b.stdin != nil {
		_ = b.stdin.Close()
	}
	cmd := b.cmd
	b.mu.Unlock()
	if cmd != nil {
		go func() { time.Sleep(5 * time.Second); _ = cmd.Process.Kill() }()
	}
}

func (d *Daemon) handleBrowserRequest(raw json.RawMessage) {
	var request protocol.BrowserRequest
	if json.Unmarshal(raw, &request) != nil || request.ID == "" {
		return
	}
	if d.findRuntime(request.RuntimeID) == nil {
		return
	}
	accepted, claimErr := d.browser.requests.claim(request, time.Now())
	if !accepted && claimErr == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var result json.RawMessage
	err := claimErr
	if err == nil {
		result, err = d.browser.Call(ctx, request, "user")
	}
	response := protocol.BrowserResponse{ID: request.ID, RuntimeID: request.RuntimeID, Result: result}
	if err != nil {
		response.Error = err.Error()
	}
	frame, _ := json.Marshal(protocol.Message{Type: protocol.EventDaemonBrowserResponse, Payload: marshalRaw(response)})
	d.wsRPC.mu.Lock()
	defer d.wsRPC.mu.Unlock()
	if d.wsRPC.sendFrame != nil {
		_, _ = d.wsRPC.sendFrame(frame)
	}
}
