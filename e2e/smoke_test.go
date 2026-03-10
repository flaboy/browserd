package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
)

type envelope struct {
	Data  map[string]any `json:"data"`
	Error map[string]any `json:"error"`
}

func mustDoJSON(t *testing.T, method, url string, body any) (int, envelope) {
	t.Helper()
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		payload = b
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var env envelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp.StatusCode, env
}

func TestBrowserdMinIOSmoke(t *testing.T) {
	base := strings.TrimRight(os.Getenv("BROWSERD_BASE_URL"), "/")
	if base == "" {
		t.Skip("BROWSERD_BASE_URL not set")
	}

	profilePath := "s3://private/browser-sessions/team_e2e/case_e2e/bs_e2e/profile.tgz"
	createURL := base + "/v1/sessions"

	status, createEnv := mustDoJSON(t, http.MethodPost, createURL, map[string]any{
		"s3ProfilePath": profilePath,
	})
	if status != http.StatusOK {
		t.Fatalf("create status=%d err=%v", status, createEnv.Error)
	}
	if createEnv.Data["resolvedVersion"] != "new" {
		t.Fatalf("expected resolvedVersion=new, got %v", createEnv.Data["resolvedVersion"])
	}
	runtimeSessionID := fmt.Sprint(createEnv.Data["runtimeSessionId"])
	status, navEnv := mustDoJSON(t, http.MethodPost, base+"/v1/sessions/"+runtimeSessionID+"/navigate", map[string]any{
		"url":       "https://example.com/",
		"waitUntil": "load",
		"timeoutMs": 30000,
	})
	if status != http.StatusOK {
		t.Fatalf("navigate after create status=%d err=%v", status, navEnv.Error)
	}

	commitURL := base + "/v1/sessions/" + runtimeSessionID + "/commit"
	status, commitEnv := mustDoJSON(t, http.MethodPost, commitURL, map[string]any{
		"ifMatchVersion": "new",
	})
	if status != http.StatusOK {
		t.Fatalf("first commit status=%d err=%v", status, commitEnv.Error)
	}
	newVersion := fmt.Sprint(commitEnv.Data["newVersion"])
	if newVersion == "" || newVersion == "<nil>" {
		t.Fatalf("empty newVersion")
	}

	status, create2Env := mustDoJSON(t, http.MethodPost, createURL, map[string]any{
		"s3ProfilePath":   profilePath,
		"expectedVersion": newVersion,
	})
	if status != http.StatusOK {
		t.Fatalf("create2 status=%d err=%v", status, create2Env.Error)
	}
	runtimeSessionID2 := fmt.Sprint(create2Env.Data["runtimeSessionId"])
	status, navEnv = mustDoJSON(t, http.MethodPost, base+"/v1/sessions/"+runtimeSessionID2+"/navigate", map[string]any{
		"url":       "https://example.com/",
		"waitUntil": "load",
		"timeoutMs": 30000,
	})
	if status != http.StatusOK {
		t.Fatalf("navigate after create2 status=%d err=%v", status, navEnv.Error)
	}

	status, conflictEnv := mustDoJSON(t, http.MethodPost, base+"/v1/sessions/"+runtimeSessionID2+"/commit", map[string]any{
		"ifMatchVersion": "new",
	})
	if status != http.StatusConflict {
		t.Fatalf("expected 409 conflict, got status=%d err=%v", status, conflictEnv.Error)
	}
}
