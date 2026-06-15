package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
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

	cdinput "github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

var (
	ErrInvalidRequest        = errors.New("invalid request")
	ErrNavigationFailed      = errors.New("navigation failed")
	ErrActionFailed          = errors.New("action failed")
	ErrEvaluateFailed        = errors.New("evaluate failed")
	ErrScreenshotFailed      = errors.New("screenshot failed")
	ErrPlaywrightUnavailable = errors.New("playwright not available")
	ErrProxyHopFailed        = errors.New("proxy hop failed")
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
	Action        string     `json:"action"`
	Ref           string     `json:"ref,omitempty"`
	Target        *ActTarget `json:"target,omitempty"`
	Text          string     `json:"text,omitempty"`
	Key           string     `json:"key,omitempty"`
	Value         string     `json:"value,omitempty"`
	Values        []string   `json:"values,omitempty"`
	Clear         bool       `json:"clear,omitempty"`
	Button        string     `json:"button,omitempty"`
	ClickCount    int        `json:"clickCount,omitempty"`
	MotionProfile string     `json:"motionProfile,omitempty"`
	TimeoutMs     int        `json:"timeoutMs,omitempty"`
}

type ActTarget struct {
	Ref      string    `json:"ref,omitempty"`
	Selector string    `json:"selector,omitempty"`
	Text     string    `json:"text,omitempty"`
	Point    *ActPoint `json:"point,omitempty"`
}

type ActPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
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

	capturePNG func(context.Context) ([]byte, error)
	mu         sync.Mutex
	browsers   map[string]*activeBrowser
	pointers   map[string]pointerState
}

type pointerState struct {
	Point       pointerPoint
	Initialized bool
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
	return &Service{
		sessions:   opts.Sessions,
		state:      state,
		assets:     opts.Assets,
		proxyHop:   opts.ProxyHop,
		capturePNG: capturePagePNG,
		browsers:   map[string]*activeBrowser{},
		pointers:   map[string]pointerState{},
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
	delete(s.pointers, runtimeSessionID)
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
	if b.proxyAdapter != nil {
		_ = b.proxyAdapter.Close()
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
	if input.Action == "type" && input.Ref == "" && input.Target == nil {
		return ActOutput{}, ErrInvalidRequest
	}
	ctx, cancel, err := s.newBrowserContext(runtimeSessionID, input.TimeoutMs)
	if err != nil {
		return ActOutput{}, err
	}
	defer cancel()

	switch input.Action {
	case "click":
		refState, refErr := s.state.GetRef(runtimeSessionID, input.Ref)
		if refErr != nil {
			return ActOutput{}, refErr
		}
		if err := validateActionRef(input.Action, refState); err != nil {
			return ActOutput{}, err
		}
		err = s.trustedClick(ctx, runtimeSessionID, refState.Selector, input)
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
		refState, refErr := s.state.GetRef(runtimeSessionID, input.Ref)
		if refErr != nil {
			return ActOutput{}, refErr
		}
		if err := validateActionRef(input.Action, refState); err != nil {
			return ActOutput{}, err
		}
		selector := refState.Selector
		err = chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf(`(() => {
      const el = document.querySelector(%q);
      if (!el) throw new Error("missing");
      el.dispatchEvent(new MouseEvent("mouseover", { bubbles: true }));
      el.dispatchEvent(new MouseEvent("mouseenter", { bubbles: true }));
      return true;
    })()`, selector), nil))
	case "type":
		err = s.trustedTextInput(ctx, runtimeSessionID, input)
	case "fill":
		refState, refErr := s.state.GetRef(runtimeSessionID, input.Ref)
		if refErr != nil {
			return ActOutput{}, refErr
		}
		if err := validateActionRef(input.Action, refState); err != nil {
			return ActOutput{}, err
		}
		selector := refState.Selector
		err = chromedp.Run(ctx, chromedp.SetValue(selector, input.Value, chromedp.ByQuery))
	case "press":
		refState, refErr := s.state.GetRef(runtimeSessionID, input.Ref)
		if refErr != nil {
			return ActOutput{}, refErr
		}
		if err := validateActionRef(input.Action, refState); err != nil {
			return ActOutput{}, err
		}
		selector := refState.Selector
		err = chromedp.Run(ctx, chromedp.SendKeys(selector, input.Key, chromedp.ByQuery))
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
	if strings.TrimSpace(input.Text) == "" {
		return ErrInvalidRequest
	}
	if input.Ref != "" && input.Target != nil {
		return ErrInvalidRequest
	}
	target, err := s.resolveActTarget(ctx, runtimeSessionID, input.Ref, input.Target)
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
	actions := []chromedp.Action{}
	if input.Clear {
		actions = append(actions,
			chromedp.KeyEvent("a", chromedp.KeyModifiers(cdinput.ModifierCtrl)),
			chromedp.KeyEvent(kb.Backspace),
		)
	}
	actions = append(actions, chromedp.ActionFunc(func(ctx context.Context) error {
		return cdinput.InsertText(input.Text).Do(ctx)
	}))
	return chromedp.Run(ctx, actions...)
}

func (s *Service) trustedClick(ctx context.Context, runtimeSessionID string, selector string, input ActInput) error {
	target, err := queryTargetBySelector(ctx, selector, false)
	if err != nil {
		return err
	}
	return s.trustedClickTarget(ctx, runtimeSessionID, target, input)
}

func (s *Service) trustedClickTarget(ctx context.Context, runtimeSessionID string, target browserTarget, input ActInput) error {
	viewport := viewportRect{Width: 1366, Height: 768}
	_ = chromedp.Run(ctx, chromedp.Evaluate(`(() => ({ width: window.innerWidth || 1366, height: window.innerHeight || 768 }))()`, &viewport))
	start := s.pointerStart(runtimeSessionID, viewport)
	path := planPointerPath(start, target.Rect, viewport, input.MotionProfile)
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
		for _, point := range path {
			if err := cdinput.DispatchMouseEvent(cdinput.MouseMoved, point.X, point.Y).WithButton(cdinput.None).Do(ctx); err != nil {
				return err
			}
		}
		end := path[len(path)-1]
		if err := cdinput.DispatchMouseEvent(cdinput.MousePressed, end.X, end.Y).WithButton(button).WithClickCount(clickCount).Do(ctx); err != nil {
			return err
		}
		return cdinput.DispatchMouseEvent(cdinput.MouseReleased, end.X, end.Y).WithButton(button).WithClickCount(clickCount).Do(ctx)
	})); err != nil {
		return err
	}
	s.setPointer(runtimeSessionID, path[len(path)-1])
	return nil
}

func (s *Service) resolveActTarget(ctx context.Context, runtimeSessionID string, ref string, target *ActTarget) (browserTarget, error) {
	if ref != "" {
		refState, err := s.state.GetRef(runtimeSessionID, ref)
		if err != nil {
			return browserTarget{}, err
		}
		if refState.Kind != "element" {
			return browserTarget{}, browserrt.ErrInvalidRef
		}
		return queryTargetBySelector(ctx, refState.Selector, false)
	}
	if target == nil {
		return browserTarget{}, ErrInvalidRequest
	}
	locators := 0
	if target.Ref != "" {
		locators++
	}
	if target.Selector != "" {
		locators++
	}
	if target.Text != "" {
		locators++
	}
	if target.Point != nil {
		locators++
	}
	if locators != 1 {
		return browserTarget{}, ErrInvalidRequest
	}
	switch {
	case target.Ref != "":
		return s.resolveActTarget(ctx, runtimeSessionID, target.Ref, nil)
	case target.Selector != "":
		return queryTargetBySelector(ctx, target.Selector, true)
	case target.Text != "":
		return queryTargetByText(ctx, target.Text)
	case target.Point != nil:
		rect := targetRect{X: target.Point.X, Y: target.Point.Y, Width: 1, Height: 1}
		return browserTarget{Rect: rect}, nil
	default:
		return browserTarget{}, ErrInvalidRequest
	}
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

func queryTargetByText(ctx context.Context, text string) (browserTarget, error) {
	var out browserTarget
	script := fmt.Sprintf(`(() => {
      const text = %q;
      const visible = (el) => {
        const r = el.getBoundingClientRect();
        const style = window.getComputedStyle(el);
        return r.width > 0 && r.height > 0 && style.visibility !== "hidden" && style.display !== "none";
      };
      const matches = Array.from(document.querySelectorAll("body *")).filter(el => visible(el) && (el.innerText || el.textContent || "").trim() === text);
      if (matches.length !== 1) throw new Error("target ambiguous");
      const el = matches[0];
      el.scrollIntoView({block:"center", inline:"center"});
      const r = el.getBoundingClientRect();
      return { selector: "", rect: { x: r.left, y: r.top, width: r.width, height: r.height }, editable: !!el.isContentEditable };
    })()`, text)
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

func (s *Service) setPointer(runtimeSessionID string, point pointerPoint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pointers[runtimeSessionID] = pointerState{Point: point, Initialized: true}
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
		liveRuntime, err = NewLiveRuntime(sessionRootFromProfileDir(info.ProfileDir))
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
	if opts.Fingerprint.ViewportWidth > 0 && opts.Fingerprint.ViewportHeight > 0 {
		args = append(args, fmt.Sprintf("--window-size=%d,%d", opts.Fingerprint.ViewportWidth, opts.Fingerprint.ViewportHeight))
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
