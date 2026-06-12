package browser

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestLocalProxyAdapter_StartReturnsLoopbackProxyServer(t *testing.T) {
	proxy, err := ParseProxyServer("http://user:pass@proxy.example.com:8080")
	if err != nil {
		t.Fatal(err)
	}
	adapter := newLocalProxyAdapter(localProxyAdapterOptions{
		RuntimeSessionID: "rt_1",
		UpstreamProxy:    proxy,
		WorkerURL:        "wss://worker.example/tunnel",
		WorkerToken:      "token",
	})
	if err := adapter.Start(context.Background()); err != nil {
		t.Fatalf("start adapter: %v", err)
	}
	defer adapter.Close()

	server := adapter.ChromeProxyServer()
	if server == "" {
		t.Fatalf("expected chrome proxy server")
	}
	if _, _, err := net.SplitHostPort(strings.TrimPrefix(server, "http://")); err != nil {
		t.Fatalf("expected host:port proxy server, got %q: %v", server, err)
	}
}

func TestLocalProxyAdapter_ConnectOpensWorkerTunnel(t *testing.T) {
	opener := newFakeTunnelOpener()
	defer opener.server.Close()
	proxy, err := ParseProxyServer("http://user:pass@proxy.example.com:8080")
	if err != nil {
		t.Fatal(err)
	}
	adapter := newLocalProxyAdapter(localProxyAdapterOptions{
		RuntimeSessionID: "rt_1",
		UpstreamProxy:    proxy,
		TunnelOpener:     opener,
	})
	if err := adapter.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()

	conn, err := net.Dial("tcp", strings.TrimPrefix(adapter.ChromeProxyServer(), "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if _, err := fmt.Fprintf(conn, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %s", resp.Status)
	}
	if !reflect.DeepEqual(opener.targets, []string{"example.com:443"}) {
		t.Fatalf("unexpected targets: %+v", opener.targets)
	}
}

func TestLocalProxyAdapter_HTTPAbsoluteFormUsesTargetHost(t *testing.T) {
	opener := newFakeTunnelOpener()
	defer opener.server.Close()
	proxy, err := ParseProxyServer("http://user:pass@proxy.example.com:8080")
	if err != nil {
		t.Fatal(err)
	}
	adapter := newLocalProxyAdapter(localProxyAdapterOptions{
		RuntimeSessionID: "rt_1",
		UpstreamProxy:    proxy,
		TunnelOpener:     opener,
	})
	if err := adapter.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()

	conn, err := net.Dial("tcp", strings.TrimPrefix(adapter.ChromeProxyServer(), "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if _, err := fmt.Fprintf(conn, "GET http://proxy-auth-check.local/path?q=1 HTTP/1.1\r\nHost: proxy-auth-check.local\r\nProxy-Connection: keep-alive\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	req, err := http.ReadRequest(bufio.NewReader(opener.server))
	if err != nil {
		t.Fatal(err)
	}
	if req.RequestURI != "/path?q=1" {
		t.Fatalf("expected origin-form request URI, got %q", req.RequestURI)
	}
	if !reflect.DeepEqual(opener.targets, []string{"proxy-auth-check.local:80"}) {
		t.Fatalf("unexpected targets: %+v", opener.targets)
	}
	if req.Header.Get("Proxy-Connection") != "" {
		t.Fatalf("expected proxy headers to be removed: %+v", req.Header)
	}
	if _, err := fmt.Fprintf(opener.server, "HTTP/1.1 204 No Content\r\nContent-Length: 0\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	_ = opener.server.Close()
}

type fakeTunnelOpener struct {
	targets []string
	client  net.Conn
	server  net.Conn
}

func newFakeTunnelOpener() *fakeTunnelOpener {
	client, server := net.Pipe()
	return &fakeTunnelOpener{client: client, server: server}
}

func (f *fakeTunnelOpener) Open(_ context.Context, _ string, target string, _ ProxyConfig) (io.ReadWriteCloser, error) {
	f.targets = append(f.targets, target)
	return f.client, nil
}
