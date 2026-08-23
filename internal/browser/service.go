package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"browserd/internal/assets"
	browserrt "browserd/internal/runtime"
	"browserd/internal/session"

	cdbrowser "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/dom"
	cdinput "github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

var (
	ErrInvalidRequest          = errors.New("invalid request")
	ErrInvalidKey              = errors.New("invalid key")
	ErrNavigationFailed        = errors.New("navigation failed")
	ErrActionFailed            = errors.New("action failed")
	ErrEvaluateFailed          = errors.New("evaluate failed")
	ErrUploadFilesFailed       = errors.New("upload files failed")
	ErrUploadSourceFetchFailed = errors.New("upload source fetch failed")
	ErrScreenshotFailed        = errors.New("screenshot failed")
	ErrPlaywrightUnavailable   = errors.New("playwright not available")
	ErrProxyHopFailed          = errors.New("proxy hop failed")
	ErrProfileCheckpointFailed = errors.New("profile checkpoint failed")
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
	Action        string   `json:"action"`
	Ref           string   `json:"ref,omitempty"`
	X             float64  `json:"x,omitempty"`
	Y             float64  `json:"y,omitempty"`
	DeltaX        float64  `json:"deltaX,omitempty"`
	DeltaY        float64  `json:"deltaY,omitempty"`
	Text          string   `json:"text,omitempty"`
	HTML          string   `json:"html,omitempty"`
	Key           string   `json:"key,omitempty"`
	Value         string   `json:"value,omitempty"`
	Values        []string `json:"values,omitempty"`
	Clear         bool     `json:"clear,omitempty"`
	Submit        bool     `json:"submit,omitempty"`
	Button        string   `json:"button,omitempty"`
	ClickCount    int      `json:"clickCount,omitempty"`
	MotionProfile string   `json:"motionProfile,omitempty"`
	TimeoutMs     int      `json:"timeoutMs,omitempty"`
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

type UploadFileSource struct {
	S3Path    string `json:"s3Path,omitempty"`
	LocalPath string `json:"localPath,omitempty"`
	URL       string `json:"url,omitempty"`
	Filename  string `json:"filename,omitempty"`
}

type UploadFilesInput struct {
	Ref       string             `json:"ref"`
	X         float64            `json:"x,omitempty"`
	Y         float64            `json:"y,omitempty"`
	Files     []UploadFileSource `json:"files"`
	TimeoutMs int                `json:"timeoutMs,omitempty"`
}

type UploadFilesOutput struct {
	OK        bool     `json:"ok"`
	Ref       string   `json:"ref"`
	FileNames []string `json:"fileNames"`
}

type EvaluateInput struct {
	Script    string `json:"script"`
	Args      []any  `json:"args,omitempty"`
	TimeoutMs int    `json:"timeoutMs,omitempty"`
	World     string `json:"world,omitempty"`
}

type EvaluateOutput struct {
	Result any    `json:"result"`
	URL    string `json:"url"`
	Title  string `json:"title"`
}

type Service struct {
	sessions session.Manager
	state    *browserrt.State
	assets   assets.Store
	proxyHop ProxyHopOptions

	capturePNG         func(context.Context) ([]byte, error)
	setFileInputFiles  func(runtimeSessionID string, selector string, filePaths []string, timeoutMs int) error
	setFilesAtPoint    func(runtimeSessionID string, x float64, y float64, filePaths []string, timeoutMs int) error
	dropFilesOnTarget  func(runtimeSessionID string, selector string, filePaths []string, timeoutMs int) error
	mu                 sync.Mutex
	browsers           map[string]*activeBrowser
	pointers           map[string]pointerState
	pointerSubscribers map[string]map[chan VirtualPointerSnapshot]struct{}
}

type pointerState struct {
	Point       pointerPoint
	Viewport    viewportRect
	Initialized bool
	ButtonDown  bool
}

type browserTarget struct {
	Selector string
	Rect     targetRect
	Editable bool
}

type ProxyHopOptions struct {
	Mode        string
	WorkerURL   string
	WorkerToken string
}

type ServiceOptions struct {
	Sessions session.Manager
	State    *browserrt.State
	Assets   assets.Store
	ProxyHop ProxyHopOptions
}

type activeBrowser struct {
	cmd          *exec.Cmd
	live         *LiveRuntime
	wsURL        string
	rootCtx      context.Context
	rootCancel   context.CancelFunc
	allocCtx     context.Context
	allocCancel  context.CancelFunc
	pageCtx      context.Context
	pageCancel   context.CancelFunc
	proxyAdapter *localProxyAdapter
}

type snapshotRuntimeEnvelope struct {
	Page PageSnapshot                  `json:"page"`
	Refs map[string]browserrt.RefState `json:"refs"`
}

func NewService(sessions session.Manager, state *browserrt.State, assetStore assets.Store) *Service {
	return NewServiceWithOptions(ServiceOptions{
		Sessions: sessions,
		State:    state,
		Assets:   assetStore,
	})
}

func NewServiceWithOptions(opts ServiceOptions) *Service {
	state := opts.State
	if state == nil {
		state = browserrt.NewState()
	}
	svc := &Service{
		sessions:           opts.Sessions,
		state:              state,
		assets:             opts.Assets,
		proxyHop:           opts.ProxyHop,
		capturePNG:         capturePagePNG,
		browsers:           map[string]*activeBrowser{},
		pointers:           map[string]pointerState{},
		pointerSubscribers: map[string]map[chan VirtualPointerSnapshot]struct{}{},
	}
	svc.setFileInputFiles = svc.defaultSetFileInputFiles
	svc.setFilesAtPoint = svc.defaultSetFilesAtPoint
	svc.dropFilesOnTarget = svc.defaultDropFilesOnTarget
	return svc
}

func (s *Service) PrepareSession(runtimeSessionID string) error {
	_, err := s.ensureBrowser(runtimeSessionID)
	return err
}

func (s *Service) Close(runtimeSessionID string) error {
	b, ok := s.removeActiveBrowser(runtimeSessionID)
	if !ok {
		return nil
	}
	if b.cmd != nil && b.cmd.Process != nil {
		_ = b.cmd.Process.Kill()
		_, _ = b.cmd.Process.Wait()
	}
	s.cleanupActiveBrowser(b)
	return nil
}

func (s *Service) Checkpoint(runtimeSessionID string) error {
	s.mu.Lock()
	b, ok := s.browsers[runtimeSessionID]
	s.mu.Unlock()
	if !ok {
		return nil
	}
	if err := s.closeBrowserGracefully(b, 15*time.Second); err != nil {
		return fmt.Errorf("%w: %v", ErrProfileCheckpointFailed, err)
	}
	if removed, ok := s.removeActiveBrowser(runtimeSessionID); ok {
		s.cleanupActiveBrowser(removed)
	}
	return nil
}

func (s *Service) removeActiveBrowser(runtimeSessionID string) (*activeBrowser, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.browsers[runtimeSessionID]
	if !ok {
		return nil, false
	}
	delete(s.browsers, runtimeSessionID)
	delete(s.pointers, runtimeSessionID)
	for ch := range s.pointerSubscribers[runtimeSessionID] {
		close(ch)
	}
	delete(s.pointerSubscribers, runtimeSessionID)
	return b, true
}

func (s *Service) cleanupActiveBrowser(b *activeBrowser) {
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
	if b.proxyAdapter != nil {
		_ = b.proxyAdapter.Close()
	}
}

func (s *Service) closeBrowserGracefully(b *activeBrowser, timeout time.Duration) error {
	if b == nil || strings.TrimSpace(b.wsURL) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	conn, _, _, err := ws.Dial(ctx, b.wsURL)
	if err != nil {
		if b.cmd != nil && b.cmd.Process != nil {
			return waitChromeProcessExit(ctx, b.cmd)
		}
		return err
	}
	if err := wsutil.WriteClientText(conn, []byte(`{"id":1,"method":"Browser.close"}`)); err != nil {
		_ = conn.Close()
		return err
	}
	_ = conn.Close()
	return waitChromeProcessExit(ctx, b.cmd)
}

func waitChromeProcessExit(ctx context.Context, cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	select {
	case err := <-done:
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				return nil
			}
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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
	if input.Action == "type" && input.Ref == "" {
		return ActOutput{}, ErrInvalidRequest
	}
	ctx, cancel, err := s.newBrowserContext(runtimeSessionID, input.TimeoutMs)
	if err != nil {
		return ActOutput{}, err
	}
	defer cancel()

	switch input.Action {
	case "click":
		if strings.TrimSpace(input.Ref) == "" {
			if input.X <= 0 || input.Y <= 0 {
				return ActOutput{}, ErrInvalidRequest
			}
			err = s.trustedClickPoint(ctx, runtimeSessionID, input)
		} else {
			refState, refErr := s.state.GetRef(runtimeSessionID, input.Ref)
			if refErr != nil {
				return ActOutput{}, refErr
			}
			if err := validateActionRef(input.Action, refState); err != nil {
				return ActOutput{}, err
			}
			err = s.trustedClick(ctx, runtimeSessionID, refState.Selector, input)
		}
	case "doubleClick":
		refState, refErr := s.state.GetRef(runtimeSessionID, input.Ref)
		if refErr != nil {
			return ActOutput{}, refErr
		}
		if err := validateActionRef(input.Action, refState); err != nil {
			return ActOutput{}, err
		}
		selector := refState.Selector
		err = chromedp.Run(ctx, chromedp.DoubleClick(selector, chromedp.ByQuery))
	case "hover":
		target, refErr := s.resolveActRef(ctx, runtimeSessionID, input.Ref)
		if refErr != nil {
			return ActOutput{}, refErr
		}
		_, _, err = s.trustedMoveTarget(ctx, runtimeSessionID, target, input)
	case "scroll":
		err = s.trustedScroll(ctx, runtimeSessionID, input)
	case "paste":
		err = s.trustedPaste(ctx, runtimeSessionID, input)
	case "type":
		err = s.trustedTextInput(ctx, runtimeSessionID, input)
	case "fill":
		err = s.trustedTextInput(ctx, runtimeSessionID, ActInput{
			Action:        "type",
			Ref:           input.Ref,
			Text:          input.Value,
			Clear:         true,
			Submit:        input.Submit,
			Button:        input.Button,
			ClickCount:    input.ClickCount,
			MotionProfile: input.MotionProfile,
			TimeoutMs:     input.TimeoutMs,
		})
	case "press":
		refState, refErr := s.state.GetRef(runtimeSessionID, input.Ref)
		if refErr != nil {
			return ActOutput{}, refErr
		}
		if err := validateActionRef(input.Action, refState); err != nil {
			return ActOutput{}, err
		}
		encodedKey, keyErr := normalizePressKey(input.Key)
		if keyErr != nil {
			return ActOutput{}, keyErr
		}
		selector := refState.Selector
		err = chromedp.Run(ctx, chromedp.SendKeys(selector, encodedKey, chromedp.ByQuery))
	case "scrollIntoView":
		refState, refErr := s.state.GetRef(runtimeSessionID, input.Ref)
		if refErr != nil {
			return ActOutput{}, refErr
		}
		if err := validateActionRef(input.Action, refState); err != nil {
			return ActOutput{}, err
		}
		selector := refState.Selector
		err = chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf(`(() => { const el = document.querySelector(%q); if (!el) throw new Error("missing"); el.scrollIntoView({block:"center", inline:"center"}); return true; })()`, selector), nil))
	case "select":
		refState, refErr := s.state.GetRef(runtimeSessionID, input.Ref)
		if refErr != nil {
			return ActOutput{}, refErr
		}
		if err := validateActionRef(input.Action, refState); err != nil {
			return ActOutput{}, err
		}
		selector := refState.Selector
		err = chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf(`(() => { const el = document.querySelector(%q); if (!el) throw new Error("missing"); const values = %s; for (const opt of el.options ?? []) { opt.selected = values.includes(opt.value); } el.dispatchEvent(new Event("input", {bubbles:true})); el.dispatchEvent(new Event("change", {bubbles:true})); return true; })()`, selector, jsStringArray(input.Values)), nil))
	case "waitFor":
		refState, refErr := s.state.GetRef(runtimeSessionID, input.Ref)
		if refErr != nil {
			return ActOutput{}, refErr
		}
		if err := validateActionRef(input.Action, refState); err != nil {
			return ActOutput{}, err
		}
		selector := refState.Selector
		err = chromedp.Run(ctx, chromedp.WaitVisible(selector, chromedp.ByQuery))
	default:
		return ActOutput{}, ErrInvalidRequest
	}
	if err != nil {
		return ActOutput{}, fmt.Errorf("%w: %v", ErrActionFailed, err)
	}
	if err := sleepBehavior(ctx, defaultBehaviorProfile().ActionAfterDelay); err != nil {
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

func (s *Service) trustedTextInput(ctx context.Context, runtimeSessionID string, input ActInput) error {
	if strings.TrimSpace(input.Text) == "" && !input.Clear {
		return ErrInvalidRequest
	}
	if input.Ref == "" {
		return ErrInvalidRequest
	}
	target, err := s.resolveActRef(ctx, runtimeSessionID, input.Ref)
	if err != nil {
		return err
	}
	if err := s.trustedClickTarget(ctx, runtimeSessionID, target, input); err != nil {
		return err
	}
	var editable bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(`(() => {
      const el = document.activeElement;
      if (!el) return false;
      const tag = (el.tagName || "").toLowerCase();
      return !!(el.isContentEditable || tag === "textarea" || (tag === "input" && !["button","checkbox","file","hidden","image","radio","range","reset","submit"].includes((el.type || "").toLowerCase())));
    })()`, &editable)); err != nil {
		return err
	}
	if !editable {
		return ErrInvalidRequest
	}
	profile := defaultBehaviorProfile()
	before, after := planTextInputKeys(input.Clear, input.Submit)
	return chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		for _, key := range before {
			if err := keyEventAction(key).Do(ctx); err != nil {
				return err
			}
			if err := sleepBehavior(ctx, profile.KeyAfterDelay); err != nil {
				return err
			}
		}
		parts := splitTextRunes(input.Text)
		for index, part := range parts {
			if err := cdinput.InsertText(part).Do(ctx); err != nil {
				return err
			}
			if index < len(parts)-1 {
				if err := sleepBehavior(ctx, profile.TypeRuneDelay); err != nil {
					return err
				}
			}
		}
		for _, key := range after {
			if err := sleepBehavior(ctx, profile.KeyAfterDelay); err != nil {
				return err
			}
			if err := keyEventAction(key).Do(ctx); err != nil {
				return err
			}
		}
		return nil
	}))
}

func (s *Service) trustedClick(ctx context.Context, runtimeSessionID string, selector string, input ActInput) error {
	target, err := queryTargetBySelector(ctx, selector, false)
	if err != nil {
		return err
	}
	return s.trustedClickTarget(ctx, runtimeSessionID, target, input)
}

func (s *Service) trustedClickPoint(ctx context.Context, runtimeSessionID string, input ActInput) error {
	target := browserTarget{
		Rect: targetRect{
			X:      input.X - 0.5,
			Y:      input.Y - 0.5,
			Width:  1,
			Height: 1,
		},
	}
	return s.trustedClickTarget(ctx, runtimeSessionID, target, input)
}

func (s *Service) trustedScroll(ctx context.Context, runtimeSessionID string, input ActInput) error {
	if input.DeltaX == 0 && input.DeltaY == 0 {
		return ErrInvalidRequest
	}
	viewport := viewportRect{Width: 1366, Height: 768}
	_ = chromedp.Run(ctx, chromedp.Evaluate(`(() => ({ width: window.innerWidth || 1366, height: window.innerHeight || 768 }))()`, &viewport))
	x := input.X
	y := input.Y
	if x <= 0 {
		x = viewport.Width / 2
	}
	if y <= 0 {
		y = viewport.Height / 2
	}
	target := browserTarget{
		Rect: targetRect{
			X:      x - 0.5,
			Y:      y - 0.5,
			Width:  1,
			Height: 1,
		},
	}
	end, viewport, err := s.trustedMoveTarget(ctx, runtimeSessionID, target, input)
	if err != nil {
		return err
	}
	return chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		if err := cdinput.DispatchMouseEvent(cdinput.MouseWheel, end.X, end.Y).
			WithDeltaX(input.DeltaX).
			WithDeltaY(input.DeltaY).
			Do(ctx); err != nil {
			return err
		}
		s.setPointer(runtimeSessionID, end, viewport)
		return nil
	}))
}

func (s *Service) trustedPaste(ctx context.Context, runtimeSessionID string, input ActInput) error {
	if strings.TrimSpace(input.Text) == "" && strings.TrimSpace(input.HTML) == "" {
		return ErrInvalidRequest
	}
	if input.Ref == "" {
		return ErrInvalidRequest
	}
	target, err := s.resolveActRef(ctx, runtimeSessionID, input.Ref)
	if err != nil {
		return err
	}
	if err := s.trustedClickTarget(ctx, runtimeSessionID, target, input); err != nil {
		return err
	}
	if input.Clear {
		for _, key := range []textInputKey{
			{Key: "a", Modifiers: cdinput.ModifierCtrl},
			{Key: kb.Backspace},
		} {
			if err := keyEventAction(key).Do(ctx); err != nil {
				return err
			}
			if err := sleepBehavior(ctx, defaultBehaviorProfile().KeyAfterDelay); err != nil {
				return err
			}
		}
	}
	if err := s.writeClipboard(ctx, input.Text, input.HTML); err != nil {
		return err
	}
	return keyEventAction(textInputKey{Key: "v", Modifiers: cdinput.ModifierCtrl}).Do(ctx)
}

func (s *Service) writeClipboard(ctx context.Context, text string, html string) error {
	var currentURL string
	_ = chromedp.Run(ctx, chromedp.Location(&currentURL))
	if origin := clipboardOrigin(currentURL); origin != "" {
		_ = cdbrowser.GrantPermissions([]cdbrowser.PermissionType{
			cdbrowser.PermissionTypeClipboardReadWrite,
			cdbrowser.PermissionTypeClipboardSanitizedWrite,
		}).WithOrigin(origin).Do(ctx)
	}
	textJSON, err := json.Marshal(text)
	if err != nil {
		return err
	}
	htmlJSON, err := json.Marshal(html)
	if err != nil {
		return err
	}
	script := fmt.Sprintf(`(async () => {
      const text = %s;
      const html = %s;
      if (!navigator.clipboard) throw new Error("clipboard unavailable");
      if (html && typeof ClipboardItem !== "undefined") {
        const item = new ClipboardItem({
          "text/plain": new Blob([text || ""], {type: "text/plain"}),
          "text/html": new Blob([html], {type: "text/html"})
        });
        await navigator.clipboard.write([item]);
        return true;
      }
      await navigator.clipboard.writeText(text || html);
      return true;
    })()`, string(textJSON), string(htmlJSON))
	var ok bool
	return chromedp.Run(ctx, chromedp.Evaluate(script, &ok, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
		return p.WithAwaitPromise(true)
	}))
}

func clipboardOrigin(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func (s *Service) trustedClickTarget(ctx context.Context, runtimeSessionID string, target browserTarget, input ActInput) error {
	profile := defaultBehaviorProfile()
	end, viewport, err := s.trustedMoveTarget(ctx, runtimeSessionID, target, input)
	if err != nil {
		return err
	}
	button := cdinput.Left
	switch input.Button {
	case "right":
		button = cdinput.Right
	case "middle":
		button = cdinput.Middle
	}
	clickCount := int64(input.ClickCount)
	if clickCount <= 0 {
		clickCount = 1
	}
	if err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		if err := sleepBehavior(ctx, profile.MouseBeforeDownDelay); err != nil {
			return err
		}
		if err := cdinput.DispatchMouseEvent(cdinput.MousePressed, end.X, end.Y).WithButton(button).WithClickCount(clickCount).Do(ctx); err != nil {
			return err
		}
		s.setPointerButton(runtimeSessionID, end, viewport, true)
		if err := sleepBehavior(ctx, profile.MouseDownUpDelay); err != nil {
			return err
		}
		if err := cdinput.DispatchMouseEvent(cdinput.MouseReleased, end.X, end.Y).WithButton(button).WithClickCount(clickCount).Do(ctx); err != nil {
			return err
		}
		s.setPointerButton(runtimeSessionID, end, viewport, false)
		return nil
	})); err != nil {
		return err
	}
	return nil
}

func (s *Service) trustedMoveTarget(ctx context.Context, runtimeSessionID string, target browserTarget, input ActInput) (pointerPoint, viewportRect, error) {
	profile := defaultBehaviorProfile()
	viewport := viewportRect{Width: 1366, Height: 768}
	_ = chromedp.Run(ctx, chromedp.Evaluate(`(() => ({ width: window.innerWidth || 1366, height: window.innerHeight || 768 }))()`, &viewport))
	start := s.pointerStart(runtimeSessionID, viewport)
	path := planPointerPath(start, target.Rect, viewport, input.MotionProfile)
	if len(path) == 0 {
		return pointerPoint{}, viewportRect{}, ErrInvalidRequest
	}
	if err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		for _, point := range path {
			if err := cdinput.DispatchMouseEvent(cdinput.MouseMoved, point.X, point.Y).WithButton(cdinput.None).Do(ctx); err != nil {
				return err
			}
			if err := sleepBehavior(ctx, profile.MouseMoveStepDelay); err != nil {
				return err
			}
		}
		return nil
	})); err != nil {
		return pointerPoint{}, viewportRect{}, err
	}
	end := path[len(path)-1]
	s.setPointer(runtimeSessionID, end, viewport)
	return end, viewport, nil
}

func (s *Service) resolveActRef(ctx context.Context, runtimeSessionID string, ref string) (browserTarget, error) {
	if ref == "" {
		return browserTarget{}, ErrInvalidRequest
	}
	refState, err := s.state.GetRef(runtimeSessionID, ref)
	if err != nil {
		return browserTarget{}, err
	}
	if refState.Kind != "element" {
		return browserTarget{}, browserrt.ErrInvalidRef
	}
	return queryTargetBySelector(ctx, refState.Selector, false)
}

func queryTargetBySelector(ctx context.Context, selector string, requireUnique bool) (browserTarget, error) {
	var out browserTarget
	script := fmt.Sprintf(`(() => {
      const selector = %q;
      const visible = (el) => {
        const r = el.getBoundingClientRect();
        const style = window.getComputedStyle(el);
        return r.width > 0 && r.height > 0 && style.visibility !== "hidden" && style.display !== "none";
      };
      const matches = Array.from(document.querySelectorAll(selector)).filter(visible);
      if (%t && matches.length !== 1) throw new Error("target ambiguous");
      const el = matches[0] || document.querySelector(selector);
      if (!el) throw new Error("missing");
      el.scrollIntoView({block:"center", inline:"center"});
      const r = el.getBoundingClientRect();
      const tag = (el.tagName || "").toLowerCase();
      return {
        selector,
        rect: { x: r.left, y: r.top, width: r.width, height: r.height },
        editable: !!(el.isContentEditable || tag === "textarea" || (tag === "input" && !["button","checkbox","file","hidden","image","radio","range","reset","submit"].includes((el.type || "").toLowerCase())))
      };
    })()`, selector, requireUnique)
	if err := chromedp.Run(ctx, chromedp.Evaluate(script, &out)); err != nil {
		return browserTarget{}, err
	}
	return out, nil
}

func (s *Service) pointerStart(runtimeSessionID string, viewport viewportRect) pointerPoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.pointers[runtimeSessionID]
	if ok && state.Initialized {
		return clampPoint(state.Point, viewport)
	}
	return pointerPoint{X: viewport.Width * 0.35, Y: viewport.Height * 0.35}
}

func (s *Service) PointerSnapshot(runtimeSessionID string) (VirtualPointerSnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, ok := s.pointers[runtimeSessionID]
	if !ok || !state.Initialized {
		return VirtualPointerSnapshot{Visible: false}, false
	}
	return newVirtualPointerSnapshot(runtimeSessionID, state), true
}

func (s *Service) SubscribePointer(runtimeSessionID string) (PointerSubscription, error) {
	if strings.TrimSpace(runtimeSessionID) == "" {
		return PointerSubscription{}, ErrInvalidRequest
	}
	ch := make(chan VirtualPointerSnapshot, 16)
	s.mu.Lock()
	if s.pointerSubscribers[runtimeSessionID] == nil {
		s.pointerSubscribers[runtimeSessionID] = map[chan VirtualPointerSnapshot]struct{}{}
	}
	s.pointerSubscribers[runtimeSessionID][ch] = struct{}{}
	if state, ok := s.pointers[runtimeSessionID]; ok && state.Initialized {
		ch <- newVirtualPointerSnapshot(runtimeSessionID, state)
	}
	s.mu.Unlock()
	return PointerSubscription{
		C: ch,
		Cancel: func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if _, ok := s.pointerSubscribers[runtimeSessionID][ch]; ok {
				delete(s.pointerSubscribers[runtimeSessionID], ch)
				close(ch)
			}
		},
	}, nil
}

func (s *Service) setPointer(runtimeSessionID string, point pointerPoint, viewport viewportRect) {
	s.setPointerButton(runtimeSessionID, point, viewport, false)
}

func (s *Service) setPointerButton(runtimeSessionID string, point pointerPoint, viewport viewportRect, buttonDown bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := pointerState{
		Point:       point,
		Viewport:    viewport,
		Initialized: true,
		ButtonDown:  buttonDown,
	}
	s.pointers[runtimeSessionID] = state
	snapshot := newVirtualPointerSnapshot(runtimeSessionID, state)
	for ch := range s.pointerSubscribers[runtimeSessionID] {
		select {
		case ch <- snapshot:
		default:
		}
	}
}

func (s *Service) UploadFiles(runtimeSessionID string, input UploadFilesInput) (UploadFilesOutput, error) {
	ref := strings.TrimSpace(input.Ref)
	hasPoint := input.X > 0 && input.Y > 0
	if (ref == "" && !hasPoint) || len(input.Files) == 0 {
		return UploadFilesOutput{}, ErrInvalidRequest
	}
	if s.sessions == nil || s.state == nil {
		return UploadFilesOutput{}, ErrInvalidRequest
	}
	var refState browserrt.RefState
	isFileInput := false
	if ref != "" {
		var err error
		refState, err = s.state.GetRef(runtimeSessionID, ref)
		if err != nil {
			return UploadFilesOutput{}, err
		}
		isFileInput = refState.Kind == "element" && strings.ToLower(strings.TrimSpace(refState.TagName)) == "input"
		if !isFileInput && refState.Kind != "text" && refState.Kind != "element" {
			return UploadFilesOutput{}, browserrt.ErrInvalidRef
		}
	}
	info, err := s.sessions.Get(runtimeSessionID)
	if err != nil {
		return UploadFilesOutput{}, err
	}
	uploadDir := filepath.Join(filepath.Dir(info.ProfileDir), "uploads")
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		return UploadFilesOutput{}, err
	}
	filePaths := make([]string, 0, len(input.Files))
	fileNames := make([]string, 0, len(input.Files))
	for index, source := range input.Files {
		path, name, err := s.materializeUploadSource(context.Background(), uploadDir, index, source)
		if err != nil {
			return UploadFilesOutput{}, err
		}
		filePaths = append(filePaths, path)
		fileNames = append(fileNames, name)
	}
	if ref == "" {
		setter := s.setFilesAtPoint
		if setter == nil {
			setter = s.defaultSetFilesAtPoint
		}
		if err := setter(runtimeSessionID, input.X, input.Y, filePaths, input.TimeoutMs); err != nil {
			return UploadFilesOutput{}, fmt.Errorf("%w: %v", ErrUploadFilesFailed, err)
		}
	} else if isFileInput {
		setter := s.setFileInputFiles
		if setter == nil {
			setter = s.defaultSetFileInputFiles
		}
		if err := setter(runtimeSessionID, refState.Selector, filePaths, input.TimeoutMs); err != nil {
			return UploadFilesOutput{}, fmt.Errorf("%w: %v", ErrUploadFilesFailed, err)
		}
	} else {
		dropper := s.dropFilesOnTarget
		if dropper == nil {
			dropper = s.defaultDropFilesOnTarget
		}
		if err := dropper(runtimeSessionID, refState.Selector, filePaths, input.TimeoutMs); err != nil {
			return UploadFilesOutput{}, fmt.Errorf("%w: %v", ErrUploadFilesFailed, err)
		}
	}
	return UploadFilesOutput{OK: true, Ref: ref, FileNames: fileNames}, nil
}

func (s *Service) materializeUploadSource(ctx context.Context, uploadDir string, index int, source UploadFileSource) (string, string, error) {
	s3Path := strings.TrimSpace(source.S3Path)
	localPath := strings.TrimSpace(source.LocalPath)
	sourceURL := strings.TrimSpace(source.URL)
	sourceCount := 0
	for _, value := range []string{s3Path, localPath, sourceURL} {
		if value != "" {
			sourceCount++
		}
	}
	if sourceCount != 1 {
		return "", "", ErrInvalidRequest
	}
	name := sanitizeUploadFilename(source.Filename)
	if name == "" {
		switch {
		case s3Path != "":
			name = sanitizeUploadFilename(filepath.Base(strings.TrimPrefix(s3Path, "s3://")))
		case localPath != "":
			name = sanitizeUploadFilename(filepath.Base(localPath))
		case sourceURL != "":
			parsed, err := url.Parse(sourceURL)
			if err != nil {
				return "", "", ErrInvalidRequest
			}
			name = sanitizeUploadFilename(filepath.Base(parsed.Path))
		}
	}
	if name == "" || name == "." {
		name = fmt.Sprintf("upload-%d.bin", index+1)
	}
	if s3Path != "" {
		if s.assets == nil {
			return "", "", ErrInvalidRequest
		}
		body, _, err := s.assets.Get(ctx, s3Path)
		if err != nil {
			return "", "", err
		}
		outPath := filepath.Join(uploadDir, name)
		if err := os.WriteFile(outPath, body, 0o600); err != nil {
			return "", "", err
		}
		return outPath, name, nil
	}
	if sourceURL != "" {
		parsed, err := url.Parse(sourceURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return "", "", ErrInvalidRequest
		}
		client := newUploadHTTPClient()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
		if err != nil {
			return "", "", ErrInvalidRequest
		}
		res, err := client.Do(req)
		if err != nil {
			return "", "", fmt.Errorf("%w: GET %s failed: %v", ErrUploadSourceFetchFailed, uploadSourceURLForError(sourceURL), err)
		}
		defer func() {
			_ = res.Body.Close()
		}()
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			body, _ := io.ReadAll(io.LimitReader(res.Body, 512))
			detail := strings.TrimSpace(string(body))
			if detail == "" {
				detail = res.Status
			}
			return "", "", fmt.Errorf("%w: GET %s returned HTTP %d: %s", ErrUploadSourceFetchFailed, uploadSourceURLForError(sourceURL), res.StatusCode, detail)
		}
		outPath := filepath.Join(uploadDir, name)
		out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return "", "", err
		}
		defer func() {
			_ = out.Close()
		}()
		if _, err := io.Copy(out, res.Body); err != nil {
			return "", "", err
		}
		return outPath, name, nil
	}
	cleanLocal, err := filepath.Abs(localPath)
	if err != nil {
		return "", "", ErrInvalidRequest
	}
	allowedRoot, err := filepath.Abs(uploadDir)
	if err != nil {
		return "", "", ErrInvalidRequest
	}
	rel, err := filepath.Rel(allowedRoot, cleanLocal)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." || filepath.IsAbs(rel) {
		return "", "", ErrInvalidRequest
	}
	return cleanLocal, name, nil
}

func uploadSourceURLForError(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return strings.TrimSpace(raw)
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func sanitizeUploadFilename(value string) string {
	name := filepath.Base(strings.TrimSpace(value))
	name = strings.ReplaceAll(name, string(os.PathSeparator), "_")
	name = strings.TrimSpace(name)
	if name == "." || name == string(os.PathSeparator) {
		return ""
	}
	return name
}

func newUploadHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = func(req *http.Request) (*url.URL, error) {
		if shouldBypassUploadURLProxy(req.URL) {
			return nil, nil
		}
		return http.ProxyFromEnvironment(req)
	}
	return &http.Client{Timeout: 2 * time.Minute, Transport: transport}
}

func shouldBypassUploadURLProxy(u *url.URL) bool {
	if u == nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host == "localhost" || host == "host.docker.internal" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Service) defaultSetFileInputFiles(runtimeSessionID string, selector string, filePaths []string, timeoutMs int) error {
	ctx, cancel, err := s.newBrowserContext(runtimeSessionID, timeoutMs)
	if err != nil {
		return err
	}
	defer cancel()

	chooserCh := make(chan *page.EventFileChooserOpened, 1)
	listenCtx, stopListening := context.WithCancel(ctx)
	defer stopListening()
	chromedp.ListenTarget(listenCtx, func(ev any) {
		chooser, ok := ev.(*page.EventFileChooserOpened)
		if !ok {
			return
		}
		select {
		case chooserCh <- chooser:
		default:
		}
	})

	if err := chromedp.Run(ctx, page.SetInterceptFileChooserDialog(true)); err != nil {
		return err
	}
	defer func() {
		_ = chromedp.Run(ctx, page.SetInterceptFileChooserDialog(false))
	}()

	if err := s.trustedClick(ctx, runtimeSessionID, selector, ActInput{TimeoutMs: timeoutMs}); err != nil {
		return fmt.Errorf("open file chooser: %w", err)
	}
	select {
	case chooser := <-chooserCh:
		if chooser.BackendNodeID == 0 {
			return errors.New("file chooser opened without backend node")
		}
		return chromedp.Run(ctx, dom.SetFileInputFiles(filePaths).WithBackendNodeID(chooser.BackendNodeID))
	case <-ctx.Done():
		return fmt.Errorf("file chooser not opened: %w", ctx.Err())
	}
}

func (s *Service) defaultSetFilesAtPoint(runtimeSessionID string, x float64, y float64, filePaths []string, timeoutMs int) error {
	ctx, cancel, err := s.newBrowserContext(runtimeSessionID, timeoutMs)
	if err != nil {
		return err
	}
	defer cancel()

	chooserCh := make(chan *page.EventFileChooserOpened, 1)
	listenCtx, stopListening := context.WithCancel(ctx)
	defer stopListening()
	chromedp.ListenTarget(listenCtx, func(ev any) {
		chooser, ok := ev.(*page.EventFileChooserOpened)
		if !ok {
			return
		}
		select {
		case chooserCh <- chooser:
		default:
		}
	})

	if err := chromedp.Run(ctx, page.SetInterceptFileChooserDialog(true)); err != nil {
		return err
	}
	defer func() {
		_ = chromedp.Run(ctx, page.SetInterceptFileChooserDialog(false))
	}()

	if err := s.trustedClickPoint(ctx, runtimeSessionID, ActInput{X: x, Y: y, TimeoutMs: timeoutMs}); err != nil {
		return fmt.Errorf("open file chooser: %w", err)
	}
	select {
	case chooser := <-chooserCh:
		if chooser.BackendNodeID == 0 {
			return errors.New("file chooser opened without backend node")
		}
		return chromedp.Run(ctx, dom.SetFileInputFiles(filePaths).WithBackendNodeID(chooser.BackendNodeID))
	case <-ctx.Done():
		return fmt.Errorf("file chooser not opened: %w", ctx.Err())
	}
}

func (s *Service) defaultDropFilesOnTarget(runtimeSessionID string, selector string, filePaths []string, timeoutMs int) error {
	ctx, cancel, err := s.newBrowserContext(runtimeSessionID, timeoutMs)
	if err != nil {
		return err
	}
	defer cancel()

	target, err := queryTargetBySelector(ctx, selector, false)
	if err != nil {
		return err
	}
	point, _, err := s.trustedMoveTarget(ctx, runtimeSessionID, target, ActInput{TimeoutMs: timeoutMs})
	if err != nil {
		return err
	}
	data := &cdinput.DragData{
		Items:              []*cdinput.DragDataItem{},
		Files:              filePaths,
		DragOperationsMask: 1,
	}
	return chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		if err := cdinput.DispatchDragEvent(cdinput.DragEnter, point.X, point.Y, data).Do(ctx); err != nil {
			return err
		}
		if err := cdinput.DispatchDragEvent(cdinput.DragOver, point.X, point.Y, data).Do(ctx); err != nil {
			return err
		}
		return cdinput.DispatchDragEvent(cdinput.Drop, point.X, point.Y, data).Do(ctx)
	}))
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

func (s *Service) Evaluate(runtimeSessionID string, input EvaluateInput) (EvaluateOutput, error) {
	if strings.TrimSpace(input.Script) == "" {
		return EvaluateOutput{}, ErrInvalidRequest
	}
	world := strings.TrimSpace(input.World)
	if world != "" && world != "MAIN" {
		return EvaluateOutput{}, fmt.Errorf("%w: browserd evaluate only supports MAIN world; omit world or use MAIN", ErrInvalidRequest)
	}
	argsJSON, err := json.Marshal(input.Args)
	if err != nil {
		return EvaluateOutput{}, fmt.Errorf("%w: args must be JSON serializable", ErrInvalidRequest)
	}
	ctx, cancel, err := s.newBrowserContext(runtimeSessionID, input.TimeoutMs)
	if err != nil {
		return EvaluateOutput{}, err
	}
	defer cancel()

	var out EvaluateOutput
	if err := chromedp.Run(ctx, chromedp.Evaluate(browserEvaluateRuntimeScript(input.Script, string(argsJSON)), &out, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
		return p.WithAwaitPromise(true)
	})); err != nil {
		return EvaluateOutput{}, fmt.Errorf("%w: %v", ErrEvaluateFailed, err)
	}
	return out, nil
}

func browserEvaluateRuntimeScript(script string, argsJSON string) string {
	return fmt.Sprintf(`(async () => {
  const args = %s;
  const raw = await (async () => { %s })();
  const result = raw === undefined ? null : raw;
  const guard = findNonJsonValue(result, "$", new Set());
  if (guard) throw new Error("EVALUATE_RESULT_NOT_JSON: " + guard);
  return {
    result: JSON.parse(JSON.stringify(result)),
    url: document.location.href,
    title: document.title
  };

  function findNonJsonValue(value, path, seen) {
    if (value === null) return null;
    const valueType = typeof value;
    if (valueType === "function" || valueType === "symbol" || valueType === "bigint" || valueType === "undefined") return path + " is " + valueType;
    if (valueType !== "object") return null;
    if (value instanceof Node) return path + " is DOM Node";
    if (seen.has(value)) return path + " is circular";
    seen.add(value);
    if (Array.isArray(value)) {
      for (let i = 0; i < value.length; i += 1) {
        const nested = findNonJsonValue(value[i], path + "[" + i + "]", seen);
        if (nested) return nested;
      }
      return null;
    }
    for (const key of Object.keys(value)) {
      const nested = findNonJsonValue(value[key], path + "." + key, seen);
      if (nested) return nested;
    }
    return null;
  }
})()`, argsJSON, script)
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
	fp := info.Fingerprint
	proxy, err := ParseProxyServer(info.ProxyServer)
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
		screenWidth, screenHeight, err := liveRuntimeDimensionsFromFingerprint(fp)
		if err != nil {
			return nil, err
		}
		liveRuntime, err = NewLiveRuntime(sessionRootFromProfileDir(info.ProfileDir), screenWidth, screenHeight)
		if err != nil {
			return nil, err
		}
		if err := liveRuntime.Start(context.Background()); err != nil {
			return nil, err
		}
		chromeEnv = liveRuntime.ChromeEnv()
	}

	var proxyAdapter *localProxyAdapter
	proxyOverride := ""
	useProxyHop, err := s.proxyHopMode(proxy)
	if err != nil {
		if liveRuntime != nil {
			_ = liveRuntime.Stop(context.Background())
		}
		return nil, err
	}
	if useProxyHop {
		proxyAdapter = newLocalProxyAdapter(localProxyAdapterOptions{
			RuntimeSessionID: runtimeSessionID,
			UpstreamProxy:    proxy,
			WorkerURL:        s.proxyHop.WorkerURL,
			WorkerToken:      s.proxyHop.WorkerToken,
		})
		if err := proxyAdapter.Start(context.Background()); err != nil {
			if liveRuntime != nil {
				_ = liveRuntime.Stop(context.Background())
			}
			return nil, fmt.Errorf("%w: proxy hop adapter start failed: %v", ErrProxyHopFailed, err)
		}
		proxyOverride = proxyAdapter.ChromeProxyServer()
	}

	cmd := exec.Command(chromeBin, buildChromeArgs(BrowserOptions{
		UserDataDir:         info.ProfileDir,
		Headless:            !liveEnabled,
		Fingerprint:         fp,
		Proxy:               proxy,
		ProxyOverrideServer: proxyOverride,
	})...)
	cmd.Env = append(cmd.Environ(), chromeEnv...)
	if err := cmd.Start(); err != nil {
		if liveRuntime != nil {
			_ = liveRuntime.Stop(context.Background())
		}
		if proxyAdapter != nil {
			_ = proxyAdapter.Close()
		}
		return nil, err
	}

	wsURL, err := waitForDevToolsWS(info.ProfileDir, 5*time.Second)
	if err != nil {
		_ = cmd.Process.Kill()
		if liveRuntime != nil {
			_ = liveRuntime.Stop(context.Background())
		}
		if proxyAdapter != nil {
			_ = proxyAdapter.Close()
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
		if proxyAdapter != nil {
			_ = proxyAdapter.Close()
		}
		return nil, err
	}
	if err := applyRuntimeOptions(pageCtx, fp, proxy); err != nil {
		pageCancel()
		allocCancel()
		rootCancel()
		_ = cmd.Process.Kill()
		if liveRuntime != nil {
			_ = liveRuntime.Stop(context.Background())
		}
		if proxyAdapter != nil {
			_ = proxyAdapter.Close()
		}
		return nil, err
	}

	s.mu.Lock()
	ab := &activeBrowser{
		cmd:          cmd,
		live:         liveRuntime,
		wsURL:        wsURL,
		rootCtx:      rootCtx,
		rootCancel:   rootCancel,
		allocCtx:     allocCtx,
		allocCancel:  allocCancel,
		pageCtx:      pageCtx,
		pageCancel:   pageCancel,
		proxyAdapter: proxyAdapter,
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
	UserDataDir         string
	Headless            bool
	Fingerprint         FingerprintConfig
	Proxy               ProxyConfig
	ProxyOverrideServer string
}

func buildChromeArgs(opts BrowserOptions) []string {
	args := []string{
		"--disable-gpu",
		"--no-sandbox",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-dev-shm-usage",
		"--remote-debugging-port=0",
		"--force-color-profile=srgb",
		"--force-webrtc-ip-handling-policy=disable_non_proxied_udp",
	}
	proxyServer := opts.Proxy.ChromeServer
	if opts.ProxyOverrideServer != "" {
		proxyServer = opts.ProxyOverrideServer
	}
	if proxyServer != "" {
		args = append(args, "--proxy-server="+proxyServer)
	}
	if opts.Fingerprint.UserAgent != "" {
		args = append(args, "--user-agent="+opts.Fingerprint.UserAgent)
	}
	if opts.Fingerprint.Locale != "" {
		args = append(args, "--lang="+opts.Fingerprint.Locale)
	}
	if opts.Fingerprint.ScreenWidth > 0 && opts.Fingerprint.ScreenHeight > 0 {
		args = append(args, fmt.Sprintf("--window-size=%d,%d", opts.Fingerprint.ScreenWidth, opts.Fingerprint.ScreenHeight))
	}
	if opts.Headless {
		args = append(args, "--headless=new")
	}
	args = append(args, "--user-data-dir="+opts.UserDataDir, "about:blank")
	return args
}

func (s *Service) proxyHopMode(proxy ProxyConfig) (bool, error) {
	if proxy.Raw == "" {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(s.proxyHop.Mode), "cloudflare-worker") {
		return false, nil
	}
	if strings.TrimSpace(s.proxyHop.WorkerURL) == "" || strings.TrimSpace(s.proxyHop.WorkerToken) == "" {
		return false, ErrProxyHopConfigMissing
	}
	return true, nil
}

func liveModeEnabled() bool {
	value := strings.TrimSpace(os.Getenv("BROWSERD_LIVE_ENABLED"))
	return strings.EqualFold(value, "true") || value == "1"
}

func liveRuntimeDimensionsFromFingerprint(fp FingerprintConfig) (int, int, error) {
	if fp.ScreenWidth <= 0 || fp.ScreenHeight <= 0 {
		return 0, 0, fmt.Errorf("%w: fingerprint screenWidth/screenHeight are required for live runtime, got %dx%d", ErrInvalidRequest, fp.ScreenWidth, fp.ScreenHeight)
	}
	return int(fp.ScreenWidth), int(fp.ScreenHeight), nil
}
