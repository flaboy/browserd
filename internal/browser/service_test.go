package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	browserrt "browserd/internal/runtime"
)

type fakeAssetStore struct {
	puts []fakeAssetPut
	err  error
}

type fakeAssetPut struct {
	URI         string
	Body        []byte
	ContentType string
}

func (f *fakeAssetStore) Put(_ context.Context, uri string, body []byte, contentType string) error {
	f.puts = append(f.puts, fakeAssetPut{URI: uri, Body: append([]byte(nil), body...), ContentType: contentType})
	return f.err
}

func TestBuildChromeArgs_IncludesNoSandboxAndProfileDir(t *testing.T) {
	args := buildChromeArgs(BrowserOptions{UserDataDir: "/tmp/profile", Headless: true})

	hasNoSandbox := false
	hasUserDataDir := false
	for _, arg := range args {
		if arg == "--no-sandbox" {
			hasNoSandbox = true
		}
		if arg == "--user-data-dir=/tmp/profile" {
			hasUserDataDir = true
		}
	}

	if !hasNoSandbox {
		t.Fatalf("expected --no-sandbox in chrome args")
	}
	if !hasUserDataDir {
		t.Fatalf("expected user-data-dir arg")
	}
}

func TestWaitForDevToolsWS_ReturnsWebSocketURLFromActivePortFile(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "DevToolsActivePort"), []byte("12345\n/devtools/browser/abc\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	got, err := waitForDevToolsWS(dir, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("expected ready websocket, got %v", err)
	}
	if got != "ws://127.0.0.1:12345/devtools/browser/abc" {
		t.Fatalf("unexpected ws url: %s", got)
	}
}

func TestBuildChromeArgs_KeepsAboutBlankBootstrapPage(t *testing.T) {
	args := buildChromeArgs(BrowserOptions{UserDataDir: "/tmp/profile", Headless: true})
	if args[len(args)-1] != "about:blank" {
		t.Fatalf("expected about:blank bootstrap page, got %+v", args)
	}
}

func TestBuildChromeArgs_HeadedWhenLiveViewEnabled(t *testing.T) {
	args := buildChromeArgs(BrowserOptions{UserDataDir: "/tmp/profile", Headless: false})
	for _, arg := range args {
		if arg == "--headless=new" {
			t.Fatalf("did not expect headless arg in headed mode: %+v", args)
		}
	}
}

func TestBuildChromeArgs_AppliesFingerprintAndProxyOptions(t *testing.T) {
	proxy, err := ParseProxyServer("http://user:pass@proxy.example.com:8080")
	if err != nil {
		t.Fatalf("parse proxy: %v", err)
	}
	fp := FingerprintFromSeed("fp_seed_1")
	args := buildChromeArgs(BrowserOptions{
		UserDataDir: "/tmp/profile",
		Headless:    true,
		Fingerprint: fp,
		Proxy:       proxy,
	})

	want := []string{
		"--proxy-server=http://proxy.example.com:8080",
		fmt.Sprintf("--window-size=%d,%d", fp.ViewportWidth, fp.ViewportHeight),
		"--lang=" + fp.Locale,
		"--user-agent=" + fp.UserAgent,
		"--force-webrtc-ip-handling-policy=disable_non_proxied_udp",
	}
	for _, expected := range want {
		found := false
		for _, arg := range args {
			if arg == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected arg %q in %+v", expected, args)
		}
	}
	for _, arg := range args {
		if strings.Contains(arg, "user:pass") {
			t.Fatalf("proxy credentials must not be placed in chrome args: %+v", args)
		}
	}
}

func TestParseProxyServer_MasksCredentialsAndRejectsInvalidScheme(t *testing.T) {
	proxy, err := ParseProxyServer("socks5://user:pass@proxy.example.com:1080")
	if err != nil {
		t.Fatalf("parse proxy: %v", err)
	}
	if proxy.ChromeServer != "socks5://proxy.example.com:1080" {
		t.Fatalf("unexpected chrome server: %+v", proxy)
	}
	if proxy.Masked != "socks5://***:***@proxy.example.com:1080" {
		t.Fatalf("unexpected masked proxy: %+v", proxy)
	}
	if proxy.Username != "user" || proxy.Password != "pass" {
		t.Fatalf("unexpected credentials: %+v", proxy)
	}

	_, err = ParseProxyServer("ftp://proxy.example.com:21")
	if err == nil {
		t.Fatalf("expected invalid proxy server")
	}
	if !errors.Is(err, ErrInvalidProxyServer) {
		t.Fatalf("expected ErrInvalidProxyServer, got %v", err)
	}
	_, err = ParseProxyServer("https://proxy.example.com:443")
	if err == nil {
		t.Fatalf("expected https proxy to be rejected")
	}
	if !errors.Is(err, ErrInvalidProxyServer) {
		t.Fatalf("expected ErrInvalidProxyServer, got %v", err)
	}
}

func TestFingerprintFromSeed_IsStableAndVariesBySeed(t *testing.T) {
	first := FingerprintFromSeed("fp_seed_1")
	again := FingerprintFromSeed("fp_seed_1")
	other := FingerprintFromSeed("fp_seed_2")

	if first != again {
		t.Fatalf("expected same seed to produce same fingerprint: %+v %+v", first, again)
	}
	if first == other {
		t.Fatalf("expected different seeds to produce different fingerprint: %+v", first)
	}
	if first.Locale == "" || first.Timezone == "" || first.UserAgent == "" || first.ViewportWidth == 0 || first.HardwareConcurrency == 0 {
		t.Fatalf("expected complete fingerprint: %+v", first)
	}
}

func TestFingerprintInitScript_ContainsStableOverrides(t *testing.T) {
	script := fingerprintInitScript(FingerprintFromSeed("fp_seed_1"))
	for _, expected := range []string{"Navigator", "deviceMemory", "WebGLRenderingContext", "HTMLCanvasElement", "AudioContext", "RTCPeerConnection"} {
		if !strings.Contains(script, expected) {
			t.Fatalf("expected init script to contain %q, got %s", expected, script)
		}
	}
}

func TestSnapshotOutput_UsesPageAsSingleStructure(t *testing.T) {
	out := SnapshotOutput{
		SnapshotID: "snap_1",
		Page: PageSnapshot{
			URL:   "https://example.com",
			Title: "Example",
			Groups: map[string]PageTable{
				"buttons": {
					Columns: []string{"ref", "tag", "text"},
					Rows:    [][]any{{"e1", "BUTTON", "Submit"}},
				},
			},
		},
	}

	if out.Page.URL == "" || out.Page.Title == "" {
		t.Fatalf("page metadata missing")
	}
	if _, ok := out.Page.Groups["buttons"]; !ok {
		t.Fatalf("expected buttons group")
	}
}

func TestEvaluate_AwaitsRuntimePromise(t *testing.T) {
	source, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "WithAwaitPromise(true)") {
		t.Fatalf("Evaluate must await the async runtime script promise")
	}
}

func TestValidateActionRef_RejectsTextRefForClick(t *testing.T) {
	err := validateActionRef("click", browserrt.RefState{
		Ref:  "t1",
		Kind: "text",
	})
	if err == nil {
		t.Fatalf("expected invalid ref for click on text ref")
	}
	if err != browserrt.ErrInvalidRef {
		t.Fatalf("expected ErrInvalidRef, got %v", err)
	}
}

func TestValidateActionRef_AllowsTextRefForScrollIntoView(t *testing.T) {
	err := validateActionRef("scrollIntoView", browserrt.RefState{
		Ref:  "t1",
		Kind: "text",
	})
	if err != nil {
		t.Fatalf("expected scrollIntoView to allow text refs, got %v", err)
	}
}

func TestUploadAfterNavigate_UploadsScreenshotWhenS3PathProvided(t *testing.T) {
	store := &fakeAssetStore{}
	svc := &Service{
		assets: store,
		capturePNG: func(context.Context) ([]byte, error) {
			return []byte("png-bytes"), nil
		},
	}

	err := svc.uploadAfterNavigate(context.Background(), "s3://browserd-snapshots/team_1/conv_1/1737373333.png")
	if err != nil {
		t.Fatalf("uploadAfterNavigate returned error: %v", err)
	}
	if len(store.puts) != 1 {
		t.Fatalf("expected one put, got %+v", store.puts)
	}
	if store.puts[0].URI != "s3://browserd-snapshots/team_1/conv_1/1737373333.png" {
		t.Fatalf("unexpected upload uri: %+v", store.puts[0])
	}
	if store.puts[0].ContentType != "image/png" {
		t.Fatalf("unexpected content type: %+v", store.puts[0])
	}
	if string(store.puts[0].Body) != "png-bytes" {
		t.Fatalf("unexpected body: %+v", store.puts[0])
	}
}

func TestUploadAfterNavigate_UsesRequestedBucketInsteadOfProfileBucket(t *testing.T) {
	store := &fakeAssetStore{}
	svc := &Service{
		assets: store,
		capturePNG: func(context.Context) ([]byte, error) {
			return []byte("png-bytes"), nil
		},
	}

	err := svc.uploadAfterNavigate(context.Background(), "s3://separate-snapshot-bucket/team_1/conv_1/1737373333.png")
	if err != nil {
		t.Fatalf("uploadAfterNavigate returned error: %v", err)
	}
	if len(store.puts) != 1 || store.puts[0].URI != "s3://separate-snapshot-bucket/team_1/conv_1/1737373333.png" {
		t.Fatalf("expected requested snapshot bucket to be used, got %+v", store.puts)
	}
}

func TestUploadAfterNavigate_ReturnsCaptureError(t *testing.T) {
	svc := &Service{
		assets: &fakeAssetStore{},
		capturePNG: func(context.Context) ([]byte, error) {
			return nil, errors.New("capture failed")
		},
	}

	err := svc.uploadAfterNavigate(context.Background(), "s3://browserd-snapshots/team_1/conv_1/1737373333.png")
	if err == nil || err.Error() != "capture failed" {
		t.Fatalf("expected capture failure, got %v", err)
	}
}
