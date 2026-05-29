package controller_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"browserd/internal/browser"
	"browserd/internal/config"
	"browserd/internal/controller"
	"browserd/internal/live"
	"browserd/internal/profile"
	"browserd/internal/router"
	"browserd/internal/session"
)

type fakeBrowserRuntime struct {
	prepareErr    error
	prepareCalls  []string
	closeCalls    []string
	navigateCalls []browser.NavigateInput
	navigateOut   browser.NavigateOutput
	navigateErr   error
	snapshotOut   browser.SnapshotOutput
	snapshotErr   error
	actOut        browser.ActOutput
	actErr        error
	screenshotOut browser.ScreenshotOutput
	screenshotErr error
	evaluateCalls []browser.EvaluateInput
	evaluateOut   browser.EvaluateOutput
	evaluateErr   error
}

type fakeLiveProxyBrowserRuntime struct {
	fakeBrowserRuntime
	target    string
	targetErr error
}

func (f *fakeLiveProxyBrowserRuntime) LiveProxyTarget(_ string) (string, error) {
	if f.targetErr != nil {
		return "", f.targetErr
	}
	return f.target, nil
}

func (f *fakeBrowserRuntime) PrepareSession(runtimeSessionID string) error {
	f.prepareCalls = append(f.prepareCalls, runtimeSessionID)
	return f.prepareErr
}

func (f *fakeBrowserRuntime) Close(runtimeSessionID string) error {
	f.closeCalls = append(f.closeCalls, runtimeSessionID)
	return nil
}

func (f *fakeBrowserRuntime) Navigate(_ string, input browser.NavigateInput) (browser.NavigateOutput, error) {
	f.navigateCalls = append(f.navigateCalls, input)
	return f.navigateOut, f.navigateErr
}

func (f *fakeBrowserRuntime) Snapshot(_ string, _ browser.SnapshotInput) (browser.SnapshotOutput, error) {
	return f.snapshotOut, f.snapshotErr
}

func (f *fakeBrowserRuntime) Act(_ string, _ browser.ActInput) (browser.ActOutput, error) {
	return f.actOut, f.actErr
}

func (f *fakeBrowserRuntime) Screenshot(_ string, _ browser.ScreenshotInput) (browser.ScreenshotOutput, error) {
	return f.screenshotOut, f.screenshotErr
}

func (f *fakeBrowserRuntime) Evaluate(_ string, input browser.EvaluateInput) (browser.EvaluateOutput, error) {
	f.evaluateCalls = append(f.evaluateCalls, input)
	return f.evaluateOut, f.evaluateErr
}

type fakeSessionManager struct {
	createOut   session.CreateOutput
	createErr   error
	deleteCalls []string
	deleteErr   error
}

func (f *fakeSessionManager) Create(_ session.CreateInput) (session.CreateOutput, error) {
	return f.createOut, f.createErr
}

func (f *fakeSessionManager) Commit(_ string, _ session.CommitInput) (session.CommitOutput, error) {
	return session.CommitOutput{}, errors.New("not implemented")
}

func (f *fakeSessionManager) Delete(runtimeSessionID string) error {
	f.deleteCalls = append(f.deleteCalls, runtimeSessionID)
	return f.deleteErr
}

func (f *fakeSessionManager) Get(_ string) (session.SessionInfo, error) {
	return session.SessionInfo{}, session.ErrSessionNotFound
}

func TestCreateSession_ReturnsCdpWsUrlAndLeaseEcho(t *testing.T) {
	manager := session.NewManager(session.ManagerOptions{
		Store:      profile.NewMemoryStore(),
		Workdir:    t.TempDir(),
		CDPBaseURL: "ws://browserd:9222/devtools/browser",
	})
	handler := controller.NewSessionController(manager, &fakeBrowserRuntime{}, "ws://browserd:9222/devtools/browser")

	body := []byte(`{
		"s3ProfilePath":"s3://bucket/browser-sessions/t_1/c_1/bs_1/profile.tgz",
		"leaseId":"lease_1"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.CreateSession(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	data := out["data"].(map[string]any)
	if data["leaseId"] != "lease_1" {
		t.Fatalf("leaseId mismatch: %+v", data)
	}
	cdp := data["cdpWsUrl"].(string)
	if !strings.HasPrefix(cdp, "ws://browserd:9222/devtools/browser/rt_") {
		t.Fatalf("unexpected cdpWsUrl: %s", cdp)
	}
}

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
	if len(browserRuntime.prepareCalls) != 1 || browserRuntime.prepareCalls[0] != "rt_1" {
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
	if len(manager.deleteCalls) != 1 || manager.deleteCalls[0] != "rt_1" {
		t.Fatalf("expected session cleanup, got %+v", manager.deleteCalls)
	}
	if len(browserRuntime.closeCalls) != 1 || browserRuntime.closeCalls[0] != "rt_1" {
		t.Fatalf("expected browser close on prepare failure, got %+v", browserRuntime.closeCalls)
	}
}

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

func TestCommitSession_ValidatesIfMatchVersion(t *testing.T) {
	manager := session.NewManager(session.ManagerOptions{
		Store:      profile.NewMemoryStore(),
		Workdir:    t.TempDir(),
		CDPBaseURL: "ws://browserd:9222/devtools/browser",
	})
	handler := controller.NewSessionController(manager, &fakeBrowserRuntime{}, "ws://browserd:9222/devtools/browser")

	create := []byte(`{
		"s3ProfilePath":"s3://bucket/browser-sessions/t_1/c_1/bs_1/profile.tgz"
	}`)
	creq := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(create))
	creq.Header.Set("Content-Type", "application/json")
	crr := httptest.NewRecorder()
	handler.CreateSession(crr, creq)
	if crr.Code != http.StatusOK {
		t.Fatalf("create expected 200, got %d body=%s", crr.Code, crr.Body.String())
	}

	var cbody map[string]any
	_ = json.Unmarshal(crr.Body.Bytes(), &cbody)
	rid := cbody["data"].(map[string]any)["runtimeSessionId"].(string)

	commit := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+rid+"/commit", bytes.NewReader(commit))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.CommitSession(rr, req, rid)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestCommitSession_Returns409OnVersionConflict(t *testing.T) {
	manager := session.NewManager(session.ManagerOptions{
		Store:      profile.NewMemoryStore(),
		Workdir:    t.TempDir(),
		CDPBaseURL: "ws://browserd:9222/devtools/browser",
	})
	handler := controller.NewSessionController(manager, &fakeBrowserRuntime{}, "ws://browserd:9222/devtools/browser")

	create := []byte(`{
		"s3ProfilePath":"s3://bucket/browser-sessions/t_1/c_1/bs_1/profile.tgz"
	}`)
	creq := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(create))
	creq.Header.Set("Content-Type", "application/json")
	crr := httptest.NewRecorder()
	handler.CreateSession(crr, creq)
	if crr.Code != http.StatusOK {
		t.Fatalf("create expected 200, got %d body=%s", crr.Code, crr.Body.String())
	}

	var cbody map[string]any
	_ = json.Unmarshal(crr.Body.Bytes(), &cbody)
	rid := cbody["data"].(map[string]any)["runtimeSessionId"].(string)

	// first commit with matching version succeeds and moves version forward
	commit1 := []byte(`{"ifMatchVersion":"new"}`)
	req1 := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+rid+"/commit", bytes.NewReader(commit1))
	req1.Header.Set("Content-Type", "application/json")
	rr1 := httptest.NewRecorder()
	handler.CommitSession(rr1, req1, rid)
	if rr1.Code != http.StatusOK {
		t.Fatalf("first commit expected 200, got %d body=%s", rr1.Code, rr1.Body.String())
	}

	// stale ifMatchVersion should conflict
	commit2 := []byte(`{"ifMatchVersion":"new"}`)
	req2 := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+rid+"/commit", bytes.NewReader(commit2))
	req2.Header.Set("Content-Type", "application/json")
	rr2 := httptest.NewRecorder()
	handler.CommitSession(rr2, req2, rid)
	if rr2.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rr2.Code, rr2.Body.String())
	}
}

func TestNavigate_ForwardsAfterLoadScreenshotS3Path(t *testing.T) {
	browserRuntime := &fakeBrowserRuntime{
		navigateOut: browser.NavigateOutput{
			URL:             "https://news.163.com/",
			Title:           "News",
			SnapshotCleared: true,
		},
	}
	handler := controller.NewSessionController(&fakeSessionManager{}, browserRuntime, "ws://browserd:9222/devtools/browser")

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/rt_1/navigate", bytes.NewReader([]byte(`{
		"url":"https://news.163.com",
		"waitUntil":"load",
		"afterLoadScreenshotS3Path":"s3://browserd-snapshots/team_1/conv_1/1737373333.png"
	}`)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.Navigate(rr, req, "rt_1")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if len(browserRuntime.navigateCalls) != 1 {
		t.Fatalf("expected one navigate call, got %+v", browserRuntime.navigateCalls)
	}
	got := browserRuntime.navigateCalls[0]
	if got.AfterLoadScreenshotS3Path != "s3://browserd-snapshots/team_1/conv_1/1737373333.png" {
		t.Fatalf("unexpected afterLoadScreenshotS3Path: %+v", got)
	}
	if got.URL != "https://news.163.com" || got.WaitUntil != "load" {
		t.Fatalf("unexpected navigate input: %+v", got)
	}
}

func TestNavigateRoute_RejectsUnknownSession(t *testing.T) {
	h := router.New(config.Config{Port: 7011, CDPBaseURL: "ws://browserd:9222/devtools/browser"})

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/rt_missing/navigate", bytes.NewReader([]byte(`{"url":"https://example.com"}`)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestSnapshotRoute_UsesPageAsSingleStructure(t *testing.T) {
	controller := controller.NewSessionController(&fakeSessionManager{}, &fakeBrowserRuntime{
		snapshotOut: browser.SnapshotOutput{
			SnapshotID: "snap_1",
			Page: browser.PageSnapshot{
				URL:   "https://example.com",
				Title: "Example",
				Groups: map[string]browser.PageTable{
					"buttons": {
						Columns: []string{"ref", "tag", "text"},
						Rows:    [][]any{{"e1", "BUTTON", "Submit"}},
					},
				},
			},
		},
	}, "ws://browserd:9222/devtools/browser")

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/rt_1/snapshot?mode=refs", nil)
	rr := httptest.NewRecorder()
	controller.Snapshot(rr, req, "rt_1")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	data := body["data"].(map[string]any)
	if _, ok := data["page"]; !ok {
		t.Fatalf("expected page field, got %+v", data)
	}
	if _, ok := data["refs"]; ok {
		t.Fatalf("expected refs field to be removed: %+v", data)
	}
	if _, ok := data["text"]; ok {
		t.Fatalf("expected text field to be removed: %+v", data)
	}
}

func TestHandoffStart_ReturnsControlViewerURL(t *testing.T) {
	manager := session.NewManager(session.ManagerOptions{
		Store:      profile.NewMemoryStore(),
		Workdir:    t.TempDir(),
		CDPBaseURL: "ws://browserd:9222/devtools/browser",
	})
	handler := controller.NewSessionControllerWithLive(controller.SessionControllerOptions{
		Manager:      manager,
		Browser:      &fakeBrowserRuntime{},
		CDPBaseURL:   "ws://browserd:9222/devtools/browser",
		LiveBaseURL:  "https://browser.example",
		LiveTokenTTL: 15 * time.Minute,
		TokenStore:   live.NewTokenStore(live.TokenStoreOptions{}),
	})
	rid := createTestSession(t, handler)

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+rid+"/handoff/start", bytes.NewReader([]byte(`{
		"permission":"control",
		"ttlSeconds":900
	}`)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.StartHandoff(rr, req, rid)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	data := decodeData(t, rr)
	if !strings.HasPrefix(data["handoffId"].(string), "ho_") {
		t.Fatalf("expected handoff id, got %+v", data)
	}
	if !strings.HasPrefix(data["viewerUrl"].(string), "https://browser.example/v/") {
		t.Fatalf("unexpected viewerUrl: %+v", data)
	}
	if data["permission"] != "control" {
		t.Fatalf("unexpected permission: %+v", data)
	}
	if data["expiresAt"] == "" {
		t.Fatalf("expected expiresAt: %+v", data)
	}
}

func TestLiveView_ReturnsViewOnlyViewerURL(t *testing.T) {
	manager := session.NewManager(session.ManagerOptions{
		Store:      profile.NewMemoryStore(),
		Workdir:    t.TempDir(),
		CDPBaseURL: "ws://browserd:9222/devtools/browser",
	})
	handler := controller.NewSessionControllerWithLive(controller.SessionControllerOptions{
		Manager:      manager,
		Browser:      &fakeBrowserRuntime{},
		CDPBaseURL:   "ws://browserd:9222/devtools/browser",
		LiveBaseURL:  "https://browser.example/",
		LiveTokenTTL: 15 * time.Minute,
		TokenStore:   live.NewTokenStore(live.TokenStoreOptions{}),
	})
	rid := createTestSession(t, handler)

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+rid+"/live-view", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.LiveView(rr, req, rid)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	data := decodeData(t, rr)
	if data["permission"] != "view" {
		t.Fatalf("unexpected permission: %+v", data)
	}
	if !strings.HasPrefix(data["viewerUrl"].(string), "https://browser.example/v/") {
		t.Fatalf("unexpected viewerUrl: %+v", data)
	}
}

func TestEvaluate_ReturnsJSONResult(t *testing.T) {
	manager := session.NewManager(session.ManagerOptions{
		Store:      profile.NewMemoryStore(),
		Workdir:    t.TempDir(),
		CDPBaseURL: "ws://browserd:9222/devtools/browser",
	})
	browserRuntime := &fakeBrowserRuntime{
		evaluateOut: browser.EvaluateOutput{
			Result: map[string]any{"title": "Example"},
			URL:    "https://example.com/",
			Title:  "Example",
		},
	}
	handler := controller.NewSessionControllerWithLive(controller.SessionControllerOptions{
		Manager:    manager,
		Browser:    browserRuntime,
		CDPBaseURL: "ws://browserd:9222/devtools/browser",
	})
	rid := createTestSession(t, handler)

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+rid+"/evaluate", bytes.NewReader([]byte(`{
		"script":"return { title: document.title }",
		"args":["ok"],
		"timeoutMs":1000
	}`)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.Evaluate(rr, req, rid)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	data := decodeData(t, rr)
	result := data["result"].(map[string]any)
	if result["title"] != "Example" {
		t.Fatalf("unexpected result: %+v", data)
	}
	if len(browserRuntime.evaluateCalls) != 1 {
		t.Fatalf("expected one evaluate call, got %d", len(browserRuntime.evaluateCalls))
	}
	if browserRuntime.evaluateCalls[0].Script != "return { title: document.title }" {
		t.Fatalf("unexpected script: %+v", browserRuntime.evaluateCalls[0])
	}
}

func TestHandoffComplete_RevokesViewerToken(t *testing.T) {
	manager := session.NewManager(session.ManagerOptions{
		Store:      profile.NewMemoryStore(),
		Workdir:    t.TempDir(),
		CDPBaseURL: "ws://browserd:9222/devtools/browser",
	})
	handler := controller.NewSessionControllerWithLive(controller.SessionControllerOptions{
		Manager:      manager,
		Browser:      &fakeBrowserRuntime{},
		CDPBaseURL:   "ws://browserd:9222/devtools/browser",
		LiveBaseURL:  "https://browser.example",
		LiveTokenTTL: 15 * time.Minute,
		TokenStore:   live.NewTokenStore(live.TokenStoreOptions{}),
	})
	rid := createTestSession(t, handler)

	startReq := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+rid+"/handoff/start", bytes.NewReader([]byte(`{"permission":"control"}`)))
	startRR := httptest.NewRecorder()
	handler.StartHandoff(startRR, startReq, rid)
	if startRR.Code != http.StatusOK {
		t.Fatalf("expected start 200, got %d body=%s", startRR.Code, startRR.Body.String())
	}
	startData := decodeData(t, startRR)
	handoffID := startData["handoffId"].(string)
	token := liveTokenFromTestViewerURL(t, startData["viewerUrl"].(string))

	liveReq := httptest.NewRequest(http.MethodGet, "/v/"+token+"/", nil)
	liveRR := httptest.NewRecorder()
	handler.ServeLiveView(liveRR, liveReq, token)
	if liveRR.Code != http.StatusOK {
		t.Fatalf("expected live token before complete, got %d body=%s", liveRR.Code, liveRR.Body.String())
	}

	completeReq := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+rid+"/handoff/"+handoffID+"/complete", nil)
	completeRR := httptest.NewRecorder()
	handler.CompleteHandoff(completeRR, completeReq, rid, handoffID)
	if completeRR.Code != http.StatusOK {
		t.Fatalf("expected complete 200, got %d body=%s", completeRR.Code, completeRR.Body.String())
	}

	revokedRR := httptest.NewRecorder()
	handler.ServeLiveView(revokedRR, liveReq, token)
	if revokedRR.Code != http.StatusGone {
		t.Fatalf("expected revoked token to return 410, got %d body=%s", revokedRR.Code, revokedRR.Body.String())
	}
}

func TestServeLiveView_ReturnsUnhealthyWhenLiveProxyTargetFails(t *testing.T) {
	manager := session.NewManager(session.ManagerOptions{
		Store:      profile.NewMemoryStore(),
		Workdir:    t.TempDir(),
		CDPBaseURL: "ws://browserd:9222/devtools/browser",
	})
	handler := controller.NewSessionControllerWithLive(controller.SessionControllerOptions{
		Manager:      manager,
		Browser:      &fakeLiveProxyBrowserRuntime{targetErr: browser.ErrLiveRuntimeUnhealthy},
		CDPBaseURL:   "ws://browserd:9222/devtools/browser",
		LiveBaseURL:  "https://browser.example",
		LiveTokenTTL: 15 * time.Minute,
		TokenStore:   live.NewTokenStore(live.TokenStoreOptions{}),
	})
	rid := createTestSession(t, handler)
	token := startControlHandoff(t, handler, rid)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v/"+token+"/vnc.html", nil)
	handler.ServeLiveView(rr, req, token)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected unhealthy live proxy to return 503, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "LIVE_RUNTIME_UNHEALTHY") {
		t.Fatalf("expected LIVE_RUNTIME_UNHEALTHY body, got %s", rr.Body.String())
	}
}

func TestHandoffControlDisconnectAutoCompletesAfterGrace(t *testing.T) {
	proxy, release := newBlockingLiveProxy(t)
	defer proxy.Close()

	manager := session.NewManager(session.ManagerOptions{
		Store:      profile.NewMemoryStore(),
		Workdir:    t.TempDir(),
		CDPBaseURL: "ws://browserd:9222/devtools/browser",
	})
	browserRuntime := &fakeLiveProxyBrowserRuntime{
		target: proxy.URL,
		fakeBrowserRuntime: fakeBrowserRuntime{
			navigateOut: browser.NavigateOutput{URL: "https://example.com/", Title: "Example"},
		},
	}
	handler := controller.NewSessionControllerWithLive(controller.SessionControllerOptions{
		Manager:                manager,
		Browser:                browserRuntime,
		CDPBaseURL:             "ws://browserd:9222/devtools/browser",
		LiveBaseURL:            "https://browser.example",
		LiveTokenTTL:           15 * time.Minute,
		HandoffDisconnectGrace: 25 * time.Millisecond,
		TokenStore:             live.NewTokenStore(live.TokenStoreOptions{}),
	})
	rid := createTestSession(t, handler)
	token := startControlHandoff(t, handler, rid)

	done := serveLiveWebsockify(t, handler, token)
	release()
	<-done

	assertNavigateStatus(t, handler, rid, http.StatusConflict)
	eventually(t, 500*time.Millisecond, func() bool {
		return navigateStatus(handler, rid) == http.StatusOK
	})

	revokedRR := httptest.NewRecorder()
	handler.ServeLiveView(revokedRR, httptest.NewRequest(http.MethodGet, "/v/"+token+"/", nil), token)
	if revokedRR.Code != http.StatusGone {
		t.Fatalf("expected auto-completed token to return 410, got %d body=%s", revokedRR.Code, revokedRR.Body.String())
	}
}

func TestHandoffControlReconnectCancelsAutoComplete(t *testing.T) {
	proxy, releaseFirst := newBlockingLiveProxy(t)
	defer proxy.Close()

	handler, rid := newLiveHandoffTestController(t, proxy.URL, 50*time.Millisecond)
	token := startControlHandoff(t, handler, rid)

	firstDone := serveLiveWebsockify(t, handler, token)
	releaseFirst()
	<-firstDone

	_, releaseSecond := replaceBlockingLiveProxyHandler(t, proxy)
	secondDone := serveLiveWebsockify(t, handler, token)
	time.Sleep(80 * time.Millisecond)
	assertNavigateStatus(t, handler, rid, http.StatusConflict)

	releaseSecond()
	<-secondDone
	eventually(t, 500*time.Millisecond, func() bool {
		return navigateStatus(handler, rid) == http.StatusOK
	})
}

func TestHandoffExplicitCompleteCancelsDisconnectTimer(t *testing.T) {
	proxy, release := newBlockingLiveProxy(t)
	defer proxy.Close()

	handler, rid := newLiveHandoffTestController(t, proxy.URL, 50*time.Millisecond)
	startReq := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+rid+"/handoff/start", bytes.NewReader([]byte(`{"permission":"control"}`)))
	startRR := httptest.NewRecorder()
	handler.StartHandoff(startRR, startReq, rid)
	if startRR.Code != http.StatusOK {
		t.Fatalf("expected start 200, got %d body=%s", startRR.Code, startRR.Body.String())
	}
	startData := decodeData(t, startRR)
	token := liveTokenFromTestViewerURL(t, startData["viewerUrl"].(string))
	handoffID := startData["handoffId"].(string)

	done := serveLiveWebsockify(t, handler, token)
	release()
	<-done

	completeReq := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+rid+"/handoff/"+handoffID+"/complete", nil)
	completeRR := httptest.NewRecorder()
	handler.CompleteHandoff(completeRR, completeReq, rid, handoffID)
	if completeRR.Code != http.StatusOK {
		t.Fatalf("expected complete 200, got %d body=%s", completeRR.Code, completeRR.Body.String())
	}
	time.Sleep(80 * time.Millisecond)
	assertNavigateStatus(t, handler, rid, http.StatusOK)
}

func TestViewOnlyLiveViewDisconnectDoesNotCompleteActiveHandoff(t *testing.T) {
	proxy, release := newBlockingLiveProxy(t)
	defer proxy.Close()

	handler, rid := newLiveHandoffTestController(t, proxy.URL, 25*time.Millisecond)
	_ = startControlHandoff(t, handler, rid)

	liveReq := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+rid+"/live-view", bytes.NewReader([]byte(`{}`)))
	liveRR := httptest.NewRecorder()
	handler.LiveView(liveRR, liveReq, rid)
	if liveRR.Code != http.StatusOK {
		t.Fatalf("expected live view 200, got %d body=%s", liveRR.Code, liveRR.Body.String())
	}
	viewToken := liveTokenFromTestViewerURL(t, decodeData(t, liveRR)["viewerUrl"].(string))

	done := serveLiveWebsockify(t, handler, viewToken)
	release()
	<-done
	time.Sleep(60 * time.Millisecond)
	assertNavigateStatus(t, handler, rid, http.StatusConflict)
}

func newLiveHandoffTestController(t *testing.T, target string, grace time.Duration) (*controller.SessionController, string) {
	t.Helper()
	manager := session.NewManager(session.ManagerOptions{
		Store:      profile.NewMemoryStore(),
		Workdir:    t.TempDir(),
		CDPBaseURL: "ws://browserd:9222/devtools/browser",
	})
	handler := controller.NewSessionControllerWithLive(controller.SessionControllerOptions{
		Manager:                manager,
		Browser:                &fakeLiveProxyBrowserRuntime{target: target, fakeBrowserRuntime: fakeBrowserRuntime{navigateOut: browser.NavigateOutput{URL: "https://example.com/"}}},
		CDPBaseURL:             "ws://browserd:9222/devtools/browser",
		LiveBaseURL:            "https://browser.example",
		LiveTokenTTL:           15 * time.Minute,
		HandoffDisconnectGrace: grace,
		TokenStore:             live.NewTokenStore(live.TokenStoreOptions{}),
	})
	return handler, createTestSession(t, handler)
}

func newBlockingLiveProxy(t *testing.T) (*httptest.Server, func()) {
	t.Helper()
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/websockify" {
			http.NotFound(w, r)
			return
		}
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	return server, func() {
		close(release)
	}
}

func replaceBlockingLiveProxyHandler(t *testing.T, server *httptest.Server) (*httptest.Server, func()) {
	t.Helper()
	release := make(chan struct{})
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/websockify" {
			http.NotFound(w, r)
			return
		}
		<-release
		w.WriteHeader(http.StatusOK)
	})
	return server, func() {
		close(release)
	}
}

func startControlHandoff(t *testing.T, handler *controller.SessionController, rid string) string {
	t.Helper()
	startReq := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+rid+"/handoff/start", bytes.NewReader([]byte(`{"permission":"control"}`)))
	startRR := httptest.NewRecorder()
	handler.StartHandoff(startRR, startReq, rid)
	if startRR.Code != http.StatusOK {
		t.Fatalf("expected start 200, got %d body=%s", startRR.Code, startRR.Body.String())
	}
	return liveTokenFromTestViewerURL(t, decodeData(t, startRR)["viewerUrl"].(string))
}

func serveLiveWebsockify(t *testing.T, handler *controller.SessionController, token string) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v/"+token+"/websockify", nil)
		handler.ServeLiveView(rr, req, token)
	}()
	return done
}

func assertNavigateStatus(t *testing.T, handler *controller.SessionController, rid string, want int) {
	t.Helper()
	if got := navigateStatus(handler, rid); got != want {
		t.Fatalf("expected navigate status %d, got %d", want, got)
	}
}

func navigateStatus(handler *controller.SessionController, rid string) int {
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+rid+"/navigate", bytes.NewReader([]byte(`{"url":"https://example.com"}`)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.Navigate(rr, req, rid)
	return rr.Code
}

func eventually(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition was not met within %s", timeout)
}

func liveTokenFromTestViewerURL(t *testing.T, viewerURL string) string {
	t.Helper()
	withoutBase := strings.TrimPrefix(viewerURL, "https://browser.example/v/")
	token, _, _ := strings.Cut(withoutBase, "/")
	if token == "" {
		t.Fatalf("expected viewer token in %q", viewerURL)
	}
	return token
}

func createTestSession(t *testing.T, handler *controller.SessionController) string {
	t.Helper()

	body := []byte(`{
		"s3ProfilePath":"s3://bucket/browser-sessions/opaque/profile.tgz"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.CreateSession(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("create expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	data := decodeData(t, rr)
	return data["runtimeSessionId"].(string)
}

func decodeData(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("missing data: %+v", body)
	}
	return data
}
