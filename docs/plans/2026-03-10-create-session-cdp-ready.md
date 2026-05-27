# Create Session CDP Ready Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 让 `POST /v1/sessions` 只有在 Chromium 已启动且 CDP websocket 已 ready 时才返回；若初始化失败，则同步返回错误并清理临时 session，不向调用方暴露不可用的 `runtimeSessionId`。

**Architecture:** 保持 `about:blank` 作为浏览器启动默认页面，不新增额外首屏导航。把“启动 Chromium + 等待 `DevToolsActivePort` + 建立 chromedp remote allocator”的 readiness 过程前移到 `CreateSession` 链路，由 controller 在 `manager.Create(...)` 成功后立即调用 browser runtime 的新 readiness 方法。若 readiness 失败，controller 必须关闭可能已启动的浏览器、删除刚创建的 session/profile 目录，再返回 `SESSION_INIT_FAILED`；成功时维持现有响应结构不变。

**Tech Stack:** Go, net/http, chromedp, Chromium headless,现有 `internal/controller`, `internal/browser`, `internal/session`

---

### Task 1: 先锁定 CreateSession 新契约

**Files:**
- Modify: `internal/controller/session_controller.go`
- Modify: `internal/controller/session_controller_test.go`

**Step 1: 写失败测试，要求 create 成功前必须通过 browser readiness**

在 `internal/controller/session_controller_test.go` 扩展 fake runtime 和 fake session manager，新增以下测试：

```go
func TestCreateSession_PreparesBrowserBeforeReturning(t *testing.T) {
	manager := &fakeSessionManager{
		createOut: session.CreateOutput{
			RuntimeSessionID: "rt_1",
			CDPWsURL:         "ws://browserd:9222/devtools/browser/rt_1",
			LeaseID:          "lease_1",
			ResolvedVersion:  "new",
		},
	}
	browserRuntime := &fakeBrowserRuntime{}
	controller := controller.NewSessionController(manager, browserRuntime, "ws://browserd:9222/devtools/browser")

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader([]byte(`{
		"s3ProfilePath":"s3://bucket/browser-sessions/t_1/c_1/bs_1/profile.tgz"
	}`)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	controller.CreateSession(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if browserRuntime.prepareCalls != []string{"rt_1"} {
		t.Fatalf("expected prepare to run before returning, got %+v", browserRuntime.prepareCalls)
	}
	if manager.deleteCalls != nil {
		t.Fatalf("did not expect delete on success, got %+v", manager.deleteCalls)
	}
}

func TestCreateSession_DeletesSessionWhenBrowserPrepareFails(t *testing.T) {
	manager := &fakeSessionManager{
		createOut: session.CreateOutput{
			RuntimeSessionID: "rt_1",
			CDPWsURL:         "ws://browserd:9222/devtools/browser/rt_1",
		},
	}
	browserRuntime := &fakeBrowserRuntime{
		prepareErr: errors.New("devtools websocket not ready"),
	}
	controller := controller.NewSessionController(manager, browserRuntime, "ws://browserd:9222/devtools/browser")

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader([]byte(`{
		"s3ProfilePath":"s3://bucket/browser-sessions/t_1/c_1/bs_1/profile.tgz"
	}`)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	controller.CreateSession(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", rr.Code, rr.Body.String())
	}
	if manager.deleteCalls != []string{"rt_1"} {
		t.Fatalf("expected session cleanup, got %+v", manager.deleteCalls)
	}
	if browserRuntime.closeCalls != []string{"rt_1"} {
		t.Fatalf("expected browser close on prepare failure, got %+v", browserRuntime.closeCalls)
	}
}
```

同时扩展 fake 类型，至少增加：

```go
type fakeBrowserRuntime struct {
	prepareErr   error
	prepareCalls []string
	closeCalls   []string
}

func (f *fakeBrowserRuntime) PrepareSession(runtimeSessionID string) error {
	f.prepareCalls = append(f.prepareCalls, runtimeSessionID)
	return f.prepareErr
}

func (f *fakeBrowserRuntime) Close(runtimeSessionID string) error {
	f.closeCalls = append(f.closeCalls, runtimeSessionID)
	return nil
}

type fakeSessionManager struct {
	createOut    session.CreateOutput
	createErr    error
	deleteCalls  []string
	deleteErr    error
}

func (f *fakeSessionManager) Delete(runtimeSessionID string) error {
	f.deleteCalls = append(f.deleteCalls, runtimeSessionID)
	return f.deleteErr
}
```

**Step 2: 运行测试，确认当前实现失败**

Run: `go test ./internal/controller -run 'TestCreateSession_(PreparesBrowserBeforeReturning|DeletesSessionWhenBrowserPrepareFails)'`

Expected: FAIL，因为当前 `browserRuntime` interface 还没有 `PrepareSession`，且 `CreateSession` 没有做 readiness/cleanup。

**Step 3: 最小实现 controller 契约**

在 `internal/controller/session_controller.go`：

```go
type browserRuntime interface {
	PrepareSession(runtimeSessionID string) error
	Close(runtimeSessionID string) error
	Navigate(runtimeSessionID string, input browser.NavigateInput) (browser.NavigateOutput, error)
	Snapshot(runtimeSessionID string, input browser.SnapshotInput) (browser.SnapshotOutput, error)
	Act(runtimeSessionID string, input browser.ActInput) (browser.ActOutput, error)
	Screenshot(runtimeSessionID string, input browser.ScreenshotInput) (browser.ScreenshotOutput, error)
}
```

并在 `CreateSession` 成功拿到 `out` 之后补 readiness：

```go
	if h.browser != nil {
		if err := h.browser.PrepareSession(out.RuntimeSessionID); err != nil {
			_ = h.browser.Close(out.RuntimeSessionID)
			_ = h.manager.Delete(out.RuntimeSessionID)
			types.WriteErr(w, http.StatusServiceUnavailable, "SESSION_INIT_FAILED", err.Error())
			return
		}
	}
```

要求：
- 成功时继续返回原始 `CreateOutput`
- 失败时不要把 `runtimeSessionId` 返回给调用方
- 失败清理以 best-effort 执行，不覆盖主错误

**Step 4: 重跑 controller 测试**

Run: `go test ./internal/controller -run 'TestCreateSession_(PreparesBrowserBeforeReturning|DeletesSessionWhenBrowserPrepareFails)'`

Expected: PASS

**Step 5: 提交**

```bash
git add internal/controller/session_controller.go internal/controller/session_controller_test.go
git commit -m "feat: make session creation wait for browser readiness"
```

### Task 2: 把 readiness 变成 browser service 的显式能力

**Files:**
- Modify: `internal/browser/service.go`
- Modify: `internal/browser/service_test.go`

**Step 1: 写失败测试，锁定 readiness helper 行为**

在 `internal/browser/service_test.go` 新增两个小测试，先把已有隐式逻辑显式化：

```go
func TestWaitForDevToolsWS_ReturnsWebSocketURLFromActivePortFile(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "DevToolsActivePort"), []byte("12345\n/devtools/browser/abc\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	got, err := waitForDevToolsWS(dir, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("expected ready websocket, got %v", err)
	}
	if got != "ws://127.0.0.1:12345/devtools/browser/abc" {
		t.Fatalf("unexpected ws url: %s", got)
	}
}

func TestBuildChromeArgs_KeepsAboutBlankBootstrapPage(t *testing.T) {
	args := buildChromeArgs("/tmp/profile")
	if args[len(args)-1] != "about:blank" {
		t.Fatalf("expected about:blank bootstrap page, got %+v", args)
	}
}
```

**Step 2: 跑测试确认基线**

Run: `go test ./internal/browser -run 'Test(WaitForDevToolsWS_ReturnsWebSocketURLFromActivePortFile|BuildChromeArgs_KeepsAboutBlankBootstrapPage)'`

Expected: 第一条可能 FAIL（当前无覆盖），第二条 PASS 或补齐后 PASS。

**Step 3: 写最小实现，暴露 PrepareSession**

在 `internal/browser/service.go` 增加显式方法，直接复用现有 `ensureBrowser`：

```go
func (s *Service) PrepareSession(runtimeSessionID string) error {
	_, err := s.ensureBrowser(runtimeSessionID)
	return err
}
```

不要新增额外 `Navigate("about:blank")`，原因：
- `buildChromeArgs(...)` 已经把 `about:blank` 作为启动页
- 真正缺的是“create 必须等待 readiness”，不是缺首页 URL

**Step 4: 重跑 browser 单测**

Run: `go test ./internal/browser`

Expected: PASS

**Step 5: 提交**

```bash
git add internal/browser/service.go internal/browser/service_test.go
git commit -m "feat: expose browser session readiness preparation"
```

### Task 3: 端到端锁定失败回滚，不留下坏 session

**Files:**
- Modify: `internal/controller/session_controller_test.go`
- Modify: `internal/session/manager_test.go`

**Step 1: 写失败测试，确认 create 失败后 session 不可再被 Get**

在 `internal/controller/session_controller_test.go` 新增一条集成风格小测试，直接用真实 `session.NewManager(...)` 和 fake browser runtime：

```go
func TestCreateSession_PrepareFailureRemovesRuntimeSessionFromManager(t *testing.T) {
	manager := session.NewManager(session.ManagerOptions{
		Store:      profile.NewMemoryStore(),
		Workdir:    t.TempDir(),
		CDPBaseURL: "ws://browserd:9222/devtools/browser",
	})
	browserRuntime := &fakeBrowserRuntime{prepareErr: errors.New("devtools websocket not ready")}
	controller := controller.NewSessionController(manager, browserRuntime, "ws://browserd:9222/devtools/browser")

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader([]byte(`{
		"s3ProfilePath":"s3://bucket/browser-sessions/t_1/c_1/bs_1/profile.tgz"
	}`)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	controller.CreateSession(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", rr.Code, rr.Body.String())
	}
	for _, runtimeSessionID := range browserRuntime.prepareCalls {
		if _, err := manager.Get(runtimeSessionID); err == nil {
			t.Fatalf("expected failed create session to be removed: %s", runtimeSessionID)
		}
	}
}
```

**Step 2: 跑测试确认失败**

Run: `go test ./internal/controller -run TestCreateSession_PrepareFailureRemovesRuntimeSessionFromManager`

Expected: FAIL，直到 controller 真的调用 `manager.Delete(...)`

**Step 3: 必要时补最小实现**

如果 Task 1 的实现已满足，这一步只修正测试中暴露的遗漏；禁止再引入第二套清理路径。唯一清理入口保持在 `CreateSession` 的 prepare failure 分支。

**Step 4: 重跑 controller 与 session 测试**

Run: `go test ./internal/controller ./internal/session`

Expected: PASS

**Step 5: 提交**

```bash
git add internal/controller/session_controller_test.go internal/session/manager_test.go
git commit -m "test: verify failed session creation rolls back runtime session"
```

### Task 4: 更新接口文档与回归验证

**Files:**
- Modify: `README.md`
- Modify: `e2e/smoke_test.go`
- Modify: `e2e/read_page_smoke_test.go`
- Modify: `docs/plans/2026-03-10-create-session-cdp-ready.md`

**Step 1: 更新 README 契约说明**

在 `README.md` 的 Create 小节补充：

```md
`POST /v1/sessions` 是同步初始化接口：
- 返回 200 前，Chromium 已启动且 DevTools websocket 已 ready
- Chromium 仍以 `about:blank` 启动，不额外执行首屏 navigate
- 若 readiness 失败，接口返回 `503 SESSION_INIT_FAILED`
- 失败时不会保留可继续使用的 `runtimeSessionId`
```

**Step 2: 补 smoke 断言**

在 `e2e/smoke_test.go` 和 `e2e/read_page_smoke_test.go` 中，创建成功后直接继续 `navigate`，不增加额外 sleep；用现有链路证明 create 返回即 ready。

**Step 3: 运行完整验证**

Run: `go test ./...`

Expected: PASS

**Step 4: 核对清单，避免遗漏/不一致/多余项**

核对以下事项：
- `about:blank` 仍然只在 Chrome 启动参数中出现一次
- `POST /v1/sessions` 已变为同步 readiness 契约
- create failure 会清理 browser 与 session
- `navigate` 不再承担首次启动 Chromium 的责任
- README 与测试口径一致

**Step 5: 提交**

```bash
git add README.md e2e/smoke_test.go e2e/read_page_smoke_test.go docs/plans/2026-03-10-create-session-cdp-ready.md
git commit -m "docs: describe synchronous browser session initialization"
```
