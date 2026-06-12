package browser

import (
	"context"
	"errors"
	"io"
)

var (
	ErrProxyHopConfigMissing    = errors.New("proxy hop config missing")
	ErrProxyHopConnectFailed    = errors.New("proxy hop connect failed")
	ErrUpstreamProxyAuthFailed  = errors.New("upstream proxy auth failed")
	ErrUpstreamProxyConnectFail = errors.New("upstream proxy connect failed")
)

type proxyHopAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type proxyHopOpenRequest struct {
	Type              string        `json:"type"`
	ID                string        `json:"id"`
	Target            string        `json:"target"`
	UpstreamProxy     string        `json:"upstreamProxy"`
	UpstreamProxyAuth *proxyHopAuth `json:"upstreamProxyAuth,omitempty"`
}

type proxyHopOpenResponse struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func newProxyHopOpenRequest(id string, target string, proxy ProxyConfig) proxyHopOpenRequest {
	req := proxyHopOpenRequest{
		Type:          "open",
		ID:            id,
		Target:        target,
		UpstreamProxy: proxy.ChromeServer,
	}
	if proxy.HasAuth() {
		req.UpstreamProxyAuth = &proxyHopAuth{
			Username: proxy.Username,
			Password: proxy.Password,
		}
	}
	return req
}

type proxyHopTunnelOpener interface {
	Open(ctx context.Context, id string, target string, proxy ProxyConfig) (io.ReadWriteCloser, error)
}
