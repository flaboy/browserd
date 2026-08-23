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

func smokeFingerprint() map[string]any {
	return map[string]any{
		"seed":                "fp_smoke_1",
		"userAgent":           "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36",
		"acceptLanguage":      "en-US,en;q=0.9",
		"locale":              "en-US",
		"languages":           []string{"en-US", "en"},
		"timezone":            "America/New_York",
		"platform":            "Win32",
		"os":                  "Windows",
		"viewportWidth":       1366,
		"viewportHeight":      768,
		"screenWidth":         1366,
		"screenHeight":        768,
		"deviceScaleFactor":   1,
		"hardwareConcurrency": 8,
		"deviceMemory":        8,
		"webglVendor":         "Google Inc. (Intel)",
		"webglRenderer":       "ANGLE (Intel)",
	}
}

func resultString(env envelope, key string) string {
	result, _ := env.Data["result"].(map[string]any)
	return fmt.Sprint(result[key])
}

func TestBrowserdMinIOSmoke(t *testing.T) {
	base := strings.TrimRight(os.Getenv("BROWSERD_BASE_URL"), "/")
	if base == "" {
		t.Skip("BROWSERD_BASE_URL not set")
	}

	profilePath := "/browser-sessions/team_e2e/case_e2e/bs_e2e/profile.tgz"
	createURL := base + "/v1/sessions"

	status, createEnv := mustDoJSON(t, http.MethodPost, createURL, map[string]any{
		"profilePath": profilePath,
		"fingerprint": smokeFingerprint(),
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
	status, evalEnv := mustDoJSON(t, http.MethodPost, base+"/v1/sessions/"+runtimeSessionID+"/evaluate", map[string]any{
		"script": `return {
			userAgent: navigator.userAgent,
			language: navigator.language,
			languages: navigator.languages,
			timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
			platform: navigator.platform,
			width: screen.width,
			height: screen.height,
			hardwareConcurrency: navigator.hardwareConcurrency,
			deviceMemory: navigator.deviceMemory
		}`,
	})
	if status != http.StatusOK {
		t.Fatalf("evaluate fingerprint status=%d err=%v", status, evalEnv.Error)
	}
	if resultString(evalEnv, "timezone") != "America/New_York" {
		t.Fatalf("timezone fingerprint mismatch: %+v", evalEnv.Data["result"])
	}
	if resultString(evalEnv, "platform") != "Win32" {
		t.Fatalf("platform fingerprint mismatch: %+v", evalEnv.Data["result"])
	}
	if resultString(evalEnv, "language") != "en-US" {
		t.Fatalf("language fingerprint mismatch: %+v", evalEnv.Data["result"])
	}
	if resultString(evalEnv, "width") != "1366" || resultString(evalEnv, "height") != "768" {
		t.Fatalf("screen fingerprint mismatch: %+v", evalEnv.Data["result"])
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
		"profilePath":     profilePath,
		"fingerprint":     smokeFingerprint(),
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
