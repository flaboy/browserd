package e2e

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestReadPageSmoke_BaiduSnapshotPage(t *testing.T) {
	base := strings.TrimRight(os.Getenv("BROWSERD_BASE_URL"), "/")
	if base == "" {
		t.Skip("BROWSERD_BASE_URL not set")
	}

	status, createEnv := mustDoJSON(t, http.MethodPost, base+"/v1/sessions", map[string]any{
		"s3ProfilePath": "s3://private/browser-sessions/team_e2e/case_e2e/read_page/profile.tgz",
		"leaseId":       "lease_read_page_smoke",
	})
	if status != http.StatusOK {
		t.Fatalf("create status=%d err=%v", status, createEnv.Error)
	}
	runtimeSessionID := fmt.Sprint(createEnv.Data["runtimeSessionId"])
	if runtimeSessionID == "" || runtimeSessionID == "<nil>" {
		t.Fatalf("expected runtimeSessionId, got %+v", createEnv.Data)
	}
	if cdpWSURL := fmt.Sprint(createEnv.Data["cdpWsUrl"]); cdpWSURL == "" || cdpWSURL == "<nil>" {
		t.Fatalf("expected cdpWsUrl, got %+v", createEnv.Data)
	}
	t.Cleanup(func() {
		_, _ = mustDoJSON(t, http.MethodDelete, base+"/v1/sessions/"+runtimeSessionID, nil)
	})

	status, navEnv := mustDoJSON(t, http.MethodPost, base+"/v1/sessions/"+runtimeSessionID+"/navigate", map[string]any{
		"url":       "https://www.baidu.com/",
		"waitUntil": "load",
		"timeoutMs": 30000,
	})
	if status != http.StatusOK {
		t.Fatalf("navigate status=%d err=%v", status, navEnv.Error)
	}

	status, snapshotEnv := mustDoJSON(t, http.MethodGet, base+"/v1/sessions/"+runtimeSessionID+"/snapshot?mode=refs", nil)
	if status != http.StatusOK {
		t.Fatalf("snapshot status=%d err=%v", status, snapshotEnv.Error)
	}

	page, ok := snapshotEnv.Data["page"].(map[string]any)
	if !ok {
		t.Fatalf("expected page object, got %+v", snapshotEnv.Data)
	}
	if fmt.Sprint(page["title"]) != "百度一下，你就知道" {
		t.Fatalf("unexpected page title: %+v", page["title"])
	}

	groups, ok := page["groups"].(map[string]any)
	if !ok {
		t.Fatalf("expected groups object, got %+v", page["groups"])
	}
	buttons, ok := groups["buttons"].(map[string]any)
	if !ok {
		t.Fatalf("expected buttons group, got %+v", groups)
	}
	buttonRows, ok := buttons["rows"].([]any)
	if !ok || len(buttonRows) == 0 {
		t.Fatalf("expected button rows, got %+v", buttons)
	}
	foundSearchButton := false
	for _, row := range buttonRows {
		cells, ok := row.([]any)
		if !ok {
			continue
		}
		for _, cell := range cells {
			if fmt.Sprint(cell) == "百度一下" {
				foundSearchButton = true
				break
			}
		}
	}
	if !foundSearchButton {
		t.Fatalf("expected 百度一下 in buttons rows: %+v", buttonRows)
	}

	texts, ok := groups["texts"].(map[string]any)
	if !ok {
		t.Fatalf("expected texts group, got %+v", groups)
	}
	textRows, ok := texts["rows"].([]any)
	if !ok || len(textRows) == 0 {
		t.Fatalf("expected text rows, got %+v", texts)
	}

	firstTextRow, ok := textRows[0].([]any)
	if !ok || len(firstTextRow) == 0 {
		t.Fatalf("expected first text row, got %+v", textRows[0])
	}
	textRef := fmt.Sprint(firstTextRow[0])
	status, actEnv := mustDoJSON(t, http.MethodPost, base+"/v1/sessions/"+runtimeSessionID+"/act", map[string]any{
		"action": "click",
		"ref":    textRef,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 clicking text ref, got status=%d env=%+v", status, actEnv)
	}
	if code := fmt.Sprint(actEnv.Error["code"]); code != "INVALID_REF" {
		t.Fatalf("expected INVALID_REF, got %+v", actEnv.Error)
	}
}
