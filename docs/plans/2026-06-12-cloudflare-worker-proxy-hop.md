# Cloudflare Worker Proxy Hop Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add an optional Cloudflare Worker first-hop proxy path so browserd can route Chromium traffic through Worker before connecting to the configured upstream proxy, bypassing k8s-to-proxy-provider connectivity issues.

**Architecture:** Keep Chromium pointed at a per-session local HTTP proxy inside browserd. When `BROWSERD_PROXY_HOP=cloudflare-worker` is enabled and a session has `proxyServer`, browserd starts a local adapter that accepts Chromium HTTP proxy traffic, opens a WebSocket tunnel to the Worker, and instructs the Worker to connect to the configured upstream HTTP or SOCKS5 proxy. The target site still sees the upstream proxy exit IP; Worker is only the network hop between browserd and the upstream proxy.

**Tech Stack:** Go 1.24, `net/http`, `net`, `golang.org/x/net/websocket` or existing `github.com/gobwas/ws`, Chromium `--proxy-server`, Cloudflare Workers WebSocket + TCP sockets, existing `internal/browser`, `internal/config`, `internal/router`, `internal/session`

---

### Task 1: Lock Config Contract For Proxy Hop

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- No code in other packages yet.

**Step 1: Write failing config tests**

Add tests to `internal/config/config_test.go`:

```go
func TestLoad_CloudflareWorkerProxyHopConfig(t *testing.T) {
	t.Setenv("BROWSERD_PROXY_HOP", "cloudflare-worker")
	t.Setenv("BROWSERD_PROXY_WORKER_URL", "wss://proxy-hop.example.workers.dev/tunnel")
	t.Setenv("BROWSERD_PROXY_WORKER_TOKEN", "worker-token")

	cfg := Load()

	if cfg.ProxyHop != "cloudflare-worker" {
		t.Fatalf("unexpected proxy hop: %q", cfg.ProxyHop)
	}
	if cfg.ProxyWorkerURL != "wss://proxy-hop.example.workers.dev/tunnel" {
		t.Fatalf("unexpected worker url: %q", cfg.ProxyWorkerURL)
	}
	if cfg.ProxyWorkerToken != "worker-token" {
		t.Fatalf("unexpected worker token")
	}
}

func TestLoad_DefaultProxyHopDisabled(t *testing.T) {
	cfg := Load()
	if cfg.ProxyHop != "" || cfg.ProxyWorkerURL != "" || cfg.ProxyWorkerToken != "" {
		t.Fatalf("expected proxy hop disabled by default, got %+v", cfg)
	}
}
```

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/config -run 'TestLoad_.*ProxyHop' -v
```

Expected: FAIL because `Config` has no proxy hop fields.

**Step 3: Add minimal config fields**

In `internal/config/config.go`, extend `Config`:

```go
ProxyHop         string
ProxyWorkerURL   string
ProxyWorkerToken string
```

In `Load()`, parse:

```go
proxyHop := strings.ToLower(strings.TrimSpace(os.Getenv("BROWSERD_PROXY_HOP")))
proxyWorkerURL := strings.TrimSpace(os.Getenv("BROWSERD_PROXY_WORKER_URL"))
proxyWorkerToken := strings.TrimSpace(os.Getenv("BROWSERD_PROXY_WORKER_TOKEN"))
```

Return these fields.

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./internal/config -run 'TestLoad_.*ProxyHop' -v
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: add proxy hop config"
```

Do not commit unless the user has approved git operations for this browserd branch.

---

### Task 2: Model Proxy Hop Options In Browser Service

**Files:**
- Modify: `internal/browser/service.go`
- Modify: `internal/browser/service_test.go`
- Modify: `internal/router/router.go`

**Step 1: Write failing service option test**

In `internal/browser/service_test.go`, add:

```go
func TestNewService_AcceptsProxyHopOptions(t *testing.T) {
	svc := NewServiceWithOptions(ServiceOptions{
		Sessions: session.NewManager(session.ManagerOptions{
			Store:      profile.NewMemoryStore(),
			Workdir:    t.TempDir(),
			CDPBaseURL: "ws://browserd:9222/devtools/browser",
		}),
		State: browserrt.NewState(),
		ProxyHop: ProxyHopOptions{
			Mode:       "cloudflare-worker",
			WorkerURL:  "wss://proxy-hop.example.workers.dev/tunnel",
			WorkerToken: "worker-token",
		},
	})

	if svc.proxyHop.Mode != "cloudflare-worker" {
		t.Fatalf("proxy hop was not stored: %+v", svc.proxyHop)
	}
}
```

Add imports as needed: `browserd/internal/profile`, `browserd/internal/session`.

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/browser -run TestNewService_AcceptsProxyHopOptions -v
```

Expected: FAIL because `ServiceOptions`, `ProxyHopOptions`, and `NewServiceWithOptions` do not exist.

**Step 3: Implement options without behavior change**

In `internal/browser/service.go`, add:

```go
type ProxyHopOptions struct {
	Mode        string
	WorkerURL   string
	WorkerToken string
}

type ServiceOptions struct {
	Sessions session.Manager
	State    *browserrt.State
	Assets   assets.Store
	ProxyHop ProxyHopOptions
}
```

Add `proxyHop ProxyHopOptions` to `Service`.

Keep the existing constructor by delegating:

```go
func NewService(sessions session.Manager, state *browserrt.State, assetStore assets.Store) *Service {
	return NewServiceWithOptions(ServiceOptions{
		Sessions: sessions,
		State:    state,
		Assets:   assetStore,
	})
}

func NewServiceWithOptions(opts ServiceOptions) *Service {
	state := opts.State
	if state == nil {
		state = browserrt.NewState()
	}
	return &Service{
		sessions:   opts.Sessions,
		state:      state,
		assets:     opts.Assets,
		proxyHop:   opts.ProxyHop,
		capturePNG: capturePagePNG,
		browsers:   map[string]*activeBrowser{},
	}
}
```

**Step 4: Wire router config**

In `internal/router/router.go`, replace:

```go
browserSvc := browser.NewService(manager, runtime.NewState(), assetStore)
```

with:

```go
browserSvc := browser.NewServiceWithOptions(browser.ServiceOptions{
	Sessions: manager,
	State:    runtime.NewState(),
	Assets:   assetStore,
	ProxyHop: browser.ProxyHopOptions{
		Mode:        cfg.ProxyHop,
		WorkerURL:   cfg.ProxyWorkerURL,
		WorkerToken: cfg.ProxyWorkerToken,
	},
})
```

**Step 5: Run focused tests**

Run:

```bash
go test ./internal/browser -run 'TestNewService_AcceptsProxyHopOptions|TestBuildChromeArgs_AppliesFingerprintAndProxyOptions' -v
go test ./internal/router -v
```

Expected: PASS.

**Step 6: Commit**

```bash
git add internal/browser/service.go internal/browser/service_test.go internal/router/router.go
git commit -m "feat: wire proxy hop options"
```

Do not commit unless the user has approved git operations for this browserd branch.

---

### Task 3: Define Worker Tunnel Protocol Types

**Files:**
- Create: `internal/browser/proxy_hop.go`
- Create: `internal/browser/proxy_hop_test.go`

**Step 1: Write failing tests for protocol envelope**

Create `internal/browser/proxy_hop_test.go`:

```go
package browser

import (
	"encoding/json"
	"testing"
)

func TestProxyHopOpenRequestMasksUpstreamProxy(t *testing.T) {
	proxy, err := ParseProxyServer("http://user:pass@proxy.example.com:8080")
	if err != nil {
		t.Fatalf("parse proxy: %v", err)
	}
	req := NewProxyHopOpenRequest("connect-1", "news.163.com:443", proxy)

	if req.Type != "open" || req.ID != "connect-1" || req.Target != "news.163.com:443" {
		t.Fatalf("unexpected open request: %+v", req)
	}
	if req.UpstreamProxy != "http://proxy.example.com:8080" {
		t.Fatalf("unexpected upstream proxy: %q", req.UpstreamProxy)
	}
	if req.UpstreamProxyAuth == nil || req.UpstreamProxyAuth.Username != "user" || req.UpstreamProxyAuth.Password != "pass" {
		t.Fatalf("missing auth: %+v", req.UpstreamProxyAuth)
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) == "" || containsSecret(string(b), "user:pass@") {
		t.Fatalf("serialized request must not contain credential URL: %s", string(b))
	}
}

func containsSecret(s, secret string) bool {
	return strings.Contains(s, secret)
}
```

Add `strings` import in the test.

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/browser -run TestProxyHopOpenRequestMasksUpstreamProxy -v
```

Expected: FAIL because protocol types do not exist.

**Step 3: Implement protocol types**

Create `internal/browser/proxy_hop.go`:

```go
package browser

type proxyHopAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type proxyHopOpenRequest struct {
	Type              string        `json:"type"`
	ID                string        `json:"id"`
	Target            string        `json:"target"`
	UpstreamProxy     string        `json:"upstreamProxy"`
	UpstreamProxyAuth *proxyHopAuth `json:"upstreamProxyAuth,omitempty"`
}

type proxyHopOpenResponse struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func NewProxyHopOpenRequest(id string, target string, proxy ProxyConfig) proxyHopOpenRequest {
	req := proxyHopOpenRequest{
		Type:          "open",
		ID:            id,
		Target:        target,
		UpstreamProxy: proxy.ChromeServer,
	}
	if proxy.HasAuth() {
		req.UpstreamProxyAuth = &proxyHopAuth{Username: proxy.Username, Password: proxy.Password}
	}
	return req
}
```

Keep these types unexported except `NewProxyHopOpenRequest` only if tests need it. If possible, make tests package `browser` and keep helper unexported as `newProxyHopOpenRequest`.

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./internal/browser -run TestProxyHopOpenRequestMasksUpstreamProxy -v
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/browser/proxy_hop.go internal/browser/proxy_hop_test.go
git commit -m "feat: define proxy hop protocol"
```

Do not commit unless the user has approved git operations for this browserd branch.

---

### Task 4: Add Local HTTP Proxy Adapter Skeleton

**Files:**
- Create: `internal/browser/local_proxy_adapter.go`
- Create: `internal/browser/local_proxy_adapter_test.go`
- Modify: `internal/browser/service.go`

**Step 1: Write failing lifecycle test**

Create `internal/browser/local_proxy_adapter_test.go`:

```go
package browser

import (
	"context"
	"net"
	"net/http"
	"testing"
)

func TestLocalProxyAdapter_StartReturnsLoopbackProxyServer(t *testing.T) {
	proxy, err := ParseProxyServer("http://user:pass@proxy.example.com:8080")
	if err != nil {
		t.Fatal(err)
	}
	adapter := newLocalProxyAdapter(localProxyAdapterOptions{
		RuntimeSessionID: "rt_1",
		UpstreamProxy:   proxy,
		WorkerURL:       "wss://worker.example/tunnel",
		WorkerToken:     "token",
	})
	if err := adapter.Start(context.Background()); err != nil {
		t.Fatalf("start adapter: %v", err)
	}
	defer adapter.Close()

	server := adapter.ChromeProxyServer()
	if server == "" {
		t.Fatalf("expected chrome proxy server")
	}
	if _, _, err := net.SplitHostPort(strings.TrimPrefix(server, "http://")); err != nil {
		t.Fatalf("expected host:port proxy server, got %q: %v", server, err)
	}
}
```

Add missing `strings` import. Remove unused `net/http` if not needed after implementation.

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/browser -run TestLocalProxyAdapter_StartReturnsLoopbackProxyServer -v
```

Expected: FAIL because adapter does not exist.

**Step 3: Implement adapter lifecycle only**

Create `internal/browser/local_proxy_adapter.go` with:

```go
type localProxyAdapterOptions struct {
	RuntimeSessionID string
	UpstreamProxy   ProxyConfig
	WorkerURL       string
	WorkerToken     string
}

type localProxyAdapter struct {
	opts   localProxyAdapterOptions
	ln     net.Listener
	server *http.Server
}

func newLocalProxyAdapter(opts localProxyAdapterOptions) *localProxyAdapter {
	return &localProxyAdapter{opts: opts}
}

func (a *localProxyAdapter) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	a.ln = ln
	a.server = &http.Server{Handler: http.HandlerFunc(a.handleHTTP)}
	go func() {
		_ = a.server.Serve(ln)
	}()
	return nil
}

func (a *localProxyAdapter) ChromeProxyServer() string {
	if a == nil || a.ln == nil {
		return ""
	}
	return "http://" + a.ln.Addr().String()
}

func (a *localProxyAdapter) Close() error {
	if a == nil || a.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return a.server.Shutdown(ctx)
}

func (a *localProxyAdapter) handleHTTP(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "proxy hop not implemented", http.StatusBadGateway)
}
```

**Step 4: Extend activeBrowser for cleanup**

In `internal/browser/service.go`, extend:

```go
type activeBrowser struct {
	...
	proxyAdapter *localProxyAdapter
}
```

Do not wire behavior yet.

**Step 5: Run lifecycle tests**

Run:

```bash
go test ./internal/browser -run 'TestLocalProxyAdapter_StartReturnsLoopbackProxyServer|TestBuildChromeArgs_AppliesFingerprintAndProxyOptions' -v
```

Expected: PASS.

**Step 6: Commit**

```bash
git add internal/browser/local_proxy_adapter.go internal/browser/local_proxy_adapter_test.go internal/browser/service.go
git commit -m "feat: add local proxy adapter lifecycle"
```

Do not commit unless the user has approved git operations for this browserd branch.

---

### Task 5: Wire Proxy Hop Into Chromium Launch

**Files:**
- Modify: `internal/browser/service.go`
- Modify: `internal/browser/service_test.go`

**Step 1: Write failing build-args test for local adapter proxy**

In `internal/browser/service_test.go`, add:

```go
func TestBuildChromeArgs_UsesProxyHopLocalProxy(t *testing.T) {
	proxy, err := ParseProxyServer("http://user:pass@proxy.example.com:8080")
	if err != nil {
		t.Fatal(err)
	}
	args := buildChromeArgs(BrowserOptions{
		UserDataDir: "/tmp/profile",
		Headless:    true,
		Proxy:       proxy,
		ProxyOverrideServer: "http://127.0.0.1:34567",
	})

	if !containsArg(args, "--proxy-server=http://127.0.0.1:34567") {
		t.Fatalf("expected local proxy override in args: %+v", args)
	}
	if containsArg(args, "--proxy-server=http://proxy.example.com:8080") {
		t.Fatalf("did not expect upstream proxy in chrome args: %+v", args)
	}
}
```

Add helper:

```go
func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
```

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/browser -run TestBuildChromeArgs_UsesProxyHopLocalProxy -v
```

Expected: FAIL because `ProxyOverrideServer` does not exist.

**Step 3: Add BrowserOptions proxy override**

In `internal/browser/service.go`, extend:

```go
type BrowserOptions struct {
	UserDataDir         string
	Headless            bool
	Fingerprint         FingerprintConfig
	Proxy               ProxyConfig
	ProxyOverrideServer string
}
```

Update `buildChromeArgs`:

```go
proxyServer := opts.Proxy.ChromeServer
if opts.ProxyOverrideServer != "" {
	proxyServer = opts.ProxyOverrideServer
}
if proxyServer != "" {
	args = append(args, "--proxy-server="+proxyServer)
}
```

**Step 4: Wire adapter before Chrome start**

In `ensureBrowser`, after `ParseProxyServer(info.ProxyServer)` and before `exec.Command`:

```go
var proxyAdapter *localProxyAdapter
proxyOverride := ""
if s.shouldUseCloudflareWorkerHop(proxy) {
	proxyAdapter = newLocalProxyAdapter(localProxyAdapterOptions{
		RuntimeSessionID: runtimeSessionID,
		UpstreamProxy:   proxy,
		WorkerURL:       s.proxyHop.WorkerURL,
		WorkerToken:     s.proxyHop.WorkerToken,
	})
	if err := proxyAdapter.Start(context.Background()); err != nil {
		if liveRuntime != nil {
			_ = liveRuntime.Stop(context.Background())
		}
		return nil, fmt.Errorf("%w: proxy hop adapter start failed: %v", ErrProxyHopFailed, err)
	}
	proxyOverride = proxyAdapter.ChromeProxyServer()
}
```

Add sentinel error:

```go
var ErrProxyHopFailed = errors.New("proxy hop failed")
```

Add helper:

```go
func (s *Service) shouldUseCloudflareWorkerHop(proxy ProxyConfig) bool {
	return proxy.Raw != "" &&
		strings.EqualFold(strings.TrimSpace(s.proxyHop.Mode), "cloudflare-worker") &&
		strings.TrimSpace(s.proxyHop.WorkerURL) != "" &&
		strings.TrimSpace(s.proxyHop.WorkerToken) != ""
}
```

Pass `ProxyOverrideServer: proxyOverride` into `buildChromeArgs`, and store `proxyAdapter` in `activeBrowser`.

**Step 5: Cleanup adapter on every failure path and Close**

In `ensureBrowser`, whenever startup fails after adapter start, call `_ = proxyAdapter.Close()`.

In `Service.Close`, add:

```go
if b.proxyAdapter != nil {
	_ = b.proxyAdapter.Close()
}
```

**Step 6: Run tests**

Run:

```bash
go test ./internal/browser -run 'TestBuildChromeArgs_UsesProxyHopLocalProxy|TestBuildChromeArgs_AppliesFingerprintAndProxyOptions' -v
go test ./internal/browser -v
```

Expected: PASS.

**Step 7: Commit**

```bash
git add internal/browser/service.go internal/browser/service_test.go
git commit -m "feat: route chromium through local proxy hop"
```

Do not commit unless the user has approved git operations for this browserd branch.

---

### Task 6: Implement Worker WebSocket Tunnel Client

**Files:**
- Create: `internal/browser/proxy_hop_client.go`
- Create: `internal/browser/proxy_hop_client_test.go`
- Modify: `internal/browser/local_proxy_adapter.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Step 1: Choose WebSocket library**

Prefer existing dependency `github.com/gobwas/ws` because it is already in `go.mod` indirectly. If using the standard Go ecosystem requires a new dependency, confirm before adding it.

**Step 2: Write failing tunnel client test**

Create `internal/browser/proxy_hop_client_test.go` with an in-memory `httptest.Server` WebSocket endpoint that:

- Validates `Authorization: Bearer worker-token`.
- Reads the first JSON text frame.
- Expects `type=open`, `target=proxy-auth-check.local:80`, and upstream proxy details.
- Sends `{"type":"open_result","id":"...","ok":true}`.
- Echoes subsequent binary frames.

Test skeleton:

```go
func TestProxyHopClient_OpenSendsAuthAndOpenRequest(t *testing.T) {
	server := newTestWorkerTunnelServer(t)
	defer server.Close()

	proxy, err := ParseProxyServer("http://user:pass@proxy.example.com:8080")
	if err != nil {
		t.Fatal(err)
	}
	client := newProxyHopClient(proxyHopClientOptions{
		WorkerURL:   wsURL(server.URL),
		WorkerToken: "worker-token",
	})
	conn, err := client.Open(context.Background(), "connect-1", "proxy-auth-check.local:80", proxy)
	if err != nil {
		t.Fatalf("open tunnel: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("unexpected echo: %q", string(buf))
	}
}
```

**Step 3: Run test to verify it fails**

Run:

```bash
go test ./internal/browser -run TestProxyHopClient_OpenSendsAuthAndOpenRequest -v
```

Expected: FAIL because client does not exist.

**Step 4: Implement tunnel client**

Create `internal/browser/proxy_hop_client.go`.

Required behavior:

- Dial `WorkerURL` via WebSocket.
- Set `Authorization: Bearer <token>`.
- Send first text frame as JSON `proxyHopOpenRequest`.
- Read one text frame `proxyHopOpenResponse`.
- If `ok=false`, return `ErrProxyHopFailed` with worker error detail.
- Return a `net.Conn`-like wrapper over the WebSocket for binary frames.

Keep the wrapper minimal: implement `Read`, `Write`, `Close`, `SetDeadline`, `LocalAddr`, `RemoteAddr` only as needed by adapter tests. If the library does not expose `net.Conn` semantics cleanly, create a narrow interface:

```go
type proxyHopTunnel interface {
	io.ReadWriteCloser
}
```

**Step 5: Run client tests**

Run:

```bash
go test ./internal/browser -run 'TestProxyHopClient_' -v
```

Expected: PASS.

**Step 6: Commit**

```bash
git add internal/browser/proxy_hop_client.go internal/browser/proxy_hop_client_test.go go.mod go.sum
git commit -m "feat: add worker proxy hop client"
```

Do not commit unless the user has approved git operations for this browserd branch.

---

### Task 7: Implement Local Adapter CONNECT And HTTP Forwarding

**Files:**
- Modify: `internal/browser/local_proxy_adapter.go`
- Modify: `internal/browser/local_proxy_adapter_test.go`

**Step 1: Write failing CONNECT test**

In `internal/browser/local_proxy_adapter_test.go`, add a fake tunnel opener:

```go
type fakeTunnelOpener struct {
	targets []string
	client  net.Conn
	server  net.Conn
}

func newFakeTunnelOpener() *fakeTunnelOpener {
	client, server := net.Pipe()
	return &fakeTunnelOpener{client: client, server: server}
}

func (f *fakeTunnelOpener) Open(ctx context.Context, id string, target string, proxy ProxyConfig) (io.ReadWriteCloser, error) {
	f.targets = append(f.targets, target)
	return f.client, nil
}
```

Then test:

```go
func TestLocalProxyAdapter_ConnectOpensWorkerTunnel(t *testing.T) {
	opener := newFakeTunnelOpener()
	defer opener.server.Close()
	proxy, _ := ParseProxyServer("http://user:pass@proxy.example.com:8080")
	adapter := newLocalProxyAdapter(localProxyAdapterOptions{
		RuntimeSessionID: "rt_1",
		UpstreamProxy:   proxy,
		TunnelOpener:    opener,
	})
	if err := adapter.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()

	conn, err := net.Dial("tcp", strings.TrimPrefix(adapter.ChromeProxyServer(), "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	fmt.Fprintf(conn, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %s", resp.Status)
	}
	if !reflect.DeepEqual(opener.targets, []string{"example.com:443"}) {
		t.Fatalf("unexpected targets: %+v", opener.targets)
	}
}
```

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/browser -run TestLocalProxyAdapter_ConnectOpensWorkerTunnel -v
```

Expected: FAIL because adapter does not open tunnels.

**Step 3: Implement CONNECT**

In `local_proxy_adapter.go`:

- Detect `r.Method == http.MethodConnect`.
- Call tunnel opener with `r.Host`.
- On success, hijack the client connection.
- Write `HTTP/1.1 200 Connection Established\r\n\r\n`.
- Start bidirectional copy:

```go
func pipeBoth(a io.ReadWriteCloser, b io.ReadWriteCloser) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(a, b); _ = a.Close() }()
	go func() { defer wg.Done(); _, _ = io.Copy(b, a); _ = b.Close() }()
	wg.Wait()
}
```

Do not buffer or inspect tunneled TLS bytes.

**Step 4: Write and implement HTTP absolute-form forwarding test**

Add test:

```go
func TestLocalProxyAdapter_HTTPAbsoluteFormUsesTargetHost(t *testing.T) {
	// Send: GET http://proxy-auth-check.local/path HTTP/1.1
	// Expect opener target: proxy-auth-check.local:80
	// Expect first bytes written to tunnel start with: GET /path HTTP/1.1
}
```

Implementation:

- For non-CONNECT proxy requests, derive target from `r.URL`.
- Open tunnel to `host:port`.
- Rewrite request line to origin-form before writing to tunnel.
- Remove hop-by-hop headers: `Proxy-Authorization`, `Proxy-Connection`, `Connection`.

**Step 5: Run adapter tests**

Run:

```bash
go test ./internal/browser -run 'TestLocalProxyAdapter_' -v
```

Expected: PASS.

**Step 6: Commit**

```bash
git add internal/browser/local_proxy_adapter.go internal/browser/local_proxy_adapter_test.go
git commit -m "feat: forward chromium proxy traffic through worker tunnel"
```

Do not commit unless the user has approved git operations for this browserd branch.

---

### Task 8: Add Worker Reference Implementation

**Files:**
- Create: `worker/proxy-hop-worker.js`
- Create: `worker/README.md`

**Step 1: Write Worker README first**

Create `worker/README.md` documenting:

- This Worker is a WebSocket tunnel endpoint, not a browser-facing HTTP proxy.
- Required secret: `PROXY_HOP_TOKEN`.
- Request path: `/tunnel`.
- First message from browserd is JSON:

```json
{
  "type": "open",
  "id": "connect-id",
  "target": "news.163.com:443",
  "upstreamProxy": "http://proxy.example.com:8080",
  "upstreamProxyAuth": {
    "username": "user",
    "password": "pass"
  }
}
```

- Worker connects to the upstream proxy, performs HTTP CONNECT or SOCKS5 handshake, then pipes bytes.

**Step 2: Implement Worker**

Create `worker/proxy-hop-worker.js`.

Required behavior:

- Reject non-`/tunnel` path with 404.
- Require `Upgrade: websocket`.
- Require `Authorization: Bearer <PROXY_HOP_TOKEN>`.
- Accept WebSocket.
- Read first text message.
- Parse upstream proxy URL.
- For `http://` upstream:
  - `connect({ hostname, port })` to upstream proxy.
  - Send `CONNECT ${target} HTTP/1.1`.
  - Include `Proxy-Authorization: Basic ...` if auth exists.
  - Read response headers.
  - If status is not 200, send `open_result ok=false`.
- For `socks5://` upstream:
  - Connect to upstream proxy.
  - Send SOCKS5 greeting.
  - If auth exists, offer username/password method.
  - Complete username/password auth.
  - Send connect request to target host/port.
  - Validate success response.
- After open success:
  - Send `{"type":"open_result","id":"...","ok":true}`.
  - Pipe WebSocket binary messages to TCP writer.
  - Pipe TCP reader chunks back as WebSocket binary messages.

**Step 3: Add Worker non-secret logging rules**

Worker logs may include:

- connection id
- upstream scheme
- upstream host
- target host
- failure code

Worker logs must not include:

- upstream username
- upstream password
- `Proxy-Authorization`
- full upstream proxy URL with credentials

**Step 4: Syntax check without Docker**

Run:

```bash
npm --version
node --check worker/proxy-hop-worker.js
```

Expected: `node --check` exits 0.

Use `npm` only if adding Worker tooling later. Do not use `pnpm`. Do not use local Docker.

**Step 5: Commit**

```bash
git add worker/proxy-hop-worker.js worker/README.md
git commit -m "feat: add cloudflare worker proxy hop"
```

Do not commit unless the user has approved git operations for this browserd branch.

---

### Task 9: Map Errors To Fast-Fail Messages

**Files:**
- Modify: `internal/browser/proxy_hop_client.go`
- Modify: `internal/browser/local_proxy_adapter.go`
- Modify: `internal/controller/session_controller.go`
- Modify: `internal/controller/session_controller_test.go`

**Step 1: Define error sentinels**

In `internal/browser/proxy_hop.go` or `internal/browser/service.go`:

```go
var (
	ErrProxyHopConfigMissing    = errors.New("proxy hop config missing")
	ErrProxyHopConnectFailed    = errors.New("proxy hop connect failed")
	ErrUpstreamProxyAuthFailed  = errors.New("upstream proxy auth failed")
	ErrUpstreamProxyConnectFail = errors.New("upstream proxy connect failed")
)
```

**Step 2: Write failing controller test**

In `internal/controller/session_controller_test.go`, add:

```go
func TestCreateSession_ProxyHopFailureReturnsSpecificError(t *testing.T) {
	manager := newTestManager(t)
	browserRuntime := &fakeBrowserRuntime{
		prepareErr: browser.ErrProxyHopConnectFailed,
	}
	handler := NewSessionControllerWithLive(SessionControllerOptions{
		Manager: manager,
		Browser: browserRuntime,
		CDPBaseURL: "ws://browserd:9222/devtools/browser",
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader([]byte(`{
		"s3ProfilePath":"s3://tmp/proxy-hop/profile.tgz",
		"fingerprint":{"seed":"fp_seed_1","locale":"en-US","languages":["en-US","en"],"acceptLanguage":"en-US,en;q=0.9","timezone":"America/New_York","platform":"Win32","os":"Windows","userAgent":"Mozilla/5.0 test","viewportWidth":1366,"viewportHeight":768,"screenWidth":1366,"screenHeight":768,"deviceScaleFactor":1,"hardwareConcurrency":8,"deviceMemory":8,"webglVendor":"Google Inc.","webglRenderer":"ANGLE Test"},
		"proxyServer":"http://user:pass@proxy.example.com:8080"
	}`)))
	handler.CreateSession(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "PROXY_HOP_CONNECT_FAILED") {
		t.Fatalf("expected proxy hop error, got %s", rr.Body.String())
	}
}
```

Adjust helper names to match existing tests.

**Step 3: Run test to verify it fails**

Run:

```bash
go test ./internal/controller -run TestCreateSession_ProxyHopFailureReturnsSpecificError -v
```

Expected: FAIL until controller maps sentinel errors.

**Step 4: Implement mapping**

In `CreateSession`, inside `PrepareSession` error branch:

```go
switch {
case errors.Is(err, browser.ErrProxyHopConfigMissing):
	types.WriteErr(w, http.StatusServiceUnavailable, "PROXY_HOP_CONFIG_MISSING", err.Error())
case errors.Is(err, browser.ErrProxyHopConnectFailed):
	types.WriteErr(w, http.StatusServiceUnavailable, "PROXY_HOP_CONNECT_FAILED", err.Error())
case errors.Is(err, browser.ErrUpstreamProxyAuthFailed):
	types.WriteErr(w, http.StatusServiceUnavailable, "UPSTREAM_PROXY_AUTH_FAILED", err.Error())
case errors.Is(err, browser.ErrUpstreamProxyConnectFail):
	types.WriteErr(w, http.StatusServiceUnavailable, "UPSTREAM_PROXY_CONNECT_FAILED", err.Error())
case errors.Is(err, browser.ErrFingerprintInitFailed):
	types.WriteErr(w, http.StatusServiceUnavailable, "FINGERPRINT_INIT_FAILED", err.Error())
default:
	types.WriteErr(w, http.StatusServiceUnavailable, "SESSION_INIT_FAILED", err.Error())
}
```

**Step 5: Run controller tests**

Run:

```bash
go test ./internal/controller -run 'TestCreateSession_' -v
```

Expected: PASS.

**Step 6: Commit**

```bash
git add internal/browser/proxy_hop.go internal/browser/proxy_hop_client.go internal/browser/local_proxy_adapter.go internal/controller/session_controller.go internal/controller/session_controller_test.go
git commit -m "feat: return proxy hop failures explicitly"
```

Do not commit unless the user has approved git operations for this browserd branch.

---

### Task 10: Add End-To-End K8s Validation Without Local Docker

**Files:**
- Create: `docs/proxy-hop-validation.md`
- No production manifest changes in this task.

**Step 1: Document validation topology**

Create `docs/proxy-hop-validation.md`:

```markdown
# Proxy Hop Validation

This validation uses temporary k8s pods only. It does not use local Docker.

Topology:

Chromium in temporary browserd pod
-> browserd local adapter
-> Cloudflare Worker WebSocket
-> Worker outbound TCP
-> temporary authenticated proxy pod
-> test target
```

**Step 2: Add temporary authenticated proxy pod manifest snippet**

Document a `kubectl apply -f -` snippet that starts one pod with:

- HTTP authenticated proxy on `18080`
- SOCKS5 authenticated proxy on `18081`
- internal test target `proxy-auth-check.local`

Use the same Python proxy script pattern validated during investigation. Keep credentials as test-only `proxyuser/proxypass`.

**Step 3: Add temporary browserd pod manifest snippet**

Document a `kubectl apply -f -` snippet that starts:

- image under test
- `BROWSERD_PORT=7011`
- `BROWSERD_PROFILE_STORE=local`
- `BROWSERD_PROXY_HOP=cloudflare-worker`
- `BROWSERD_PROXY_WORKER_URL=wss://<worker>/tunnel`
- `BROWSERD_PROXY_WORKER_TOKEN=<token>`

**Step 4: Add create-session validation commands**

Document commands:

```bash
/Users/wanglei/Library/bin/kubectl --context octopus-01 -n aiworker exec browserd-proxy-hop-test -- sh -lc 'python3 /tmp/create_session_and_navigate.py http'
/Users/wanglei/Library/bin/kubectl --context octopus-01 -n aiworker exec browserd-proxy-hop-test -- sh -lc 'python3 /tmp/create_session_and_navigate.py socks5'
```

Expected:

- HTTP upstream returns title `Proxy Auth Check`.
- SOCKS5 upstream returns title `Proxy Auth Check`.
- No local Docker commands are required.

**Step 5: Add cleanup commands**

Document:

```bash
/Users/wanglei/Library/bin/kubectl --context octopus-01 -n aiworker delete pod browserd-proxy-hop-test proxy-auth-test --ignore-not-found=true
/Users/wanglei/Library/bin/kubectl --context octopus-01 -n aiworker delete svc proxy-auth-test --ignore-not-found=true
/Users/wanglei/Library/bin/kubectl --context octopus-01 -n aiworker delete configmap proxy-auth-test-script --ignore-not-found=true
```

**Step 6: Commit**

```bash
git add docs/proxy-hop-validation.md
git commit -m "docs: add proxy hop validation procedure"
```

Do not commit unless the user has approved git operations for this browserd branch.

---

### Task 11: Run Full Verification

**Files:**
- All files touched above.

**Step 1: Run Go tests**

Run:

```bash
go test ./...
```

Expected: PASS.

**Step 2: Run Worker syntax check**

Run:

```bash
node --check worker/proxy-hop-worker.js
```

Expected: exit 0.

**Step 3: Run k8s temporary-pod validation**

Only after the Worker endpoint is deployed or supplied for testing, run the commands from `docs/proxy-hop-validation.md`.

Expected:

- HTTP upstream through Worker returns `Proxy Auth Check`.
- SOCKS5 upstream through Worker returns `Proxy Auth Check`.
- Proxy pod logs show authenticated upstream proxy handshakes.
- browserd pod logs contain no proxy password.

**Step 4: Confirm no local Docker usage**

Run:

```bash
history | tail -80
```

Expected: no local `docker` command used during implementation or validation.

If shell history is not reliable in the current environment, state that this check could not be proven from history and list the actual commands used.

**Step 5: Review git diff**

Run:

```bash
git diff --stat
git diff -- internal/browser internal/config internal/router internal/controller worker docs
```

Expected:

- Changes are limited to proxy hop config, tunnel implementation, Worker reference, docs, and tests.
- No production deployment manifest changes are included.
- No secrets are committed.

---

### Task 12: Final Plan Review Before Any Commit Or Release

**Files:**
- `docs/plans/2026-06-12-cloudflare-worker-proxy-hop.md`
- All files touched by implementation.

**Step 1: Review checklist**

Confirm each item:

- [ ] Existing direct HTTP proxy path remains the default.
- [ ] Worker hop only activates when `BROWSERD_PROXY_HOP=cloudflare-worker`, Worker URL/token are set, and session has `proxyServer`.
- [ ] Chromium only receives a local `127.0.0.1:<port>` proxy when Worker hop is active.
- [ ] HTTP upstream proxy authentication works through Worker.
- [ ] SOCKS5 upstream username/password authentication works through Worker.
- [ ] `https://` proxy scheme remains rejected unless explicitly redesigned.
- [ ] Worker logs do not expose proxy credentials.
- [ ] browserd logs do not expose proxy credentials.
- [ ] Local adapter closes when session closes and on every Chrome startup failure.
- [ ] `go test ./...` passes.
- [ ] k8s temporary-pod validation passes for HTTP and SOCKS5.
- [ ] Temporary k8s resources are deleted.
- [ ] No local Docker command was used.

**Step 2: Decide release path**

Do not trigger GitHub Actions, publish images, update deployments, or modify production manifests without explicit user confirmation.

When the user approves release:

1. Push the browserd branch.
2. Trigger or wait for the repository's approved GitHub Actions build.
3. Confirm the new `ghcr.io/flaboy/browserd:sha-<shortsha>` image exists.
4. Ask for explicit approval before changing any production deployment image.
5. Use `/Users/wanglei/Library/bin/kubectl` for rollout and post-rollout validation.

**Step 3: Commit plan if requested**

```bash
git add docs/plans/2026-06-12-cloudflare-worker-proxy-hop.md
git commit -m "docs: plan cloudflare worker proxy hop"
```

Do not commit unless the user explicitly approves git operations.
