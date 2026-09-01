package browser

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"browserd/internal/profile"
	browserrt "browserd/internal/runtime"
	"browserd/internal/session"
)

type fakeAssetStore struct {
	puts []fakeAssetPut
	gets []string
	get  fakeAssetGet
	err  error
}

type fakeAssetPut struct {
	URI         string
	Body        []byte
	ContentType string
}

type fakeAssetGet struct {
	Body        []byte
	ContentType string
	Err         error
}

type fakeUploadSessionManager struct {
	info session.SessionInfo
	err  error
}

func (f fakeUploadSessionManager) Create(session.CreateInput) (session.CreateOutput, error) {
	return session.CreateOutput{}, errors.New("not implemented")
}

func (f fakeUploadSessionManager) Commit(string, session.CommitInput) (session.CommitOutput, error) {
	return session.CommitOutput{}, errors.New("not implemented")
}

func (f fakeUploadSessionManager) Delete(string) error {
	return errors.New("not implemented")
}

func (f fakeUploadSessionManager) Get(string) (session.SessionInfo, error) {
	if f.err != nil {
		return session.SessionInfo{}, f.err
	}
	return f.info, nil
}

func (f fakeUploadSessionManager) Touch(string) error {
	if f.err != nil {
		return f.err
	}
	return nil
}

func (f fakeUploadSessionManager) ClaimExpired(time.Time) []session.SessionInfo {
	return nil
}

func TestNewService_AcceptsProxyHopOptions(t *testing.T) {
	svc := NewServiceWithOptions(ServiceOptions{
		Sessions: session.NewManager(session.ManagerOptions{
			Store:      profile.NewMemoryStore(),
			Workdir:    t.TempDir(),
			CDPBaseURL: "ws://browserd:9222/devtools/browser",
		}),
		State: browserrt.NewState(),
		ProxyHop: ProxyHopOptions{
			Mode:        "cloudflare-worker",
			WorkerURL:   "wss://proxy-hop.example.workers.dev/tunnel",
			WorkerToken: "worker-token",
		},
	})

	if svc.proxyHop.Mode != "cloudflare-worker" {
		t.Fatalf("proxy hop was not stored: %+v", svc.proxyHop)
	}
}

func TestServiceProxyHopMode_RequiresCompleteWorkerConfig(t *testing.T) {
	proxy, err := ParseProxyServer("http://user:pass@proxy.example.com:8080")
	if err != nil {
		t.Fatal(err)
	}
	svc := NewServiceWithOptions(ServiceOptions{
		ProxyHop: ProxyHopOptions{Mode: "cloudflare-worker"},
	})

	useHop, err := svc.proxyHopMode(proxy)

	if err == nil {
		t.Fatalf("expected missing proxy hop config error")
	}
	if !errors.Is(err, ErrProxyHopConfigMissing) {
		t.Fatalf("expected ErrProxyHopConfigMissing, got %v", err)
	}
	if useHop {
		t.Fatalf("did not expect proxy hop with incomplete config")
	}
}

func (f *fakeAssetStore) Put(_ context.Context, uri string, body []byte, contentType string) error {
	f.puts = append(f.puts, fakeAssetPut{URI: uri, Body: append([]byte(nil), body...), ContentType: contentType})
	return f.err
}

func (f *fakeAssetStore) Get(_ context.Context, uri string) ([]byte, string, error) {
	f.gets = append(f.gets, uri)
	if f.get.Err != nil {
		return nil, "", f.get.Err
	}
	return append([]byte(nil), f.get.Body...), f.get.ContentType, nil
}

func TestActType_RequiresRef(t *testing.T) {
	svc := NewServiceWithOptions(ServiceOptions{
		Sessions: session.NewManager(session.ManagerOptions{
			Store:      profile.NewMemoryStore(),
			Workdir:    t.TempDir(),
			CDPBaseURL: "ws://browserd:9222/devtools/browser",
		}),
		State: browserrt.NewState(),
	})

	_, err := svc.Act("rt_1", ActInput{Action: "type", Text: "hello"})

	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected invalid request for missing type ref, got %v", err)
	}

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

func TestChromedpErrorLoggerSuppressesIPAddressSpacePrivateDecodeNoise(t *testing.T) {
	var logs []string
	logger := chromedpErrorLogger(func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	})

	logger("could not unmarshal event: json: unable to unmarshal JSON string into Go network.IPAddressSpace within %q: unknown IPAddressSpace value: Private", "/clientSecurityState/initiatorIPAddressSpace")

	if len(logs) != 0 {
		t.Fatalf("expected IPAddressSpace Private decode noise to be suppressed, got %q", logs)
	}
}

func TestChromedpErrorLoggerKeepsOtherErrors(t *testing.T) {
	var logs []string
	logger := chromedpErrorLogger(func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	})

	logger("could not unmarshal event: %s", "unexpected payload")

	if len(logs) != 1 || !strings.Contains(logs[0], "unexpected payload") {
		t.Fatalf("expected unrelated chromedp error to be logged, got %q", logs)
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

func TestBuildChromeArgs_KioskModeHidesBrowserChrome(t *testing.T) {
	args := buildChromeArgs(BrowserOptions{UserDataDir: "/tmp/profile", Headless: false, KioskMode: true})
	if !containsArg(args, "--kiosk") {
		t.Fatalf("expected kiosk mode to hide browser chrome, got %+v", args)
	}
	if args[len(args)-1] != "about:blank" {
		t.Fatalf("expected kiosk mode bootstrap URL, got %+v", args)
	}
}

func TestEnsureBrowserUsesKioskModeForLiveRuntime(t *testing.T) {
	source, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "KioskMode:           liveEnabled") {
		t.Fatalf("live browser startup must enable kiosk mode so VNC coordinates match the page viewport")
	}
}

func TestBuildChromeArgs_AppliesFingerprintAndProxyOptions(t *testing.T) {
	proxy, err := ParseProxyServer("http://user:pass@proxy.example.com:8080")
	if err != nil {
		t.Fatalf("parse proxy: %v", err)
	}
	fp := FingerprintFromSeed("fp_seed_1")
	fp.ScreenWidth = 1920
	fp.ScreenHeight = 1080
	fp.ViewportWidth = 1366
	fp.ViewportHeight = 768
	args := buildChromeArgs(BrowserOptions{
		UserDataDir: "/tmp/profile",
		Headless:    true,
		Fingerprint: fp,
		Proxy:       proxy,
	})

	want := []string{
		"--proxy-server=http://proxy.example.com:8080",
		fmt.Sprintf("--window-size=%d,%d", fp.ScreenWidth, fp.ScreenHeight),
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
	if containsArg(args, fmt.Sprintf("--window-size=%d,%d", fp.ViewportWidth, fp.ViewportHeight)) {
		t.Fatalf("expected chrome window size to use screen dimensions, got viewport size in %+v", args)
	}
	for _, arg := range args {
		if strings.Contains(arg, "user:pass") {
			t.Fatalf("proxy credentials must not be placed in chrome args: %+v", args)
		}
	}
}

func TestEnsureBrowserUsesFingerprintScreenSizeForLiveRuntime(t *testing.T) {
	fp := FingerprintFromSeed("fp_seed_1")
	fp.ScreenWidth = 1920
	fp.ScreenHeight = 1080
	fp.ViewportWidth = 1366
	fp.ViewportHeight = 768

	width, height, err := liveRuntimeDimensionsFromFingerprint(fp)
	if err != nil {
		t.Fatalf("live runtime dimensions: %v", err)
	}
	if width != 1920 || height != 1080 {
		t.Fatalf("expected live runtime to use screen size 1920x1080, got %dx%d", width, height)
	}
}

func TestBuildChromeArgs_UsesProxyHopLocalProxy(t *testing.T) {
	proxy, err := ParseProxyServer("http://user:pass@proxy.example.com:8080")
	if err != nil {
		t.Fatal(err)
	}
	args := buildChromeArgs(BrowserOptions{
		UserDataDir:         "/tmp/profile",
		Headless:            true,
		Proxy:               proxy,
		ProxyOverrideServer: "http://127.0.0.1:34567",
	})

	if !containsArg(args, "--proxy-server=http://127.0.0.1:34567") {
		t.Fatalf("expected local proxy override in args: %+v", args)
	}
	if containsArg(args, "--proxy-server=http://proxy.example.com:8080") {
		t.Fatalf("did not expect upstream proxy in chrome args: %+v", args)
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

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func TestFingerprintFromSeed_IsStableAndVariesBySeed(t *testing.T) {
	first := FingerprintFromSeed("fp_seed_1")
	again := FingerprintFromSeed("fp_seed_1")
	other := FingerprintFromSeed("fp_seed_2")

	if !reflect.DeepEqual(first, again) {
		t.Fatalf("expected same seed to produce same fingerprint: %+v %+v", first, again)
	}
	if reflect.DeepEqual(first, other) {
		t.Fatalf("expected different seeds to produce different fingerprint: %+v", first)
	}
	if first.Locale == "" || first.Timezone == "" || first.UserAgent == "" || first.ViewportWidth == 0 || first.HardwareConcurrency == 0 {
		t.Fatalf("expected complete fingerprint: %+v", first)
	}
}

func TestFingerprintInitScript_ContainsStableOverrides(t *testing.T) {
	script := fingerprintInitScript(FingerprintFromSeed("fp_seed_1"))
	for _, expected := range []string{"Navigator", "deviceMemory", "WebGLRenderingContext", "AudioContext", "RTCPeerConnection"} {
		if !strings.Contains(script, expected) {
			t.Fatalf("expected init script to contain %q, got %s", expected, script)
		}
	}
}

func TestFingerprintInitScript_DoesNotCorruptCanvasDataURLs(t *testing.T) {
	script := fingerprintInitScript(FingerprintFromSeed("fp_seed_1"))

	for _, forbidden := range []string{"HTMLCanvasElement.prototype.toDataURL", "canvasMark", `value + ":"`} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("fingerprint init script must not alter canvas data URLs with %q: %s", forbidden, script)
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

func TestAct_ClickSupportsViewportCoordinates(t *testing.T) {
	source, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, marker := range []string{"func (s *Service) trustedClickPoint", "input.X <= 0 || input.Y <= 0", "strings.TrimSpace(input.Ref) == \"\""} {
		if !strings.Contains(text, marker) {
			t.Fatalf("Act click coordinate path missing %q", marker)
		}
	}
}

func TestAct_PasteSupportsViewportCoordinates(t *testing.T) {
	source, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, marker := range []string{"func (s *Service) trustedPaste", "input.X > 0 && input.Y > 0", "trustedClickTarget(ctx, runtimeSessionID, target, input)"} {
		if !strings.Contains(text, marker) {
			t.Fatalf("paste coordinate path missing %q", marker)
		}
	}
}

func TestAct_ScrollUsesTrustedMouseWheel(t *testing.T) {
	source, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, marker := range []string{`case "scroll":`, "trustedScroll", "cdinput.MouseWheel", "WithDeltaX(input.DeltaX)", "WithDeltaY(input.DeltaY)"} {
		if !strings.Contains(text, marker) {
			t.Fatalf("Act scroll trusted mouse wheel path missing %q", marker)
		}
	}
	if strings.Contains(text, `window.scrollBy`) {
		t.Fatalf("scroll action must not mutate page scroll position through DOM")
	}
}

func TestAct_PasteUsesClipboardAndKeyboardShortcut(t *testing.T) {
	source, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, marker := range []string{`case "paste":`, "trustedPaste", "navigator.clipboard.write", "ClipboardItem", "Key: \"v\", Modifiers: cdinput.ModifierCtrl"} {
		if !strings.Contains(text, marker) {
			t.Fatalf("Act paste trusted clipboard path missing %q", marker)
		}
	}
	for _, forbidden := range []string{"innerHTML =", "textContent =", "execCommand"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("paste action must not write editor DOM directly with %q", forbidden)
		}
	}
}

func TestWaitFor_ImplementsPlaywrightStyleActionabilityChecks(t *testing.T) {
	source, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, marker := range []string{
		"func (s *Service) WaitFor",
		"requestAnimationFrame",
		"elementFromPoint",
		"aria-disabled",
		"readOnly",
		"visibility",
		"display",
	} {
		if !strings.Contains(text, marker) {
			t.Fatalf("waitFor actionability implementation missing %q", marker)
		}
	}
}

func TestAct_HoverUsesTrustedPointerMovement(t *testing.T) {
	source, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, `case "hover":`) || !strings.Contains(text, "trustedMoveTarget") {
		t.Fatalf("hover must use trusted pointer movement")
	}
	if strings.Contains(text, `new MouseEvent("mouseover"`) {
		t.Fatalf("hover must not synthesize DOM mouse events")
	}
}

func TestTrustedMoveTargetPublishesEveryPointerStep(t *testing.T) {
	source, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	required := "cdinput.DispatchMouseEvent(cdinput.MouseMoved, point.X, point.Y).WithButton(cdinput.None).Do(ctx)"
	moveIndex := strings.Index(text, required)
	if moveIndex < 0 {
		t.Fatalf("trusted pointer movement missing mouse move dispatch")
	}
	publishIndex := strings.Index(text[moveIndex:], "s.setPointer(runtimeSessionID, point, viewport)")
	sleepIndex := strings.Index(text[moveIndex:], "sleepBehavior(ctx, profile.MouseMoveStepDelay)")
	if publishIndex < 0 {
		t.Fatalf("trusted pointer movement must publish each intermediate pointer step")
	}
	if sleepIndex >= 0 && publishIndex > sleepIndex {
		t.Fatalf("trusted pointer movement must publish each step before delaying")
	}
}

func TestTrustedClickUsesPressedButtonsBitfield(t *testing.T) {
	source, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, "cdinput.DispatchMouseEvent(cdinput.MousePressed, end.X, end.Y).WithButton(button).WithButtons(mouseButtonsBitfield(button)).WithClickCount(clickCount).Do(ctx)") {
		t.Fatalf("trusted click must send pressed mouse events with buttons bitfield")
	}
	if !strings.Contains(text, "cdinput.DispatchMouseEvent(cdinput.MouseReleased, end.X, end.Y).WithButton(button).WithButtons(0).WithClickCount(clickCount).Do(ctx)") {
		t.Fatalf("trusted click must send released mouse events with cleared buttons bitfield")
	}
	for name, want := range map[string]string{
		"Left":   "return 1",
		"Right":  "return 2",
		"Middle": "return 4",
	} {
		if !strings.Contains(text, "case cdinput."+name+":\n\t\t"+want) {
			t.Fatalf("mouse button bitfield missing %s => %s", name, want)
		}
	}
}

func TestShouldDispatchTrustedMouseMoveThrottlesIntermediateMoves(t *testing.T) {
	last := pointerPoint{X: 10, Y: 10}
	if shouldDispatchTrustedMouseMove(last, pointerPoint{X: 20, Y: 10}, false) {
		t.Fatalf("expected short intermediate move to be virtual-only")
	}
	if !shouldDispatchTrustedMouseMove(last, pointerPoint{X: 34, Y: 10}, false) {
		t.Fatalf("expected sufficiently distant intermediate move to dispatch to CDP")
	}
	if !shouldDispatchTrustedMouseMove(last, pointerPoint{X: 11, Y: 10}, true) {
		t.Fatalf("expected final move to dispatch exactly")
	}
}

func TestAct_FillUsesTrustedTextInput(t *testing.T) {
	source, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, `case "fill":`) || !strings.Contains(text, "trustedTextInput(ctx, runtimeSessionID, ActInput") {
		t.Fatalf("fill must use trusted text input")
	}
	if strings.Contains(text, "chromedp.SetValue") {
		t.Fatalf("fill must not set DOM values directly")
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

func TestPersistScreenshot_UploadsToGeneratedS3Path(t *testing.T) {
	store := &fakeAssetStore{}
	svc := &Service{assets: store, assetBucket: "private"}

	out, err := svc.persistScreenshot(context.Background(), ScreenshotInput{
		Format:             "png",
		ScreenshotS3Prefix: "/browser-screenshots/2026-08/team_1/conv_1/",
	}, validPNGBytes(), "png", "image/png")
	if err != nil {
		t.Fatalf("persistScreenshot returned error: %v", err)
	}
	if out.ScreenshotID == "" || !strings.HasSuffix(out.ScreenshotID, ".png") {
		t.Fatalf("expected png screenshot id, got %+v", out)
	}
	wantPath := "/browser-screenshots/2026-08/team_1/conv_1/" + out.ScreenshotID
	if out.S3Path != wantPath {
		t.Fatalf("unexpected s3 path: got %q want %q", out.S3Path, wantPath)
	}
	wantURI := "s3://private/browser-screenshots/2026-08/team_1/conv_1/" + out.ScreenshotID
	if len(store.puts) != 1 || store.puts[0].URI != wantURI {
		t.Fatalf("expected one upload to generated path, got %+v", store.puts)
	}
	if strings.Contains(fmt.Sprintf("%+v", out), "base64") {
		t.Fatalf("output must not contain base64 fields: %+v", out)
	}
}

func TestScreenshotMode_DefaultsToViewport(t *testing.T) {
	got, err := normalizeScreenshotMode(ScreenshotInput{})
	if err != nil {
		t.Fatalf("normalizeScreenshotMode returned error: %v", err)
	}
	if got != "viewport" {
		t.Fatalf("expected viewport default, got %q", got)
	}
}

func TestScreenshotMode_RequiresSelectorForSelectorMode(t *testing.T) {
	_, err := normalizeScreenshotMode(ScreenshotInput{Mode: "selector"})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected selector mode without selector to fail, got %v", err)
	}
}

func TestScreenshotMode_RejectsSelectorOutsideSelectorMode(t *testing.T) {
	_, err := normalizeScreenshotMode(ScreenshotInput{Mode: "viewport", Selector: "form"})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected selector with viewport mode to fail, got %v", err)
	}
}

func TestScreenshotMode_RejectsUnsupportedMode(t *testing.T) {
	_, err := normalizeScreenshotMode(ScreenshotInput{Mode: "element", Selector: "form"})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected unsupported mode to fail, got %v", err)
	}
}

func TestScreenshotMode_RejectsRef(t *testing.T) {
	_, err := normalizeScreenshotMode(ScreenshotInput{Ref: "e1"})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ref screenshot to fail, got %v", err)
	}
}

func TestScreenshotMode_RejectsFullPage(t *testing.T) {
	_, err := normalizeScreenshotMode(ScreenshotInput{FullPage: true})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected fullPage screenshot to fail, got %v", err)
	}
}

func TestPersistScreenshot_RejectsPNGContentMismatch(t *testing.T) {
	store := &fakeAssetStore{}
	svc := &Service{assets: store, assetBucket: "private"}

	_, err := svc.persistScreenshot(context.Background(), ScreenshotInput{
		Format:             "png",
		ScreenshotS3Prefix: "/browser-screenshots/2026-08/team_1/conv_1/",
	}, []byte{0xff, 0xd8, 0xff, 0xe0}, "png", "image/png")
	if !errors.Is(err, ErrScreenshotFailed) {
		t.Fatalf("expected screenshot failure for png metadata with jpeg bytes, got %v", err)
	}
	if len(store.puts) != 0 {
		t.Fatalf("mismatched screenshot bytes must not be uploaded, got %+v", store.puts)
	}
}

func TestPersistScreenshot_RequiresS3Prefix(t *testing.T) {
	svc := &Service{assets: &fakeAssetStore{}, assetBucket: "private"}
	_, err := svc.persistScreenshot(context.Background(), ScreenshotInput{}, validPNGBytes(), "png", "image/png")
	if err == nil {
		t.Fatalf("expected missing screenshotS3Prefix to fail")
	}
}

func TestPersistScreenshot_RejectsFullS3Prefix(t *testing.T) {
	svc := &Service{assets: &fakeAssetStore{}, assetBucket: "private"}
	_, err := svc.persistScreenshot(context.Background(), ScreenshotInput{
		ScreenshotS3Prefix: "s3://private/browser-screenshots/2026-08/team_1/conv_1/",
	}, validPNGBytes(), "png", "image/png")
	if err == nil {
		t.Fatalf("expected full s3 screenshot prefix to fail")
	}
}

func validPNGBytes() []byte {
	return append([]byte(nil), []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}...)
}

func TestScreenshotExt_RejectsJPEGUntilEncoderExists(t *testing.T) {
	if _, _, err := screenshotExt("jpeg"); err == nil {
		t.Fatalf("expected jpeg to be rejected until screenshot bytes are encoded as jpeg")
	}
}

func TestUploadFilesUsesFileChooserFlow(t *testing.T) {
	source, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, marker := range []string{
		"page.SetInterceptFileChooserDialog(true)",
		"*page.EventFileChooserOpened",
		"dom.SetFileInputFiles(filePaths).WithBackendNodeID",
		"trustedClick(ctx, runtimeSessionID, selector",
	} {
		if !strings.Contains(text, marker) {
			t.Fatalf("default upload must use file chooser flow, missing %q", marker)
		}
	}
	if strings.Contains(text, "chromedp.SetUploadFiles(selector") {
		t.Fatalf("default upload must not rely on direct SetUploadFiles selector injection")
	}
}

func TestUploadFilesAllowsDropZoneTextRef(t *testing.T) {
	state := browserrt.NewState()
	state.ReplaceSnapshot("rt_upload", browserrt.SnapshotState{
		SnapshotID: "snap_1",
		Refs: map[string]browserrt.RefState{
			"drop_1": {Ref: "drop_1", Kind: "text", TagName: "div", Selector: "#drop-zone"},
		},
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := fmt.Fprint(w, "video-bytes"); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	defer server.Close()
	sessionRoot := filepath.Join(t.TempDir(), "sessions", "rt_upload")
	manager := fakeUploadSessionManager{info: session.SessionInfo{RuntimeSessionID: "rt_upload", ProfileDir: filepath.Join(sessionRoot, "profile")}}
	svc := NewServiceWithOptions(ServiceOptions{Sessions: manager, State: state})
	var dropSelector string
	var dropPaths []string
	svc.dropFilesOnTarget = func(_ string, selector string, paths []string, _ int) error {
		dropSelector = selector
		dropPaths = append([]string(nil), paths...)
		return nil
	}

	out, err := svc.UploadFiles("rt_upload", UploadFilesInput{
		Ref: "drop_1",
		Files: []UploadFileSource{{
			URL:      server.URL + "/video.mp4",
			Filename: "video.mp4",
		}},
	})

	if err != nil {
		t.Fatalf("UploadFiles returned error: %v", err)
	}
	if dropSelector != "#drop-zone" {
		t.Fatalf("unexpected drop selector: %s", dropSelector)
	}
	if len(dropPaths) != 1 || filepath.Base(dropPaths[0]) != "video.mp4" {
		t.Fatalf("expected materialized video path, got %+v", dropPaths)
	}
	if !out.OK || out.Ref != "drop_1" || !reflect.DeepEqual(out.FileNames, []string{"video.mp4"}) {
		t.Fatalf("unexpected upload output: %+v", out)
	}
}

func TestUploadFilesRequiresRef(t *testing.T) {
	svc := NewServiceWithOptions(ServiceOptions{
		Sessions: session.NewManager(session.ManagerOptions{
			Store:      profile.NewMemoryStore(),
			Workdir:    t.TempDir(),
			CDPBaseURL: "ws://browserd:9222/devtools/browser",
		}),
		State: browserrt.NewState(),
	})

	_, err := svc.UploadFiles("rt_1", UploadFilesInput{Files: []UploadFileSource{{S3Path: "s3://bucket/key.png"}}})

	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected invalid request for missing ref, got %v", err)
	}
}

func TestUploadFilesAllowsPointTarget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := fmt.Fprint(w, "upload-by-point"); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	defer server.Close()
	sessionRoot := filepath.Join(t.TempDir(), "sessions", "rt_upload")
	manager := fakeUploadSessionManager{info: session.SessionInfo{RuntimeSessionID: "rt_upload", ProfileDir: filepath.Join(sessionRoot, "profile")}}
	svc := NewServiceWithOptions(ServiceOptions{Sessions: manager, State: browserrt.NewState()})
	var gotX float64
	var gotY float64
	var gotPaths []string
	svc.setFilesAtPoint = func(_ string, x float64, y float64, paths []string, _ int) error {
		gotX = x
		gotY = y
		gotPaths = append([]string(nil), paths...)
		return nil
	}

	out, err := svc.UploadFiles("rt_upload", UploadFilesInput{
		X: 212.5,
		Y: 144.25,
		Files: []UploadFileSource{{
			URL:      server.URL + "/note.txt",
			Filename: "note.txt",
		}},
	})

	if err != nil {
		t.Fatalf("UploadFiles returned error: %v", err)
	}
	if gotX != 212.5 || gotY != 144.25 {
		t.Fatalf("unexpected point: x=%v y=%v", gotX, gotY)
	}
	if len(gotPaths) != 1 || filepath.Base(gotPaths[0]) != "note.txt" {
		t.Fatalf("expected materialized file path, got %+v", gotPaths)
	}
	if !out.OK || out.Ref != "" || !reflect.DeepEqual(out.FileNames, []string{"note.txt"}) {
		t.Fatalf("unexpected upload output: %+v", out)
	}
}

func TestUploadFilesDownloadsS3FileAndSetsInput(t *testing.T) {
	state := browserrt.NewState()
	state.ReplaceSnapshot("rt_upload", browserrt.SnapshotState{
		SnapshotID: "snap_1",
		Refs: map[string]browserrt.RefState{
			"file_1": {Ref: "file_1", Kind: "element", TagName: "input", Selector: "#file"},
		},
	})
	store := &fakeAssetStore{get: fakeAssetGet{Body: []byte("image-bytes"), ContentType: "image/png"}}
	sessionRoot := filepath.Join(t.TempDir(), "sessions", "rt_upload")
	manager := fakeUploadSessionManager{info: session.SessionInfo{RuntimeSessionID: "rt_upload", ProfileDir: filepath.Join(sessionRoot, "profile")}}
	svc := NewServiceWithOptions(ServiceOptions{Sessions: manager, State: state, Assets: store})
	var setterSelector string
	var setterPaths []string
	svc.setFileInputFiles = func(_ string, selector string, paths []string, _ int) error {
		setterSelector = selector
		setterPaths = append([]string(nil), paths...)
		return nil
	}

	out, err := svc.UploadFiles("rt_upload", UploadFilesInput{
		Ref: "file_1",
		Files: []UploadFileSource{{
			S3Path:   "s3://browserd-assets/team_1/image.png",
			Filename: "cover.png",
		}},
	})

	if err != nil {
		t.Fatalf("UploadFiles returned error: %v", err)
	}
	if len(store.gets) != 1 || store.gets[0] != "s3://browserd-assets/team_1/image.png" {
		t.Fatalf("expected S3 file download, got %+v", store.gets)
	}
	if setterSelector != "#file" {
		t.Fatalf("unexpected selector: %s", setterSelector)
	}
	if len(setterPaths) != 1 {
		t.Fatalf("expected one materialized file, got %+v", setterPaths)
	}
	if filepath.Base(setterPaths[0]) != "cover.png" {
		t.Fatalf("expected sanitized filename cover.png, got %s", setterPaths[0])
	}
	raw, err := os.ReadFile(setterPaths[0])
	if err != nil {
		t.Fatalf("read materialized upload file: %v", err)
	}
	if string(raw) != "image-bytes" {
		t.Fatalf("unexpected materialized body: %q", string(raw))
	}
	if !out.OK || out.Ref != "file_1" || !reflect.DeepEqual(out.FileNames, []string{"cover.png"}) {
		t.Fatalf("unexpected upload output: %+v", out)
	}
}

func TestUploadFilesDownloadsURLFileAndSetsInput(t *testing.T) {
	state := browserrt.NewState()
	state.ReplaceSnapshot("rt_upload", browserrt.SnapshotState{
		SnapshotID: "snap_1",
		Refs: map[string]browserrt.RefState{
			"file_1": {Ref: "file_1", Kind: "element", TagName: "input", Selector: "#file"},
		},
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/media/cover.png" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "image/png")
		if _, err := fmt.Fprint(w, "image-by-url"); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	defer server.Close()
	sessionRoot := filepath.Join(t.TempDir(), "sessions", "rt_upload")
	manager := fakeUploadSessionManager{info: session.SessionInfo{RuntimeSessionID: "rt_upload", ProfileDir: filepath.Join(sessionRoot, "profile")}}
	svc := NewServiceWithOptions(ServiceOptions{Sessions: manager, State: state})
	var setterPaths []string
	svc.setFileInputFiles = func(_ string, _ string, paths []string, _ int) error {
		setterPaths = append([]string(nil), paths...)
		return nil
	}

	out, err := svc.UploadFiles("rt_upload", UploadFilesInput{
		Ref: "file_1",
		Files: []UploadFileSource{{
			URL:      server.URL + "/media/cover.png",
			Filename: "cover.png",
		}},
	})

	if err != nil {
		t.Fatalf("UploadFiles returned error: %v", err)
	}
	if len(setterPaths) != 1 {
		t.Fatalf("expected one materialized URL file, got %+v", setterPaths)
	}
	if filepath.Base(setterPaths[0]) != "cover.png" {
		t.Fatalf("expected sanitized filename cover.png, got %s", setterPaths[0])
	}
	raw, err := os.ReadFile(setterPaths[0])
	if err != nil {
		t.Fatalf("read materialized URL file: %v", err)
	}
	if string(raw) != "image-by-url" {
		t.Fatalf("unexpected materialized body: %q", string(raw))
	}
	if !out.OK || out.Ref != "file_1" || !reflect.DeepEqual(out.FileNames, []string{"cover.png"}) {
		t.Fatalf("unexpected upload output: %+v", out)
	}
}

func TestUploadFilesRejectsLocalPathOutsideSession(t *testing.T) {
	state := browserrt.NewState()
	state.ReplaceSnapshot("rt_upload", browserrt.SnapshotState{
		SnapshotID: "snap_1",
		Refs: map[string]browserrt.RefState{
			"file_1": {Ref: "file_1", Kind: "element", TagName: "input", Selector: "#file"},
		},
	})
	sessionRoot := filepath.Join(t.TempDir(), "sessions", "rt_upload")
	manager := fakeUploadSessionManager{info: session.SessionInfo{RuntimeSessionID: "rt_upload", ProfileDir: filepath.Join(sessionRoot, "profile")}}
	svc := NewServiceWithOptions(ServiceOptions{Sessions: manager, State: state})

	_, err := svc.UploadFiles("rt_upload", UploadFilesInput{
		Ref:   "file_1",
		Files: []UploadFileSource{{LocalPath: filepath.Join(t.TempDir(), "x.png")}},
	})

	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected invalid request for local path outside session, got %v", err)
	}
}

func TestUploadFilesReportsURLFetchFailure(t *testing.T) {
	state := browserrt.NewState()
	state.ReplaceSnapshot("rt_upload", browserrt.SnapshotState{
		SnapshotID: "snap_1",
		Refs: map[string]browserrt.RefState{
			"file_1": {Ref: "file_1", Kind: "element", TagName: "input", Selector: "#file"},
		},
	})
	sessionRoot := filepath.Join(t.TempDir(), "sessions", "rt_upload")
	manager := fakeUploadSessionManager{info: session.SessionInfo{RuntimeSessionID: "rt_upload", ProfileDir: filepath.Join(sessionRoot, "profile")}}
	svc := NewServiceWithOptions(ServiceOptions{Sessions: manager, State: state})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "missing image", http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	_, err := svc.UploadFiles("rt_upload", UploadFilesInput{
		Ref:   "file_1",
		Files: []UploadFileSource{{URL: server.URL + "/missing.png", Filename: "cover.png"}},
	})

	if !errors.Is(err, ErrUploadSourceFetchFailed) {
		t.Fatalf("expected upload source fetch failure, got %v", err)
	}
	if !strings.Contains(err.Error(), "HTTP 404") || !strings.Contains(err.Error(), "/missing.png") {
		t.Fatalf("expected actionable URL fetch error, got %v", err)
	}
}

func TestShouldBypassUploadURLProxyForLocalHosts(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"http://host.docker.internal:13021/asset.png", true},
		{"http://localhost:13021/asset.png", true},
		{"http://127.0.0.1:13021/asset.png", true},
		{"http://[::1]:13021/asset.png", true},
		{"https://cdn.example.test/asset.png", false},
	}
	for _, tc := range cases {
		u, err := url.Parse(tc.raw)
		if err != nil {
			t.Fatalf("parse %s: %v", tc.raw, err)
		}
		if got := shouldBypassUploadURLProxy(u); got != tc.want {
			t.Fatalf("shouldBypassUploadURLProxy(%s) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}
