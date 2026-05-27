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
