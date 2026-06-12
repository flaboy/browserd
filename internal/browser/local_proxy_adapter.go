package browser

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type localProxyAdapterOptions struct {
	RuntimeSessionID string
	UpstreamProxy    ProxyConfig
	WorkerURL        string
	WorkerToken      string
	TunnelOpener     proxyHopTunnelOpener
}

type localProxyAdapter struct {
	opts   localProxyAdapterOptions
	ln     net.Listener
	server *http.Server
}

func newLocalProxyAdapter(opts localProxyAdapterOptions) *localProxyAdapter {
	return &localProxyAdapter{opts: opts}
}

func (a *localProxyAdapter) Start(_ context.Context) error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	a.ln = ln
	a.server = &http.Server{Handler: http.HandlerFunc(a.handleHTTP)}
	go func() {
		_ = a.server.Serve(ln)
	}()
	return nil
}

func (a *localProxyAdapter) ChromeProxyServer() string {
	if a == nil || a.ln == nil {
		return ""
	}
	return "http://" + a.ln.Addr().String()
}

func (a *localProxyAdapter) Close() error {
	if a == nil || a.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return a.server.Shutdown(ctx)
}

func (a *localProxyAdapter) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		a.handleConnect(w, r)
		return
	}
	a.handleForwardHTTP(w, r)
}

func (a *localProxyAdapter) handleConnect(w http.ResponseWriter, r *http.Request) {
	tunnel, err := a.openTunnel(r.Context(), r.Host)
	if err != nil {
		http.Error(w, "proxy hop connect failed", http.StatusBadGateway)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		_ = tunnel.Close()
		http.Error(w, "hijacking unsupported", http.StatusInternalServerError)
		return
	}
	clientConn, buf, err := hijacker.Hijack()
	if err != nil {
		_ = tunnel.Close()
		return
	}
	if _, err := buf.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		_ = clientConn.Close()
		_ = tunnel.Close()
		return
	}
	if err := buf.Flush(); err != nil {
		_ = clientConn.Close()
		_ = tunnel.Close()
		return
	}
	go pipeBoth(clientConn, tunnel)
}

func (a *localProxyAdapter) handleForwardHTTP(w http.ResponseWriter, r *http.Request) {
	target, err := targetHostPort(r)
	if err != nil {
		http.Error(w, "invalid proxy request target", http.StatusBadRequest)
		return
	}
	tunnel, err := a.openTunnel(r.Context(), target)
	if err != nil {
		http.Error(w, "proxy hop connect failed", http.StatusBadGateway)
		return
	}
	defer tunnel.Close()

	outReq := new(http.Request)
	*outReq = *r
	outReq.RequestURI = ""
	outReq.URL = cloneURL(r.URL)
	outReq.URL.Scheme = ""
	outReq.URL.Host = ""
	outReq.Header = r.Header.Clone()
	outReq.Header.Del("Proxy-Authorization")
	outReq.Header.Del("Proxy-Connection")
	outReq.Header.Del("Connection")
	outReq.Close = true
	if err := outReq.Write(tunnel); err != nil {
		http.Error(w, "proxy hop write failed", http.StatusBadGateway)
		return
	}
	if _, err := io.Copy(w, tunnel); err != nil {
		return
	}
}

func (a *localProxyAdapter) openTunnel(ctx context.Context, target string) (io.ReadWriteCloser, error) {
	opener := a.opts.TunnelOpener
	if opener == nil {
		opener = newProxyHopClient(proxyHopClientOptions{
			WorkerURL:   a.opts.WorkerURL,
			WorkerToken: a.opts.WorkerToken,
		})
	}
	id := fmt.Sprintf("%s-%d", a.opts.RuntimeSessionID, time.Now().UnixNano())
	return opener.Open(ctx, id, target, a.opts.UpstreamProxy)
}

func targetHostPort(r *http.Request) (string, error) {
	host := r.URL.Host
	if host == "" {
		host = r.Host
	}
	if host == "" {
		return "", ErrInvalidRequest
	}
	if strings.Contains(host, ":") {
		return host, nil
	}
	port := "80"
	if strings.EqualFold(r.URL.Scheme, "https") {
		port = "443"
	}
	return net.JoinHostPort(host, port), nil
}

func cloneURL(in interface{ String() string }) *url.URL {
	u, _ := url.Parse(in.String())
	return u
}

func pipeBoth(a io.ReadWriteCloser, b io.ReadWriteCloser) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(a, b)
		_ = a.Close()
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(b, a)
		_ = b.Close()
	}()
	wg.Wait()
}
