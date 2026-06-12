package browser

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProxyHopOpenRequestMasksUpstreamProxy(t *testing.T) {
	proxy, err := ParseProxyServer("http://user:pass@proxy.example.com:8080")
	if err != nil {
		t.Fatalf("parse proxy: %v", err)
	}

	req := newProxyHopOpenRequest("connect-1", "news.163.com:443", proxy)

	if req.Type != "open" || req.ID != "connect-1" || req.Target != "news.163.com:443" {
		t.Fatalf("unexpected open request: %+v", req)
	}
	if req.UpstreamProxy != "http://proxy.example.com:8080" {
		t.Fatalf("unexpected upstream proxy: %q", req.UpstreamProxy)
	}
	if req.UpstreamProxyAuth == nil || req.UpstreamProxyAuth.Username != "user" || req.UpstreamProxyAuth.Password != "pass" {
		t.Fatalf("missing auth: %+v", req.UpstreamProxyAuth)
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "user:pass@") {
		t.Fatalf("serialized request must not contain credential URL: %s", string(b))
	}
}
