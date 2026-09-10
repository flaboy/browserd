package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNavigateSnapshotE2E(t *testing.T) {
	base := strings.TrimRight(os.Getenv("BROWSERD_BASE_URL"), "/")
	if base == "" {
		t.Skip("NOT EXECUTED: BROWSERD_BASE_URL is required")
	}
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/network":
			fmt.Fprint(w, `<body>Live catalog $25<script>fetch('/stream')</script></body>`)
		case "/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: connected\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/broken":
			fmt.Fprint(w, `<body>Extraction failure<script>document.querySelectorAll = () => {throw new Error('forced extraction failure')}</script></body>`)
		case "/redirect":
			http.Redirect(w, r, "/catalog", http.StatusFound)
		case "/slow":
			select {
			case <-r.Context().Done():
			case <-time.After(time.Second * 2):
			}
			fmt.Fprint(w, "<body>slow</body>")
		case "/async":
			fmt.Fprint(w, `<body>Loading<script>setTimeout(()=>{document.body.innerHTML='<button id="buy">Buy delayed item $25</button>'},700)</script></body>`)
		default:
			fmt.Fprint(w, `<title>Catalog</title><body><a href="/item">Real item $25</a><button onclick="this.textContent='Purchased'">Buy item</button></body>`)
		}
	}))
	defer site.Close()
	status, created := mustDoJSON(t, "POST", base+"/v1/sessions", map[string]any{"profilePath": fmt.Sprintf("/accounts/nav-e2e/%d/profile.tgz", time.Now().UnixNano()), "fingerprint": smokeFingerprint()})
	if status != 200 {
		t.Fatalf("create: %d %+v", status, created)
	}
	id := fmt.Sprint(created.Data["runtimeSessionId"])
	session := base + "/v1/sessions/" + id
	defer mustDoJSON(t, "DELETE", session, nil)
	var snapshotID string
	for _, path := range []string{"/catalog", "/redirect"} {
		status, out := mustDoJSON(t, "POST", session+"/navigate", map[string]any{"url": site.URL + path, "includeSnapshot": true, "waitUntil": "load", "timeoutMs": 5000})
		if status != 200 {
			t.Fatalf("navigate: %d %+v", status, out)
		}
		snap, ok := out.Data["snapshot"].(map[string]any)
		if !ok {
			t.Fatalf("navigation did not return snapshot: %+v", out)
		}
		if snap["snapshotId"] == "" || snap["snapshotId"] == snapshotID {
			t.Fatal("snapshot ID not renewed")
		}
		snapshotID = fmt.Sprint(snap["snapshotId"])
		page := snap["page"].(map[string]any)
		if page["url"] != site.URL+"/catalog" || out.Data["url"] != page["url"] {
			t.Fatalf("redirect observation inconsistent: %+v", out)
		}
		raw, _ := json.Marshal(page)
		if !strings.Contains(string(raw), "Real item $25") {
			t.Fatal("missing real page facts")
		}
		buttons := page["groups"].(map[string]any)["buttons"].(map[string]any)
		ref := buttons["rows"].([]any)[0].([]any)[0]
		status, act := mustDoJSON(t, "POST", session+"/act", map[string]any{"action": "click", "ref": ref})
		if status != 200 {
			t.Fatalf("returned ref cannot act: %d %+v", status, act)
		}
	}
	status, out := mustDoJSON(t, "POST", session+"/navigate", map[string]any{"url": site.URL + "/network", "includeSnapshot": true, "timeoutMs": 3000})
	if status != 200 || out.Data["snapshot"] == nil {
		t.Fatalf("continuous background network blocked load observation: %d %+v", status, out)
	}
	status, out = mustDoJSON(t, "POST", session+"/navigate", map[string]any{"url": site.URL + "/broken", "includeSnapshot": true, "timeoutMs": 3000})
	if status != 502 || out.Data != nil || out.Error["code"] != "SNAPSHOT_FAILED" {
		t.Fatalf("extraction failure not distinguished: %d %+v", status, out)
	}
	status, out = mustDoJSON(t, "POST", session+"/act", map[string]any{"action": "click", "ref": "e1"})
	if status == 200 {
		t.Fatal("failed observation retained actionable stale refs")
	}
	status, out = mustDoJSON(t, "POST", session+"/navigate", map[string]any{"url": site.URL + "/async", "includeSnapshot": true})
	if status != 200 || out.Data["snapshot"] == nil {
		t.Fatalf("async navigation failed: %+v", out)
	}
	status, out = mustDoJSON(t, "POST", session+"/wait-for", map[string]any{"condition": map[string]any{"type": "element_visible", "selector": "#buy"}, "timeoutMs": 5000})
	if status != 200 {
		t.Fatalf("explicit wait failed: %+v", out)
	}
	status, out = mustDoJSON(t, "GET", session+"/snapshot", nil)
	raw, _ := json.Marshal(out.Data)
	if status != 200 || !strings.Contains(string(raw), "Buy delayed item $25") {
		t.Fatalf("fresh observation missing async facts: %s", raw)
	}
	// Evaluate -> native PageTool must not recursively acquire the request guard.
	status, out = mustDoJSON(t, "POST", session+"/evaluate", map[string]any{"script": "return await window.__browserdPageToolCall({method:'page.title',payload:{}});", "pageToolBridge": map[string]any{"name": "__browserdPageToolCall", "enabled": true}, "timeoutMs": 3000})
	if status != 200 {
		t.Fatalf("evaluate bridge: %d %+v", status, out)
	}
	status, out = mustDoJSON(t, "POST", session+"/navigate", map[string]any{"url": site.URL + "/slow", "includeSnapshot": true, "timeoutMs": 100})
	if status == 200 || out.Data != nil {
		t.Fatalf("timeout returned success: %d %+v", status, out)
	}
}
