package browser

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

var ErrInvalidProxyServer = errors.New("invalid proxy server")

type ProxyConfig struct {
	Raw          string
	ChromeServer string
	Masked       string
	Username     string
	Password     string
}

func ParseProxyServer(raw string) (ProxyConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ProxyConfig{}, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ProxyConfig{}, fmt.Errorf("%w: malformed proxy URL", ErrInvalidProxyServer)
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	if scheme != "http" && scheme != "socks5" {
		return ProxyConfig{}, fmt.Errorf("%w: unsupported proxy scheme", ErrInvalidProxyServer)
	}
	if strings.TrimSpace(u.Hostname()) == "" || strings.TrimSpace(u.Port()) == "" {
		return ProxyConfig{}, fmt.Errorf("%w: proxy host and port are required", ErrInvalidProxyServer)
	}
	hostport := net.JoinHostPort(u.Hostname(), u.Port())
	server := scheme + "://" + hostport
	masked := server
	username := ""
	password := ""
	if u.User != nil {
		username = u.User.Username()
		password, _ = u.User.Password()
		masked = scheme + "://***:***@" + hostport
	}
	return ProxyConfig{
		Raw:          raw,
		ChromeServer: server,
		Masked:       masked,
		Username:     username,
		Password:     password,
	}, nil
}

func (p ProxyConfig) HasAuth() bool {
	return p.Username != "" || p.Password != ""
}
