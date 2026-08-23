package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

type syncEnvelope[T any] struct {
	Data  *T            `json:"data"`
	Error *syncAPIError `json:"error"`
}

type syncAPIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type syncSession struct {
	RuntimeSessionID string `json:"runtimeSessionId"`
	ResolvedVersion  string `json:"resolvedVersion"`
}

type syncEvalResult struct {
	Result map[string]any `json:"result"`
	URL    string         `json:"url"`
	Title  string         `json:"title"`
}

type syncCommitResult struct {
	NewVersion string `json:"newVersion"`
}

func TestProfileCommitSyncSafety(t *testing.T) {
	baseURL := strings.TrimRight(os.Getenv("BROWSERD_SYNC_TEST_BASE_URL"), "/")
	workdir := strings.TrimSpace(os.Getenv("BROWSERD_SYNC_TEST_WORKDIR"))
	if baseURL == "" || workdir == "" {
		t.Skip("set BROWSERD_SYNC_TEST_BASE_URL and BROWSERD_SYNC_TEST_WORKDIR to run profile commit sync safety test")
	}
	pageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>sync safety</title></head><body>sync safety</body></html>`))
	}))
	defer pageServer.Close()
	rounds := 8
	if raw := strings.TrimSpace(os.Getenv("BROWSERD_SYNC_TEST_ROUNDS")); raw != "" {
		if _, err := fmt.Sscanf(raw, "%d", &rounds); err != nil || rounds <= 0 {
			t.Fatalf("invalid BROWSERD_SYNC_TEST_ROUNDS=%q", raw)
		}
	}

	directPass := 0
	for i := 0; i < rounds; i++ {
		ok := runProfileSyncRound(t, baseURL, workdir, pageServer.URL, "direct-commit", i)
		if ok {
			directPass++
		}
	}

	cdpPass := 0
	for i := 0; i < rounds; i++ {
		ok := runProfileSyncRound(t, baseURL, workdir, pageServer.URL, "cdp-close", i)
		if ok {
			cdpPass++
		}
	}

	t.Logf("profile commit restore result: direct-commit=%d/%d cdp-close=%d/%d", directPass, rounds, cdpPass, rounds)
	if cdpPass != rounds {
		t.Fatalf("CDP Browser.close profile checkpoint is not reliable: %d/%d rounds restored persistent cookie and localStorage", cdpPass, rounds)
	}
	if directPass != rounds {
		t.Fatalf("browserd commit endpoint is not reliable: %d/%d rounds restored persistent cookie and localStorage", directPass, rounds)
	}
}

func runProfileSyncRound(t *testing.T, baseURL string, workdir string, pageURL string, mode string, round int) bool {
	t.Helper()
	profilePath := fmt.Sprintf("/browser-sessions/sync-test/%s/round-%02d/profile.tgz", mode, round)
	session := syncCreateSession(t, baseURL, profilePath, "")
	runtimeID := session.RuntimeSessionID
	defer syncDeleteSession(baseURL, runtimeID)

	syncPost[syncEvalResult](t, baseURL, "/v1/sessions/"+runtimeID+"/navigate", map[string]any{
		"url":       pageURL,
		"waitUntil": "load",
		"timeoutMs": 15000,
	})

	key := fmt.Sprintf("sync_%s_%02d", strings.ReplaceAll(mode, "-", "_"), round)
	value := fmt.Sprintf("value_%d", time.Now().UnixNano())
	setScript := fmt.Sprintf(`return (() => {
  document.cookie = %q + "=" + %q + "; expires=Tue, 19 Jan 2038 03:14:07 GMT; path=/; SameSite=Lax";
  localStorage.setItem(%q, %q);
  return {cookie: document.cookie, localStorage: localStorage.getItem(%q)};
})()`, key, value, key, value, key)
	before := syncEvaluate(t, baseURL, runtimeID, setScript)
	if !strings.Contains(stringValue(before["cookie"]), key+"="+value) || stringValue(before["localStorage"]) != value {
		t.Fatalf("%s round %d did not set initial browser state: %#v", mode, round, before)
	}

	if mode == "cdp-close" {
		wsURL := syncDevToolsWSURL(t, workdir, runtimeID)
		syncCloseBrowserWithCDP(t, wsURL)
		syncWaitDevToolsDown(t, wsURL, 10*time.Second)
	}

	commit := syncPost[syncCommitResult](t, baseURL, "/v1/sessions/"+runtimeID+"/commit", map[string]any{
		"ifMatchVersion": firstNonEmpty(session.ResolvedVersion, "new"),
	})
	if strings.TrimSpace(commit.NewVersion) == "" {
		t.Fatalf("%s round %d commit returned empty version", mode, round)
	}

	restored := syncCreateSession(t, baseURL, profilePath, commit.NewVersion)
	defer syncDeleteSession(baseURL, restored.RuntimeSessionID)
	syncPost[syncEvalResult](t, baseURL, "/v1/sessions/"+restored.RuntimeSessionID+"/navigate", map[string]any{
		"url":       pageURL,
		"waitUntil": "load",
		"timeoutMs": 15000,
	})
	got := syncEvaluate(t, baseURL, restored.RuntimeSessionID, fmt.Sprintf(`return (() => ({cookie: document.cookie, localStorage: localStorage.getItem(%q)}))()`, key))
	cookieOK := strings.Contains(stringValue(got["cookie"]), key+"="+value)
	localOK := stringValue(got["localStorage"]) == value
	t.Logf("%s round %d restored cookie=%t localStorage=%t", mode, round, cookieOK, localOK)
	return cookieOK && localOK
}

func syncCreateSession(t *testing.T, baseURL string, profilePath string, expectedVersion string) syncSession {
	t.Helper()
	body := map[string]any{
		"profilePath":     profilePath,
		"expectedVersion": expectedVersion,
		"ttlSec":          300,
		"fingerprint": map[string]any{
			"seed":                "sync-safe-test",
			"locale":              "zh-CN",
			"languages":           []string{"zh-CN", "zh"},
			"acceptLanguage":      "zh-CN,zh;q=0.9",
			"timezone":            "Asia/Shanghai",
			"platform":            "Win32",
			"os":                  "Windows",
			"userAgent":           "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
			"viewportWidth":       1366,
			"viewportHeight":      768,
			"screenWidth":         1366,
			"screenHeight":        768,
			"deviceScaleFactor":   1,
			"hardwareConcurrency": 8,
			"deviceMemory":        8,
			"webglVendor":         "Google Inc.",
			"webglRenderer":       "ANGLE (Intel, Intel(R) UHD Graphics Direct3D11 vs_5_0 ps_5_0, D3D11)",
		},
	}
	return syncPost[syncSession](t, baseURL, "/v1/sessions", body)
}

func syncEvaluate(t *testing.T, baseURL string, runtimeID string, script string) map[string]any {
	t.Helper()
	out := syncPost[syncEvalResult](t, baseURL, "/v1/sessions/"+runtimeID+"/evaluate", map[string]any{
		"script":    script,
		"timeoutMs": 15000,
	})
	return out.Result
}

func syncPost[T any](t *testing.T, baseURL string, path string, body any) T {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.Post(baseURL+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("POST %s failed: %v", path, err)
	}
	defer func() { _ = res.Body.Close() }()
	payload, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	var envelope syncEnvelope[T]
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("POST %s invalid json: %s", path, payload)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 || envelope.Error != nil {
		t.Fatalf("POST %s status=%d error=%#v body=%s", path, res.StatusCode, envelope.Error, payload)
	}
	if envelope.Data == nil {
		t.Fatalf("POST %s missing data: %s", path, payload)
	}
	return *envelope.Data
}

func syncDeleteSession(baseURL string, runtimeID string) {
	if strings.TrimSpace(runtimeID) == "" {
		return
	}
	req, err := http.NewRequest(http.MethodDelete, baseURL+"/v1/sessions/"+runtimeID, nil)
	if err != nil {
		return
	}
	res, err := http.DefaultClient.Do(req)
	if err == nil && res != nil {
		_ = res.Body.Close()
	}
}

func syncDevToolsWSURL(t *testing.T, workdir string, runtimeID string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(workdir, "sessions", runtimeID, "profile", "DevToolsActivePort"))
	if err != nil {
		t.Fatalf("read DevToolsActivePort: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 2 {
		t.Fatalf("invalid DevToolsActivePort: %q", string(raw))
	}
	return "ws://127.0.0.1:" + strings.TrimSpace(lines[0]) + strings.TrimSpace(lines[1])
}

func syncCloseBrowserWithCDP(t *testing.T, wsURL string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, _, err := ws.Dial(ctx, wsURL)
	if err != nil {
		t.Fatalf("dial cdp websocket: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if err := wsutil.WriteClientText(conn, []byte(`{"id":1,"method":"Browser.close"}`)); err != nil {
		t.Fatalf("send Browser.close: %v", err)
	}
}

func syncWaitDevToolsDown(t *testing.T, wsURL string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		conn, _, _, err := ws.Dial(ctx, wsURL)
		cancel()
		if err != nil {
			return
		}
		_ = conn.Close()
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("devtools websocket still accepts connections after Browser.close")
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
