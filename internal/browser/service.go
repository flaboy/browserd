package browser

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"browserd/internal/assets"
	browserrt "browserd/internal/runtime"
	"browserd/internal/session"

	"github.com/chromedp/chromedp"
)

var (
	ErrInvalidRequest        = errors.New("invalid request")
	ErrNavigationFailed      = errors.New("navigation failed")
	ErrActionFailed          = errors.New("action failed")
	ErrScreenshotFailed      = errors.New("screenshot failed")
	ErrPlaywrightUnavailable = errors.New("playwright not available")
)

type NavigateInput struct {
	URL                       string
	WaitUntil                 string
	TimeoutMs                 int
	AfterLoadScreenshotS3Path string
}

type NavigateOutput struct {
	URL             string `json:"url"`
	Title           string `json:"title,omitempty"`
	SnapshotCleared bool   `json:"snapshotCleared"`
}

type SnapshotInput struct {
	Mode string
}

type SnapshotRef struct {
	Ref     string `json:"ref"`
	Role    string `json:"role,omitempty"`
	Name    string `json:"name,omitempty"`
	Text    string `json:"text,omitempty"`
	TagName string `json:"tagName,omitempty"`
}

type PageTable struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

type PageSnapshot struct {
	URL    string               `json:"url,omitempty"`
	Title  string               `json:"title,omitempty"`
	Groups map[string]PageTable `json:"groups"`
}

type SnapshotOutput struct {
	SnapshotID string       `json:"snapshotId"`
	Page       PageSnapshot `json:"page"`
}

type ActInput struct {
	Action    string   `json:"action"`
	Ref       string   `json:"ref,omitempty"`
	Text      string   `json:"text,omitempty"`
	Key       string   `json:"key,omitempty"`
	Value     string   `json:"value,omitempty"`
	Values    []string `json:"values,omitempty"`
	TimeoutMs int      `json:"timeoutMs,omitempty"`
}

type ActOutput struct {
	OK     bool   `json:"ok"`
	Action string `json:"action"`
	Ref    string `json:"ref,omitempty"`
	URL    string `json:"url,omitempty"`
	Title  string `json:"title,omitempty"`
}

type ScreenshotInput struct {
	Ref      string `json:"ref,omitempty"`
	FullPage bool   `json:"fullPage,omitempty"`
	Format   string `json:"format,omitempty"`
	Quality  int    `json:"quality,omitempty"`
}

type ScreenshotOutput struct {
	ContentType string `json:"contentType"`
	Base64      string `json:"base64"`
	ByteLength  int    `json:"byteLength"`
}

type Service struct {
	sessions session.Manager
	state    *browserrt.State
	assets   assets.Store

	capturePNG func(context.Context) ([]byte, error)
	mu         sync.Mutex
	browsers   map[string]*activeBrowser
}

type activeBrowser struct {
	cmd         *exec.Cmd
	live        *LiveRuntime
	wsURL       string
	rootCtx     context.Context
	rootCancel  context.CancelFunc
	allocCtx    context.Context
	allocCancel context.CancelFunc
	pageCtx     context.Context
	pageCancel  context.CancelFunc
}

type snapshotRuntimeEnvelope struct {
	Page PageSnapshot                  `json:"page"`
	Refs map[string]browserrt.RefState `json:"refs"`
}

func NewService(sessions session.Manager, state *browserrt.State, assetStore assets.Store) *Service {
	if state == nil {
		state = browserrt.NewState()
	}
	return &Service{
		sessions:   sessions,
		state:      state,
		assets:     assetStore,
		capturePNG: capturePagePNG,
		browsers:   map[string]*activeBrowser{},
	}
}

func (s *Service) PrepareSession(runtimeSessionID string) error {
	_, err := s.ensureBrowser(runtimeSessionID)
	return err
}

func (s *Service) Close(runtimeSessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, ok := s.browsers[runtimeSessionID]
	if !ok {
		return nil
	}
	delete(s.browsers, runtimeSessionID)
	if b.cmd != nil && b.cmd.Process != nil {
		_ = b.cmd.Process.Kill()
		_, _ = b.cmd.Process.Wait()
	}
	if b.live != nil {
		_ = b.live.Stop(context.Background())
	}
	if b.pageCancel != nil {
		b.pageCancel()
	}
	if b.allocCancel != nil {
		b.allocCancel()
	}
	if b.rootCancel != nil {
		b.rootCancel()
	}
	return nil
}

func (s *Service) LiveProxyTarget(runtimeSessionID string) (string, error) {
	b, err := s.ensureBrowser(runtimeSessionID)
	if err != nil {
		return "", err
	}
	if b.live == nil {
		return "", ErrPlaywrightUnavailable
	}
	if err := b.live.Health(context.Background()); err != nil {
		return "", err
	}
	return b.live.ProxyTarget(), nil
}

func (s *Service) Navigate(runtimeSessionID string, input NavigateInput) (NavigateOutput, error) {
	if strings.TrimSpace(input.URL) == "" {
		return NavigateOutput{}, ErrInvalidRequest
	}
	ctx, cancel, err := s.newBrowserContext(runtimeSessionID, input.TimeoutMs)
	if err != nil {
		return NavigateOutput{}, err
	}
	defer cancel()

	var title string
	var url string
	if err := chromedp.Run(ctx,
		chromedp.Navigate(input.URL),
		chromedp.Title(&title),
		chromedp.Location(&url),
	); err != nil {
		return NavigateOutput{}, fmt.Errorf("%w: %v", ErrNavigationFailed, err)
	}
	if err := s.uploadAfterNavigate(ctx, input.AfterLoadScreenshotS3Path); err != nil {
		return NavigateOutput{}, fmt.Errorf("%w: %v", ErrScreenshotFailed, err)
	}

	s.state.ClearSnapshot(runtimeSessionID)
	return NavigateOutput{
		URL:             url,
		Title:           title,
		SnapshotCleared: true,
	}, nil
}

func (s *Service) uploadAfterNavigate(ctx context.Context, s3Path string) error {
	s3Path = strings.TrimSpace(s3Path)
	if s3Path == "" {
		return nil
	}
	if s.assets == nil {
		return fmt.Errorf("asset store not configured")
	}
	if s.capturePNG == nil {
		return fmt.Errorf("screenshot capture not configured")
	}
	png, err := s.capturePNG(ctx)
	if err != nil {
		return err
	}
	return s.assets.Put(ctx, s3Path, png, "image/png")
}

func capturePagePNG(ctx context.Context) ([]byte, error) {
	var png []byte
	if err := chromedp.Run(ctx, chromedp.FullScreenshot(&png, 90)); err != nil {
		return nil, err
	}
	return png, nil
}

func (s *Service) Snapshot(runtimeSessionID string, input SnapshotInput) (SnapshotOutput, error) {
	ctx, cancel, err := s.newBrowserContext(runtimeSessionID, 20_000)
	if err != nil {
		return SnapshotOutput{}, err
	}
	defer cancel()

	var envelope snapshotRuntimeEnvelope
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(browserSnapshotRuntimeScript, &envelope),
	); err != nil {
		return SnapshotOutput{}, fmt.Errorf("%w: %v", ErrActionFailed, err)
	}

	snapshotID := fmt.Sprintf("snap_%d", time.Now().UnixNano())
	page := envelope.Page
	if page.Groups == nil {
		page.Groups = map[string]PageTable{}
	}
	refs := envelope.Refs
	if refs == nil {
		refs = map[string]browserrt.RefState{}
	}
	for ref, state := range refs {
		if state.Ref == "" {
			state.Ref = ref
		}
		state.SnapshotID = snapshotID
		refs[ref] = state
	}

	s.state.ReplaceSnapshot(runtimeSessionID, browserrt.SnapshotState{
		SnapshotID: snapshotID,
		Page: browserrt.PageState{
			URL:    page.URL,
			Title:  page.Title,
			Groups: pageGroupsToState(page.Groups),
		},
		Refs: refs,
	})

	return SnapshotOutput{
		SnapshotID: snapshotID,
		Page:       page,
	}, nil
}

func (s *Service) Act(runtimeSessionID string, input ActInput) (ActOutput, error) {
	refState, err := s.state.GetRef(runtimeSessionID, input.Ref)
	if err != nil {
		return ActOutput{}, err
	}
	ctx, cancel, err := s.newBrowserContext(runtimeSessionID, input.TimeoutMs)
	if err != nil {
		return ActOutput{}, err
	}
	defer cancel()

	selector := refState.Selector
	if err := validateActionRef(input.Action, refState); err != nil {
		return ActOutput{}, err
	}
	switch input.Action {
	case "click":
		err = chromedp.Run(ctx, chromedp.Click(selector, chromedp.ByQuery))
	case "doubleClick":
		err = chromedp.Run(ctx, chromedp.DoubleClick(selector, chromedp.ByQuery))
	case "hover":
		err = chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf(`(() => {
      const el = document.querySelector(%q);
      if (!el) throw new Error("missing");
      el.dispatchEvent(new MouseEvent("mouseover", { bubbles: true }));
      el.dispatchEvent(new MouseEvent("mouseenter", { bubbles: true }));
      return true;
    })()`, selector), nil))
	case "type":
		err = chromedp.Run(ctx, chromedp.SendKeys(selector, input.Text, chromedp.ByQuery))
	case "fill":
		err = chromedp.Run(ctx, chromedp.SetValue(selector, input.Value, chromedp.ByQuery))
	case "press":
		err = chromedp.Run(ctx, chromedp.SendKeys(selector, input.Key, chromedp.ByQuery))
	case "scrollIntoView":
		err = chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf(`(() => { const el = document.querySelector(%q); if (!el) throw new Error("missing"); el.scrollIntoView({block:"center", inline:"center"}); return true; })()`, selector), nil))
	case "select":
		err = chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf(`(() => { const el = document.querySelector(%q); if (!el) throw new Error("missing"); const values = %s; for (const opt of el.options ?? []) { opt.selected = values.includes(opt.value); } el.dispatchEvent(new Event("input", {bubbles:true})); el.dispatchEvent(new Event("change", {bubbles:true})); return true; })()`, selector, jsStringArray(input.Values)), nil))
	case "waitFor":
		err = chromedp.Run(ctx, chromedp.WaitVisible(selector, chromedp.ByQuery))
	default:
		return ActOutput{}, ErrInvalidRequest
	}
	if err != nil {
		return ActOutput{}, fmt.Errorf("%w: %v", ErrActionFailed, err)
	}

	var url string
	var title string
	_ = chromedp.Run(ctx, chromedp.Location(&url), chromedp.Title(&title))
	return ActOutput{
		OK:     true,
		Action: input.Action,
		Ref:    input.Ref,
		URL:    url,
		Title:  title,
	}, nil
}

func validateActionRef(action string, ref browserrt.RefState) error {
	switch action {
	case "scrollIntoView":
		return nil
	case "click", "doubleClick", "hover", "type", "fill", "press", "select", "waitFor":
		if ref.Kind != "element" {
			return browserrt.ErrInvalidRef
		}
		return nil
	default:
		return ErrInvalidRequest
	}
}

func pageGroupsToState(groups map[string]PageTable) map[string]any {
	out := make(map[string]any, len(groups))
	for key, table := range groups {
		out[key] = table
	}
	return out
}

func (s *Service) Screenshot(runtimeSessionID string, input ScreenshotInput) (ScreenshotOutput, error) {
	ctx, cancel, err := s.newBrowserContext(runtimeSessionID, 20_000)
	if err != nil {
		return ScreenshotOutput{}, err
	}
	defer cancel()

	format := strings.ToLower(strings.TrimSpace(input.Format))
	if format == "" {
		format = "png"
	}
	if format != "png" && format != "jpeg" {
		return ScreenshotOutput{}, ErrInvalidRequest
	}

	var buf []byte
	switch {
	case input.Ref != "":
		if input.FullPage {
			return ScreenshotOutput{}, ErrInvalidRequest
		}
		refState, err := s.state.GetRef(runtimeSessionID, input.Ref)
		if err != nil {
			return ScreenshotOutput{}, err
		}
		if err := chromedp.Run(ctx, chromedp.Screenshot(refState.Selector, &buf, chromedp.ByQuery)); err != nil {
			return ScreenshotOutput{}, fmt.Errorf("%w: %v", ErrScreenshotFailed, err)
		}
	default:
		quality := 90
		if input.Quality > 0 {
			quality = input.Quality
		}
		if err := chromedp.Run(ctx, chromedp.FullScreenshot(&buf, quality)); err != nil {
			return ScreenshotOutput{}, fmt.Errorf("%w: %v", ErrScreenshotFailed, err)
		}
	}

	contentType := "image/png"
	if format == "jpeg" {
		contentType = "image/jpeg"
	}
	return ScreenshotOutput{
		ContentType: contentType,
		Base64:      base64.StdEncoding.EncodeToString(buf),
		ByteLength:  len(buf),
	}, nil
}

func (s *Service) newBrowserContext(runtimeSessionID string, timeoutMs int) (context.Context, context.CancelFunc, error) {
	b, err := s.ensureBrowser(runtimeSessionID)
	if err != nil {
		return nil, nil, err
	}
	if timeoutMs <= 0 {
		timeoutMs = 20_000
	}
	taskCtx, taskCancel := context.WithTimeout(b.pageCtx, time.Duration(timeoutMs)*time.Millisecond)
	cancel := func() {
		taskCancel()
	}
	return taskCtx, cancel, nil
}

func (s *Service) ensureBrowser(runtimeSessionID string) (*activeBrowser, error) {
	s.mu.Lock()
	b, ok := s.browsers[runtimeSessionID]
	s.mu.Unlock()
	if ok && b != nil && b.wsURL != "" && b.cmd != nil && b.cmd.Process != nil && b.pageCtx != nil {
		return b, nil
	}

	info, err := s.sessions.Get(runtimeSessionID)
	if err != nil {
		return nil, err
	}
	chromeBin := strings.TrimSpace(os.Getenv("CHROME_BIN"))
	if chromeBin == "" {
		chromeBin = "/usr/bin/chromium-browser"
	}
	if _, err := os.Stat(chromeBin); err != nil {
		return nil, ErrPlaywrightUnavailable
	}

	liveEnabled := liveModeEnabled()
	var liveRuntime *LiveRuntime
	var chromeEnv []string
	if liveEnabled {
		var err error
		liveRuntime, err = NewLiveRuntime(sessionRootFromProfileDir(info.ProfileDir))
		if err != nil {
			return nil, err
		}
		if err := liveRuntime.Start(context.Background()); err != nil {
			return nil, err
		}
		chromeEnv = liveRuntime.ChromeEnv()
	}

	cmd := exec.Command(chromeBin, buildChromeArgs(BrowserOptions{
		UserDataDir: info.ProfileDir,
		Headless:    !liveEnabled,
	})...)
	cmd.Env = append(cmd.Environ(), chromeEnv...)
	if err := cmd.Start(); err != nil {
		if liveRuntime != nil {
			_ = liveRuntime.Stop(context.Background())
		}
		return nil, err
	}

	wsURL, err := waitForDevToolsWS(info.ProfileDir, 5*time.Second)
	if err != nil {
		_ = cmd.Process.Kill()
		if liveRuntime != nil {
			_ = liveRuntime.Stop(context.Background())
		}
		return nil, err
	}

	rootCtx, rootCancel := context.WithCancel(context.Background())
	allocCtx, allocCancel := chromedp.NewRemoteAllocator(rootCtx, wsURL)
	pageCtx, pageCancel := chromedp.NewContext(allocCtx)
	if err := chromedp.Run(pageCtx); err != nil {
		pageCancel()
		allocCancel()
		rootCancel()
		_ = cmd.Process.Kill()
		if liveRuntime != nil {
			_ = liveRuntime.Stop(context.Background())
		}
		return nil, err
	}

	s.mu.Lock()
	ab := &activeBrowser{
		cmd:         cmd,
		live:        liveRuntime,
		wsURL:       wsURL,
		rootCtx:     rootCtx,
		rootCancel:  rootCancel,
		allocCtx:    allocCtx,
		allocCancel: allocCancel,
		pageCtx:     pageCtx,
		pageCancel:  pageCancel,
	}
	s.browsers[runtimeSessionID] = ab
	s.mu.Unlock()
	return ab, nil
}

func waitForDevToolsWS(profileDir string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	devtoolsFile := filepath.Join(profileDir, "DevToolsActivePort")
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(devtoolsFile)
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
			if len(lines) >= 2 {
				port := strings.TrimSpace(lines[0])
				path := strings.TrimSpace(lines[1])
				if port != "" && path != "" {
					return "ws://127.0.0.1:" + port + path, nil
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return "", errors.New("devtools websocket not ready")
}

func jsStringArray(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(values))
	for _, v := range values {
		parts = append(parts, fmt.Sprintf("%q", v))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

type BrowserOptions struct {
	UserDataDir string
	Headless    bool
}

func buildChromeArgs(opts BrowserOptions) []string {
	args := []string{
		"--disable-gpu",
		"--no-sandbox",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-dev-shm-usage",
		"--remote-debugging-port=0",
	}
	if opts.Headless {
		args = append(args, "--headless=new")
	}
	args = append(args, "--user-data-dir="+opts.UserDataDir, "about:blank")
	return args
}

func liveModeEnabled() bool {
	value := strings.TrimSpace(os.Getenv("BROWSERD_LIVE_ENABLED"))
	return strings.EqualFold(value, "true") || value == "1"
}
