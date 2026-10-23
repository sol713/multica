package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const browserRequestLifetime = 35 * time.Second

// Redis can replay browser RPCs after reconnect. Claim mutations on disk before
// execution so even a daemon restart cannot repeat a still-valid request.
type browserRequestGuard struct {
	mu        sync.Mutex
	seen      map[string]int64
	dir       string
	cleanedAt time.Time
}

func (g *browserRequestGuard) claim(request protocol.BrowserRequest, now time.Time) (bool, error) {
	if _, err := uuid.Parse(request.ID); err != nil || request.ExpiresAt <= now.UnixMilli() || request.ExpiresAt > now.Add(browserRequestLifetime).UnixMilli() {
		return false, errors.New("browser request expired; refresh before interacting")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.seen == nil {
		g.seen = make(map[string]int64)
	}
	for id, expires := range g.seen {
		if expires <= now.UnixMilli() {
			delete(g.seen, id)
		}
	}
	if _, exists := g.seen[request.ID]; exists {
		return false, nil
	}
	if len(g.seen) >= 4096 {
		return false, errors.New("browser request guard busy")
	}
	if request.Action != "snapshot" {
		if g.dir == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return false, err
			}
			g.dir = filepath.Join(home, ".multica", "browser-requests")
		}
		if err := os.MkdirAll(g.dir, 0700); err != nil {
			return false, err
		}
		if now.Sub(g.cleanedAt) >= time.Minute {
			entries, err := os.ReadDir(g.dir)
			if err != nil {
				return false, err
			}
			for _, entry := range entries {
				info, err := entry.Info()
				if err == nil && !entry.IsDir() && info.ModTime().Add(browserRequestLifetime).Before(now) {
					_ = os.Remove(filepath.Join(g.dir, entry.Name()))
				}
			}
			g.cleanedAt = now
		}
		file, err := os.OpenFile(filepath.Join(g.dir, request.ID), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if err := file.Close(); err != nil {
			return false, err
		}
	}
	g.seen[request.ID] = request.ExpiresAt
	return true, nil
}
