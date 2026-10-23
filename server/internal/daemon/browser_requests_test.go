package daemon

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestBrowserRequestReplayGuard(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	guard := browserRequestGuard{dir: dir}
	request := protocol.BrowserRequest{ID: uuid.NewString(), Action: "input", ExpiresAt: now.Add(30 * time.Second).UnixMilli()}
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Go(func() {
			if ok, err := guard.claim(request, now); err != nil {
				t.Error(err)
			} else if ok {
				accepted.Add(1)
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("executed %d times", accepted.Load())
	}
	// Hundreds of snapshots must not evict a mutation that can still replay.
	for i := 0; i < 700; i++ {
		if ok, err := guard.claim(protocol.BrowserRequest{ID: uuid.NewString(), Action: "snapshot", ExpiresAt: request.ExpiresAt}, now); !ok || err != nil {
			t.Fatalf("snapshot: %v", err)
		}
	}
	if ok, _ := guard.claim(request, now); ok {
		t.Fatal("mutation evicted by snapshot traffic")
	}
	restarted := browserRequestGuard{dir: dir}
	if ok, _ := restarted.claim(request, now); ok {
		t.Fatal("replayed mutation accepted after restart")
	}
	if ok, err := restarted.claim(request, now.Add(time.Minute)); ok || err == nil {
		t.Fatal("expired replay accepted")
	}
	request.ExpiresAt = 0
	if ok, err := restarted.claim(request, now); ok || err == nil {
		t.Fatal("missing deadline accepted")
	}
	request.ExpiresAt = now.Add(time.Hour).UnixMilli()
	if ok, err := restarted.claim(request, now); ok || err == nil {
		t.Fatal("unbounded deadline accepted")
	}
}
