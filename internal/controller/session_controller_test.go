package controller_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"browserd/internal/browser"
	"browserd/internal/config"
	"browserd/internal/controller"
	"browserd/internal/profile"
	"browserd/internal/router"
	"browserd/internal/session"
)

type fakeBrowserRuntime struct {
	prepareErr    error
	prepareCalls  []string
	closeCalls    []string
	navigateOut   browser.NavigateOutput
	navigateErr   error
	snapshotOut   browser.SnapshotOutput
	snapshotErr   error
	actOut        browser.ActOutput
	actErr        error
	screenshotOut browser.ScreenshotOutput
	screenshotErr error
}

func (f *fakeBrowserRuntime) PrepareSession(runtimeSessionID string) error {
	f.prepareCalls = append(f.prepareCalls, runtimeSessionID)
	return f.prepareErr
}

func (f *fakeBrowserRuntime) Close(runtimeSessionID string) error {
	f.closeCalls = append(f.closeCalls, runtimeSessionID)
	return nil
}

func (f *fakeBrowserRuntime) Navigate(_ string, _ browser.NavigateInput) (browser.NavigateOutput, error) {
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
