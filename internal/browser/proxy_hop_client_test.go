package browser

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

func TestProxyHopClient_OpenSendsAuthAndOpenRequest(t *testing.T) {
	server := newTestWorkerTunnelServer(t)
	defer server.Close()

	proxy, err := ParseProxyServer("http://user:pass@proxy.example.com:8080")
	if err != nil {
		t.Fatal(err)
	}
	client := newProxyHopClient(proxyHopClientOptions{
		WorkerURL:   wsURL(server.URL),
		WorkerToken: "worker-token",
	})
	conn, err := client.Open(context.Background(), "connect-1", "proxy-auth-check.local:80", proxy)
	if err != nil {
		t.Fatalf("open tunnel: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("unexpected echo: %q", string(buf))
	}
}

func newTestWorkerTunnelServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer worker-token" {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		conn, _, _, err := ws.UpgradeHTTP(r, w)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		raw, err := wsutil.ReadClientText(conn)
		if err != nil {
			t.Errorf("read open: %v", err)
			return
		}
		var req proxyHopOpenRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Errorf("decode open: %v", err)
			return
		}
		if req.Type != "open" || req.ID != "connect-1" || req.Target != "proxy-auth-check.local:80" {
			t.Errorf("unexpected open request: %+v", req)
			return
		}
		if req.UpstreamProxy != "http://proxy.example.com:8080" {
			t.Errorf("unexpected upstream proxy: %q", req.UpstreamProxy)
			return
		}
		if req.UpstreamProxyAuth == nil || req.UpstreamProxyAuth.Username != "user" || req.UpstreamProxyAuth.Password != "pass" {
			t.Errorf("unexpected upstream auth: %+v", req.UpstreamProxyAuth)
			return
		}
		resp, _ := json.Marshal(proxyHopOpenResponse{Type: "open_result", ID: req.ID, OK: true})
		if err := wsutil.WriteServerText(conn, resp); err != nil {
			t.Errorf("write open result: %v", err)
			return
		}
		for {
			data, err := wsutil.ReadClientBinary(conn)
			if err != nil {
				return
			}
			if err := wsutil.WriteServerBinary(conn, data); err != nil {
				return
			}
		}
	}))
}

func wsURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}
