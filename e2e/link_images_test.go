package e2e

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLinkImagesE2E(t *testing.T) {
	base := strings.TrimRight(os.Getenv("BROWSERD_BASE_URL"), "/")
	if base == "" {
		t.Skip("NOT EXECUTED: BROWSERD_BASE_URL is required")
	}
	site := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/image.svg" {
			w.Header().Set("Content-Type", "image/svg+xml")
			fmt.Fprint(w, `<svg xmlns="http://www.w3.org/2000/svg" width="80" height="80"><rect width="80" height="80" fill="red"/></svg>`)
			return
		}
		fmt.Fprint(w, `<title>Image contract</title><a href="/item?variant=1"><img style="opacity:0" width="80" height="80" src="/image.svg?version=1"></a><a href="/item?variant=1">Item $25</a><a href="/item?variant=2">Other $30</a>`)
	}))
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = site.Listener.Close()
	site.Listener = listener
	site.Start()
	defer site.Close()
	u, _ := url.Parse(site.URL)
	host := os.Getenv("BROWSERD_TEST_SITE_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	u.Host = net.JoinHostPort(host, u.Port())
	siteURL := u.String()
	status, created := mustDoJSON(t, "POST", base+"/v1/sessions", map[string]any{"profilePath": fmt.Sprintf("/accounts/image-e2e/%d/profile.tgz", time.Now().UnixNano()), "fingerprint": smokeFingerprint()})
	if status != 200 {
		t.Fatalf("create: %d %+v", status, created)
	}
	session := base + "/v1/sessions/" + fmt.Sprint(created.Data["runtimeSessionId"])
	defer mustDoJSON(t, "DELETE", session, nil)
	status, nav := mustDoJSON(t, "POST", session+"/navigate", map[string]any{"url": siteURL, "includeSnapshot": true, "waitUntil": "load", "timeoutMs": 10000})
	if status != 200 {
		t.Fatalf("navigate: %d %+v", status, nav)
	}
	assertImages := func(snapshot map[string]any) {
		t.Helper()
		links := snapshot["page"].(map[string]any)["groups"].(map[string]any)["links"].(map[string]any)
		columns := links["columns"].([]any)
		if len(columns) != 5 || columns[4] != "image_url" {
			t.Fatalf("missing image column: %v", columns)
		}
		rows := links["rows"].([]any)
		if len(rows) != 3 || rows[0].([]any)[4] != siteURL+"/image.svg?version=1" || rows[1].([]any)[4] != "" || rows[2].([]any)[4] != "" {
			t.Fatalf("image-only link lost or assigned to another link: %+v", rows)
		}
	}
	assertImages(nav.Data["snapshot"].(map[string]any))
	status, snapshot := mustDoJSON(t, "GET", session+"/snapshot", nil)
	if status != 200 {
		t.Fatalf("snapshot: %d %+v", status, snapshot)
	}
	assertImages(snapshot.Data)
}
