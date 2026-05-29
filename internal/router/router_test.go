package router

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"browserd/internal/config"
	"browserd/internal/liveviewer"
)

func TestExtractLiveViewToken_UsesFirstPathSegment(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "root", path: "/v/tok_1/", want: "tok_1"},
		{name: "asset path", path: "/v/tok_1/vnc.html", want: "tok_1"},
		{name: "websocket path", path: "/v/tok_1/websockify", want: "tok_1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractLiveViewToken(tt.path, "/v"); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestNew_ServesBrowserLiveAssetsOutsideTokenPath(t *testing.T) {
	indexHTML, err := liveviewer.IndexHTML()
	if err != nil {
		t.Fatal(err)
	}
	matches := regexp.MustCompile(`/browser-live/(assets/[^"]+\.js)`).FindSubmatch(indexHTML)
	if len(matches) != 2 {
		t.Fatalf("expected browser-live hashed JS asset in index html: %s", string(indexHTML))
	}
	handler := New(config.Config{})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/browser-live/"+string(matches[1]), nil)

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "websockify") {
		t.Fatalf("expected bundled live viewer asset, got %s", rr.Body.String())
	}
	if cache := rr.Header().Get("Cache-Control"); !strings.Contains(cache, "immutable") {
		t.Fatalf("expected immutable cache header, got %q", cache)
	}
}
