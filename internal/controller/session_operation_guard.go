package controller

import (
	"browserd/internal/types"
	"net/http"
	"sync"
)

// Only external requests acquire this guard: evaluate's native PageTool bridge
// runs inside its owning request and must never re-enter it.
type sessionOperationGuard struct {
	mu   sync.Mutex
	busy map[string]bool
}

func (g *sessionOperationGuard) tryAcquire(id string) (func(), bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.busy[id] {
		return nil, false
	}
	if g.busy == nil {
		g.busy = make(map[string]bool)
	}
	g.busy[id] = true
	return g.releaseFunc(id), true
}
func (g *sessionOperationGuard) releaseFunc(id string) func() {
	var once sync.Once
	return func() { once.Do(func() { g.mu.Lock(); defer g.mu.Unlock(); delete(g.busy, id) }) }
}
func (h *SessionController) beginOperation(w http.ResponseWriter, id string) (func(), bool) {
	release, ok := h.operations.tryAcquire(id)
	if !ok {
		types.WriteErr(w, http.StatusConflict, "SESSION_BUSY", "another browser operation is executing; this request did not execute")
	}
	return release, ok
}
