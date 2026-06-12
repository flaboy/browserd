package config

import (
	"testing"
	"time"
)

func TestLoadReadsLiveViewSettings(t *testing.T) {
	t.Setenv("BROWSERD_LIVE_BASE_URL", "https://cluster-browser.example/")
	t.Setenv("BROWSERD_LIVE_TOKEN_TTL", "15m")
	t.Setenv("BROWSERD_NOVNC_BASE_PATH", "/viewer")

	cfg := Load()

	if cfg.LiveBaseURL != "https://cluster-browser.example" {
		t.Fatalf("unexpected live base url: %q", cfg.LiveBaseURL)
	}
	if cfg.LiveTokenTTL != 15*time.Minute {
		t.Fatalf("unexpected ttl: %s", cfg.LiveTokenTTL)
	}
	if cfg.NoVNCBasePath != "/viewer" {
		t.Fatalf("unexpected base path: %q", cfg.NoVNCBasePath)
	}
}

func TestLoadDefaultsLiveViewSettings(t *testing.T) {
	cfg := Load()

	if cfg.LiveTokenTTL != 15*time.Minute {
		t.Fatalf("unexpected default ttl: %s", cfg.LiveTokenTTL)
	}
	if cfg.NoVNCBasePath != "/v" {
		t.Fatalf("unexpected default base path: %q", cfg.NoVNCBasePath)
	}
}

func TestLoad_CloudflareWorkerProxyHopConfig(t *testing.T) {
	t.Setenv("BROWSERD_PROXY_HOP", "cloudflare-worker")
	t.Setenv("BROWSERD_PROXY_WORKER_URL", "wss://proxy-hop.example.workers.dev/tunnel")
	t.Setenv("BROWSERD_PROXY_WORKER_TOKEN", "worker-token")

	cfg := Load()

	if cfg.ProxyHop != "cloudflare-worker" {
		t.Fatalf("unexpected proxy hop: %q", cfg.ProxyHop)
	}
	if cfg.ProxyWorkerURL != "wss://proxy-hop.example.workers.dev/tunnel" {
		t.Fatalf("unexpected worker url: %q", cfg.ProxyWorkerURL)
	}
	if cfg.ProxyWorkerToken != "worker-token" {
		t.Fatalf("unexpected worker token")
	}
}

func TestLoad_DefaultProxyHopDisabled(t *testing.T) {
	cfg := Load()

	if cfg.ProxyHop != "" || cfg.ProxyWorkerURL != "" || cfg.ProxyWorkerToken != "" {
		t.Fatalf("expected proxy hop disabled by default, got %+v", cfg)
	}
}
