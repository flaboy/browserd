# Visible Virtual Pointer Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add an always-visible virtual mouse pointer to browserd live handoff/viewer windows, driven by the same internal pointer state used by `Act`, without changing the external `/v1/sessions/{id}/act` API.

**Architecture:** Keep the pointer out of the target web page DOM. Browserd maintains one virtual pointer state per runtime session, publishes sanitized pointer snapshots from trusted mouse movement, and exposes those snapshots only through live-token-authenticated viewer internals. The live viewer renders a lightweight overlay above the noVNC canvas and maps browser viewport coordinates to the displayed canvas rectangle.

**Tech Stack:** Go HTTP handlers and in-memory session state in `internal/browser`, `internal/controller`, and `internal/router`; TypeScript/Vite live viewer in `web/browser-live`; existing `npm` scripts and Go tests.

---

## Non-Goals

- Do not change the external `ActInput` / `/v1/sessions/{runtimeSessionId}/act` request or response shape.
- Do not inject a cursor element into the remote page DOM.
- Do not expose runtime session ids, browser profile paths, cookies, tokens, proxy data, or fingerprint material to the browser page.
- Do not add configuration or environment variables for timing/profile behavior in this iteration.

## Design Notes

- The source of truth is browserd's internal virtual pointer state, not the noVNC cursor.
- The visible cursor is a live-view overlay only. It improves operator observability during handoff and debugging, but does not become part of screenshots or platform DOM.
- Pointer event streaming is live-token protected and implementation-private under the live viewer route. It is acceptable to add a viewer-internal endpoint such as `/v/{token}/pointer-events`; it does not modify the public `/v1/sessions/.../act` API.
- The overlay must be visible in both normal live view and control handoff.
- Manual human movement in noVNC is not required to update the virtual pointer in this pass. This plan is scoped to showing browserd-initiated `Act` movement clearly. Manual pointer reconciliation can be added later if noVNC exposes reliable pointer telemetry.

---

### Task 1: Add Virtual Pointer Data Model

**Files:**
- Create: `internal/browser/virtual_pointer.go`
- Create: `internal/browser/virtual_pointer_test.go`
- Modify: `internal/browser/service.go`

**Step 1: Write the failing test**

Add `internal/browser/virtual_pointer_test.go`:

```go
package browser

import "testing"

func TestVirtualPointerSnapshotIsSanitized(t *testing.T) {
	state := pointerState{
		Point:       pointerPoint{X: 42.5, Y: 81.25},
		Viewport:    viewportRect{Width: 1366, Height: 768},
		Initialized: true,
		ButtonDown:  true,
	}

	got := newVirtualPointerSnapshot("rt_1", state)

	if got.RuntimeSessionID != "" {
		t.Fatalf("snapshot must not expose runtime session id, got %q", got.RuntimeSessionID)
	}
	if got.X != 42.5 || got.Y != 81.25 {
		t.Fatalf("unexpected pointer coordinates: %+v", got)
	}
	if got.ViewportWidth != 1366 || got.ViewportHeight != 768 {
		t.Fatalf("unexpected viewport: %+v", got)
	}
	if !got.Visible || !got.ButtonDown {
		t.Fatalf("expected visible button-down pointer: %+v", got)
	}
}
```

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/browser -run TestVirtualPointerSnapshotIsSanitized -count=1
```

Expected: FAIL because `Viewport`, `ButtonDown`, `VirtualPointerSnapshot`, and `newVirtualPointerSnapshot` do not exist.

**Step 3: Write minimal implementation**

Create `internal/browser/virtual_pointer.go`:

```go
package browser

type VirtualPointerSnapshot struct {
	X              float64 `json:"x"`
	Y              float64 `json:"y"`
	ViewportWidth  float64 `json:"viewportWidth"`
	ViewportHeight float64 `json:"viewportHeight"`
	Visible        bool    `json:"visible"`
	ButtonDown     bool    `json:"buttonDown"`

	// RuntimeSessionID is intentionally omitted from JSON and remains empty in
	// live-view responses. It exists only to make accidental exposure testable.
	RuntimeSessionID string `json:"-"`
}

func newVirtualPointerSnapshot(_ string, state pointerState) VirtualPointerSnapshot {
	return VirtualPointerSnapshot{
		X:              state.Point.X,
		Y:              state.Point.Y,
		ViewportWidth:  state.Viewport.Width,
		ViewportHeight: state.Viewport.Height,
		Visible:        state.Initialized,
		ButtonDown:     state.ButtonDown,
	}
}
```

Modify `internal/browser/service.go`:

```go
type pointerState struct {
	Point       pointerPoint
	Viewport    viewportRect
	Initialized bool
	ButtonDown  bool
}
```

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./internal/browser -run TestVirtualPointerSnapshotIsSanitized -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/browser/virtual_pointer.go internal/browser/virtual_pointer_test.go internal/browser/service.go
git commit -m "Add virtual pointer snapshot model"
```

---

### Task 2: Publish Pointer State from Browser Actions

**Files:**
- Modify: `internal/browser/service.go`
- Modify: `internal/browser/service_test.go`
- Modify: `internal/browser/virtual_pointer.go`
- Modify: `internal/browser/virtual_pointer_test.go`

**Step 1: Write the failing test**

Add to `internal/browser/virtual_pointer_test.go`:

```go
func TestPointerSnapshotReturnsLatestVirtualPointer(t *testing.T) {
	svc := NewServiceWithOptions(ServiceOptions{})
	svc.setPointer("rt_1", pointerPoint{X: 10, Y: 20}, viewportRect{Width: 100, Height: 80})

	got, ok := svc.PointerSnapshot("rt_1")

	if !ok {
		t.Fatal("expected pointer snapshot")
	}
	if got.X != 10 || got.Y != 20 || got.ViewportWidth != 100 || got.ViewportHeight != 80 {
		t.Fatalf("unexpected snapshot: %+v", got)
	}
	if !got.Visible {
		t.Fatalf("expected visible pointer: %+v", got)
	}
}
```

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/browser -run TestPointerSnapshotReturnsLatestVirtualPointer -count=1
```

Expected: FAIL because `setPointer` still accepts only point and `PointerSnapshot` does not exist.

**Step 3: Write minimal implementation**

Modify `internal/browser/service.go`:

```go
func (s *Service) PointerSnapshot(runtimeSessionID string) (VirtualPointerSnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.pointers[runtimeSessionID]
	if !ok || !state.Initialized {
		return VirtualPointerSnapshot{Visible: false}, false
	}
	return newVirtualPointerSnapshot(runtimeSessionID, state), true
}

func (s *Service) setPointer(runtimeSessionID string, point pointerPoint, viewport viewportRect) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pointers[runtimeSessionID] = pointerState{
		Point:       point,
		Viewport:    viewport,
		Initialized: true,
	}
}
```

Update the call in `trustedMoveTarget`:

```go
s.setPointer(runtimeSessionID, end, viewport)
```

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./internal/browser -run 'TestPointerSnapshotReturnsLatestVirtualPointer|TestVirtualPointerSnapshotIsSanitized' -count=1
```

Expected: PASS.

**Step 5: Run browser package tests**

Run:

```bash
go test ./internal/browser
```

Expected: PASS.

**Step 6: Commit**

```bash
git add internal/browser/service.go internal/browser/virtual_pointer.go internal/browser/virtual_pointer_test.go
git commit -m "Publish latest virtual pointer state"
```

---

### Task 3: Add Pointer Subscriber Hub

**Files:**
- Modify: `internal/browser/virtual_pointer.go`
- Modify: `internal/browser/virtual_pointer_test.go`
- Modify: `internal/browser/service.go`

**Step 1: Write the failing test**

Add to `internal/browser/virtual_pointer_test.go`:

```go
func TestPointerSubscriptionReceivesMovementSnapshots(t *testing.T) {
	svc := NewServiceWithOptions(ServiceOptions{})
	sub, err := svc.SubscribePointer("rt_1")
	if err != nil {
		t.Fatalf("subscribe pointer: %v", err)
	}
	defer sub.Close()

	svc.setPointer("rt_1", pointerPoint{X: 11, Y: 22}, viewportRect{Width: 100, Height: 80})

	select {
	case got := <-sub.C:
		if got.X != 11 || got.Y != 22 {
			t.Fatalf("unexpected snapshot: %+v", got)
		}
	default:
		t.Fatal("expected pointer snapshot to be delivered")
	}
}
```

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/browser -run TestPointerSubscriptionReceivesMovementSnapshots -count=1
```

Expected: FAIL because subscriptions are not implemented.

**Step 3: Write minimal implementation**

Modify `internal/browser/virtual_pointer.go`:

```go
type PointerSubscription struct {
	C     <-chan VirtualPointerSnapshot
	close func()
}

func (s PointerSubscription) Close() {
	if s.close != nil {
		s.close()
	}
}
```

Modify `Service` in `internal/browser/service.go`:

```go
pointerSubscribers map[string]map[chan VirtualPointerSnapshot]struct{}
```

Initialize it in `NewServiceWithOptions`:

```go
pointerSubscribers: map[string]map[chan VirtualPointerSnapshot]struct{}{},
```

Add:

```go
func (s *Service) SubscribePointer(runtimeSessionID string) (PointerSubscription, error) {
	if strings.TrimSpace(runtimeSessionID) == "" {
		return PointerSubscription{}, ErrInvalidRequest
	}
	ch := make(chan VirtualPointerSnapshot, 16)
	s.mu.Lock()
	if s.pointerSubscribers[runtimeSessionID] == nil {
		s.pointerSubscribers[runtimeSessionID] = map[chan VirtualPointerSnapshot]struct{}{}
	}
	s.pointerSubscribers[runtimeSessionID][ch] = struct{}{}
	if state, ok := s.pointers[runtimeSessionID]; ok && state.Initialized {
		ch <- newVirtualPointerSnapshot(runtimeSessionID, state)
	}
	s.mu.Unlock()
	return PointerSubscription{
		C: ch,
		close: func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			delete(s.pointerSubscribers[runtimeSessionID], ch)
			close(ch)
		},
	}, nil
}
```

Update `setPointer` to publish non-blocking:

```go
snapshot := newVirtualPointerSnapshot(runtimeSessionID, state)
for ch := range s.pointerSubscribers[runtimeSessionID] {
	select {
	case ch <- snapshot:
	default:
	}
}
```

Update `Close` to close and delete subscribers:

```go
for ch := range s.pointerSubscribers[runtimeSessionID] {
	close(ch)
}
delete(s.pointerSubscribers, runtimeSessionID)
```

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./internal/browser -run TestPointerSubscriptionReceivesMovementSnapshots -count=1
```

Expected: PASS.

**Step 5: Run all browser package tests**

Run:

```bash
go test ./internal/browser
```

Expected: PASS.

**Step 6: Commit**

```bash
git add internal/browser/service.go internal/browser/virtual_pointer.go internal/browser/virtual_pointer_test.go
git commit -m "Stream virtual pointer snapshots"
```

---

### Task 4: Expose Live-Token Pointer Event Stream

**Files:**
- Modify: `internal/controller/session_controller.go`
- Modify: `internal/controller/session_controller_test.go`
- Modify: `internal/router/router.go`
- Modify: `internal/router/router_test.go`

**Step 1: Write the failing controller test**

Add to `internal/controller/session_controller_test.go` a fake runtime method and test:

```go
func TestServePointerEventsRequiresValidLiveToken(t *testing.T) {
	handler := NewSessionControllerWithLive(SessionControllerOptions{
		Manager:    fakeSessionManagerWith("rt_1"),
		Browser:    &fakePointerBrowserRuntime{},
		LiveBaseURL: "http://browserd.test",
		TokenStore:  live.NewTokenStore(live.TokenStoreOptions{}),
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v/bad-token/pointer-events", nil)

	handler.ServePointerEvents(rr, req, "bad-token")

	if rr.Code != http.StatusGone {
		t.Fatalf("expected invalid token to fail, got %d body=%s", rr.Code, rr.Body.String())
	}
}
```

Add a second test for event output:

```go
func TestServePointerEventsStreamsSanitizedSnapshots(t *testing.T) {
	store := live.NewTokenStore(live.TokenStoreOptions{})
	token, _, err := store.Issue(live.IssueRequest{
		RuntimeSessionID: "rt_1",
		HandoffID:        "lv_1",
		Permission:       live.PermissionView,
		TTL:              time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &fakePointerBrowserRuntime{
		sub: browser.PointerSubscription{
			C: closedPointerChannel(browser.VirtualPointerSnapshot{
				X: 1, Y: 2, ViewportWidth: 100, ViewportHeight: 80, Visible: true,
			}),
			Close: func() {},
		},
	}
	handler := NewSessionControllerWithLive(SessionControllerOptions{
		Manager:    fakeSessionManagerWith("rt_1"),
		Browser:    runtime,
		LiveBaseURL: "http://browserd.test",
		TokenStore:  store,
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v/"+token+"/pointer-events", nil)

	handler.ServePointerEvents(rr, req, token)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected stream response, got %d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "event: pointer") || !strings.Contains(body, `"visible":true`) {
		t.Fatalf("expected pointer SSE body, got %s", body)
	}
	if strings.Contains(body, "rt_1") {
		t.Fatalf("pointer stream must not expose runtime session id: %s", body)
	}
}
```

If existing test helpers do not include `fakeSessionManagerWith`, create the smallest helper in the same test file rather than touching production code.

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/controller -run 'TestServePointerEvents' -count=1
```

Expected: FAIL because `ServePointerEvents`, `browserPointerRuntime`, and route support do not exist.

**Step 3: Write minimal controller implementation**

Modify `internal/controller/session_controller.go`:

```go
type browserPointerRuntime interface {
	SubscribePointer(runtimeSessionID string) (browser.PointerSubscription, error)
}

func (h *SessionController) ServePointerEvents(w http.ResponseWriter, r *http.Request, token string) {
	state, ok := h.tokenStore.Lookup(token)
	if !ok {
		types.WriteErr(w, http.StatusGone, "LIVE_TOKEN_EXPIRED", "live view token is expired or revoked")
		return
	}
	runtime, ok := h.browser.(browserPointerRuntime)
	if !ok {
		types.WriteErr(w, http.StatusServiceUnavailable, "POINTER_STREAM_NOT_AVAILABLE", "browser runtime does not expose pointer events")
		return
	}
	sub, err := runtime.SubscribePointer(state.RuntimeSessionID)
	if err != nil {
		writeBrowserErr(w, err)
		return
	}
	defer sub.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)
	for {
		select {
		case <-r.Context().Done():
			return
		case snapshot, ok := <-sub.C:
			if !ok {
				return
			}
			payload, _ := json.Marshal(snapshot)
			_, _ = fmt.Fprintf(w, "event: pointer\ndata: %s\n\n", payload)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}
```

**Step 4: Add router support**

Modify `internal/router/router.go` before the generic live-view route:

```go
case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, noVNCBasePath+"/") && strings.HasSuffix(r.URL.Path, "/pointer-events"):
	token := extractLiveViewToken(r.URL.Path, noVNCBasePath)
	if token == "" {
		types.WriteErr(w, http.StatusBadRequest, "INVALID_REQUEST", "missing live view token")
		return
	}
	handler.ServePointerEvents(w, r, token)
	return
```

**Step 5: Run tests to verify they pass**

Run:

```bash
go test ./internal/controller ./internal/router -run 'TestServePointerEvents|Test.*Live|Test.*Pointer' -count=1
```

Expected: PASS.

**Step 6: Commit**

```bash
git add internal/controller/session_controller.go internal/controller/session_controller_test.go internal/router/router.go internal/router/router_test.go
git commit -m "Expose live virtual pointer stream"
```

---

### Task 5: Render Pointer Overlay in Live Viewer

**Files:**
- Modify: `web/browser-live/src/main.ts`
- Modify: `web/browser-live/src/style.css`
- Modify: `web/browser-live/index.html`
- Modify: `web/browser-live/live-viewer.test.mjs`

**Step 1: Write failing live viewer source test**

Modify `web/browser-live/live-viewer.test.mjs`:

```js
assert.match(source, /new EventSource\(/, 'browser live viewer must subscribe to pointer events')
assert.match(source, /#virtual-pointer/, 'browser live viewer must render the virtual pointer overlay')
assert.match(source, /pointer-events:\s*none/, 'virtual pointer overlay must not intercept handoff input')
```

Also load `src/style.css` in the test:

```js
const style = readFileSync(join(import.meta.dirname, 'src/style.css'), 'utf8')
assert.match(style, /#virtual-pointer/, 'style must define visible virtual pointer')
assert.match(style, /pointer-events:\s*none/, 'virtual pointer must not block noVNC input')
```

**Step 2: Run test to verify it fails**

Run:

```bash
npm run test:live-viewer
```

Expected: FAIL because no pointer overlay exists.

**Step 3: Add overlay DOM**

Modify `web/browser-live/index.html`:

```html
<div id="screen"></div>
<div id="virtual-pointer" aria-hidden="true"></div>
```

**Step 4: Add overlay CSS**

Modify `web/browser-live/src/style.css`:

```css
#virtual-pointer {
  position: fixed;
  left: 0;
  top: 0;
  z-index: 20;
  width: 18px;
  height: 24px;
  pointer-events: none;
  opacity: 0;
  transform: translate3d(-100px, -100px, 0);
  transition:
    transform 80ms linear,
    opacity 120ms ease;
}

#virtual-pointer::before {
  content: '';
  position: absolute;
  left: 1px;
  top: 1px;
  width: 0;
  height: 0;
  border-left: 0 solid transparent;
  border-right: 10px solid transparent;
  border-bottom: 17px solid white;
  filter: drop-shadow(0 1px 2px rgb(0 0 0 / 80%));
  transform: rotate(-24deg);
}

#virtual-pointer::after {
  content: '';
  position: absolute;
  left: 2px;
  top: 2px;
  width: 0;
  height: 0;
  border-left: 0 solid transparent;
  border-right: 8px solid transparent;
  border-bottom: 14px solid #111;
  transform: rotate(-24deg);
}

#virtual-pointer[data-visible='true'] {
  opacity: 1;
}

#virtual-pointer[data-button-down='true'] {
  transform: translate3d(var(--pointer-x), var(--pointer-y), 0) scale(0.92);
}
```

**Step 5: Add pointer event client**

Modify `web/browser-live/src/main.ts`:

```ts
type PointerSnapshot = {
  x: number
  y: number
  viewportWidth: number
  viewportHeight: number
  visible: boolean
  buttonDown: boolean
}

const pointerEl = document.querySelector<HTMLDivElement>('#virtual-pointer')

function liveToken(): string {
  const explicit = new URLSearchParams(window.location.search).get('path')?.trim()
  if (explicit) {
    const match = explicit.match(/^\/?v\/([^/]+)/)
    if (match) return match[1]
  }
  const match = window.location.pathname.match(/^\/v\/([^/]+)/)
  if (!match) throw new Error('live token path is required')
  return match[1]
}

function pointerEventsPath(): string {
  return `v/${liveToken()}/pointer-events`
}

function mapPointer(snapshot: PointerSnapshot): { x: number; y: number } | null {
  if (!screenEl || snapshot.viewportWidth <= 0 || snapshot.viewportHeight <= 0) return null
  const canvas = screenEl.querySelector('canvas')
  const rect = (canvas ?? screenEl).getBoundingClientRect()
  return {
    x: rect.left + (snapshot.x / snapshot.viewportWidth) * rect.width,
    y: rect.top + (snapshot.y / snapshot.viewportHeight) * rect.height,
  }
}

function renderPointer(snapshot: PointerSnapshot) {
  if (!pointerEl) return
  const point = mapPointer(snapshot)
  if (!snapshot.visible || !point) {
    pointerEl.dataset.visible = 'false'
    return
  }
  pointerEl.style.setProperty('--pointer-x', `${point.x}px`)
  pointerEl.style.setProperty('--pointer-y', `${point.y}px`)
  pointerEl.dataset.visible = 'true'
  pointerEl.dataset.buttonDown = snapshot.buttonDown ? 'true' : 'false'
}

function connectPointerOverlay() {
  const events = new EventSource(`/${pointerEventsPath()}`)
  events.addEventListener('pointer', (event) => {
    renderPointer(JSON.parse((event as MessageEvent).data) as PointerSnapshot)
  })
  events.addEventListener('error', () => {
    if (pointerEl) pointerEl.dataset.visible = 'false'
  })
  window.addEventListener('beforeunload', () => events.close())
}
```

Call `connectPointerOverlay()` after `connect()` succeeds.

**Step 6: Run live viewer tests**

Run:

```bash
npm run test:live-viewer
```

Expected: PASS.

**Step 7: Run browser-live build**

Run:

```bash
npm run build
```

Expected: PASS and `internal/liveviewer/dist/` changes.

**Step 8: Commit**

```bash
git add web/browser-live internal/liveviewer/dist
git commit -m "Render virtual pointer in live viewer"
```

---

### Task 6: Verify Pointer Stream and Overlay Contract End-to-End

**Files:**
- Modify: `internal/controller/session_controller_test.go`
- Modify: `internal/router/router_test.go`
- Modify: `README.md`

**Step 1: Add route regression test**

Add to `internal/router/router_test.go`:

```go
func TestRouterRoutesPointerEventsBeforeGenericLiveView(t *testing.T) {
	source, err := os.ReadFile("router.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	pointerIndex := strings.Index(text, "/pointer-events")
	liveIndex := strings.Index(text, "handler.ServeLiveView")
	if pointerIndex < 0 || liveIndex < 0 || pointerIndex > liveIndex {
		t.Fatalf("pointer-events route must be checked before generic live-view route")
	}
}
```

**Step 2: Run test to verify it passes**

Run:

```bash
go test ./internal/router -run TestRouterRoutesPointerEventsBeforeGenericLiveView -count=1
```

Expected: PASS.

**Step 3: Update README live view notes**

Modify `README.md` under the Live View / Act section:

```markdown
- Live viewer renders a browserd virtual pointer overlay above the noVNC canvas.
- The overlay is driven by browserd `Act` pointer state and does not mutate the target page DOM.
- The overlay uses the existing live token path and does not change the `/v1/sessions/{runtimeSessionId}/act` API.
```

**Step 4: Run combined verification**

Run:

```bash
go test ./...
npm test
```

Expected: both commands PASS.

**Step 5: Commit**

```bash
git add internal/router/router_test.go README.md
git commit -m "Document virtual pointer live overlay"
```

---

### Task 7: Local Image Refresh and Manual Handoff Verification

**Files:**
- No source files unless verification reveals a defect.

**Step 1: Build the local image**

Run:

```bash
docker build -t ghcr.io/flaboy/browserd:sha-$(git rev-parse --short HEAD) -t browserd:dev .
```

Expected: image build exits 0.

**Step 2: Restart local browserd if using botworks compose**

Only if the local environment is currently running `botworks-browserd-1`, update the local compose tag in `/Users/wanglei/Projects/cybersailor/botworks/docker-compose.yml` and `/Users/wanglei/Projects/cybersailor/botworks/docker-compose.e2e.yml` to the new `sha-<commit>` tag. Do not commit botworks changes without explicit user approval because that repo's `AGENTS.md` requires confirmation for main-branch git operations.

Run:

```bash
docker compose up -d --no-deps browserd
curl -fsS http://localhost:7011/healthz
```

Expected: health response contains `"ok":true`.

**Step 3: Manual handoff visual check**

Create or reuse a browserd session with live view. Use an existing caller such as Hyacinth's browserd dev session script or a minimal browserd HTTP flow.

Expected checks:

- Live viewer opens.
- Before browserd performs an `Act`, no pointer overlay blocks user input.
- When browserd performs `Act(click)` or `Act(type)`, the overlay appears and moves from the previous virtual pointer location to the target.
- During text input, the overlay is visibly parked on the focused input/editor.
- The overlay does not appear inside platform page DOM inspection.
- Handoff window still closes on clean disconnect.

**Step 4: Record verification facts**

Record in the final response:

- Browserd commit hash.
- Commands run and their pass/fail result.
- Whether local image was rebuilt.
- Whether local running browserd was restarted.
- Whether manual handoff showed the pointer overlay.

**Step 5: Optional commit for local compose tag**

Only if the user explicitly asks to commit botworks local compose updates:

```bash
cd /Users/wanglei/Projects/cybersailor/botworks
git add docker-compose.yml docker-compose.e2e.yml
git commit -m "Update local browserd image tag"
```

---

## Acceptance Checklist

- [ ] `/v1/sessions/{runtimeSessionId}/act` request and response structs are unchanged.
- [ ] Browserd stores one virtual pointer state per runtime session.
- [ ] `Act(click)`, `Act(hover)`, `Act(type)`, and `Act(fill)` move from the previous virtual pointer position.
- [ ] Pointer movement updates are published to subscribers without blocking browser actions.
- [ ] Live pointer stream is protected by existing live tokens.
- [ ] Pointer stream does not expose runtime session id, cookies, profile paths, proxy values, fingerprints, or credentials.
- [ ] Live viewer renders pointer overlay above noVNC canvas.
- [ ] Pointer overlay uses `pointer-events: none` and does not block handoff input.
- [ ] Pointer overlay maps viewport coordinates to the displayed canvas dimensions.
- [ ] Pointer is not injected into the target platform page DOM.
- [ ] Handoff clean disconnect still closes the viewer.
- [ ] `go test ./...` passes.
- [ ] `npm test` passes.
- [ ] Local image rebuild succeeds.
- [ ] Local running browserd uses the rebuilt image when manual verification is performed.
