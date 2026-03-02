package controller_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"browserd/internal/config"
	"browserd/internal/router"
)

func TestCreateSession_ReturnsCdpWsUrlAndLeaseEcho(t *testing.T) {
	h := router.New(config.Config{Port: 7011, CDPBaseURL: "ws://browserd:9222/devtools/browser"})

	body := []byte(`{
		"s3ProfilePath":"s3://bucket/browser-sessions/t_1/c_1/bs_1/profile.tgz",
		"leaseId":"lease_1"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
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

func TestCommitSession_ValidatesIfMatchVersion(t *testing.T) {
	h := router.New(config.Config{Port: 7011, CDPBaseURL: "ws://browserd:9222/devtools/browser"})

	create := []byte(`{
		"s3ProfilePath":"s3://bucket/browser-sessions/t_1/c_1/bs_1/profile.tgz"
	}`)
	creq := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(create))
	creq.Header.Set("Content-Type", "application/json")
	crr := httptest.NewRecorder()
	h.ServeHTTP(crr, creq)
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
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestCommitSession_Returns409OnVersionConflict(t *testing.T) {
	h := router.New(config.Config{Port: 7011, CDPBaseURL: "ws://browserd:9222/devtools/browser"})

	create := []byte(`{
		"s3ProfilePath":"s3://bucket/browser-sessions/t_1/c_1/bs_1/profile.tgz"
	}`)
	creq := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(create))
	creq.Header.Set("Content-Type", "application/json")
	crr := httptest.NewRecorder()
	h.ServeHTTP(crr, creq)
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
	h.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("first commit expected 200, got %d body=%s", rr1.Code, rr1.Body.String())
	}

	// stale ifMatchVersion should conflict
	commit2 := []byte(`{"ifMatchVersion":"new"}`)
	req2 := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+rid+"/commit", bytes.NewReader(commit2))
	req2.Header.Set("Content-Type", "application/json")
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rr2.Code, rr2.Body.String())
	}
}
