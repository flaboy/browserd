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
	URL       string
	WaitUntil string
	TimeoutMs int
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

	mu       sync.Mutex
	browsers map[string]*activeBrowser
}

type activeBrowser struct {
	cmd         *exec.Cmd
	wsURL       string
	rootCtx     context.Context
	rootCancel  context.CancelFunc
	allocCtx    context.Context
	allocCancel context.CancelFunc
	pageCtx     context.Context
	pageCancel  context.CancelFunc
}

type snapshotRow struct {
	Selector    string `json:"selector"`
	Group       string `json:"group"`
	Role        string `json:"role"`
	Name        string `json:"name"`
	Text        string `json:"text"`
	TagName     string `json:"tagName"`
	Href        string `json:"href"`
	Value       string `json:"value"`
	Placeholder string `json:"placeholder"`
	TextLength  int    `json:"textLength"`
}

func NewService(sessions session.Manager, state *browserrt.State) *Service {
	if state == nil {
		state = browserrt.NewState()
	}
	return &Service{
		sessions: sessions,
		state:    state,
		browsers: map[string]*activeBrowser{},
	}
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

	s.state.ClearSnapshot(runtimeSessionID)
	return NavigateOutput{
		URL:             url,
		Title:           title,
		SnapshotCleared: true,
	}, nil
}

func (s *Service) Snapshot(runtimeSessionID string, input SnapshotInput) (SnapshotOutput, error) {
	ctx, cancel, err := s.newBrowserContext(runtimeSessionID, 20_000)
	if err != nil {
		return SnapshotOutput{}, err
	}
	defer cancel()

	var rows []snapshotRow
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(snapshotScript(), &rows),
	); err != nil {
		return SnapshotOutput{}, fmt.Errorf("%w: %v", ErrActionFailed, err)
	}

	var url string
	var title string
	_ = chromedp.Run(ctx, chromedp.Location(&url), chromedp.Title(&title))

	snapshotID := fmt.Sprintf("snap_%d", time.Now().UnixNano())
	refs := make(map[string]browserrt.RefState, len(rows))
	page, refs := buildPageSnapshot(rows, url, title)
	for ref, state := range refs {
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

func buildPageSnapshot(rows []snapshotRow, url string, title string) (PageSnapshot, map[string]browserrt.RefState) {
	groups := map[string]PageTable{}
	refs := map[string]browserrt.RefState{}
	var elementIndex int
	var textIndex int

	addRow := func(group string, columns []string, values []any) {
		table := groups[group]
		if len(table.Columns) == 0 {
			table.Columns = columns
		}
		table.Rows = append(table.Rows, values)
		groups[group] = table
	}

	for _, row := range rows {
		group := strings.TrimSpace(row.Group)
		if group == "" {
			group = inferGroup(row)
		}
		kind := "element"
		var ref string
		if group == "texts" {
			textIndex++
			ref = fmt.Sprintf("t%d", textIndex)
			kind = "text"
		} else {
			elementIndex++
			ref = fmt.Sprintf("e%d", elementIndex)
		}
		refs[ref] = browserrt.RefState{
			Ref:      ref,
			Kind:     kind,
			Role:     row.Role,
			Name:     row.Name,
			TagName:  row.TagName,
			Text:     row.Text,
			Selector: row.Selector,
		}
		switch group {
		case "links":
			addRow(group, []string{"ref", "tag", "text", "href"}, []any{ref, strings.ToUpper(row.TagName), row.Text, row.Href})
		case "buttons":
			addRow(group, []string{"ref", "tag", "text"}, []any{ref, strings.ToUpper(row.TagName), row.Text})
		case "inputs", "textareas":
			addRow(group, []string{"ref", "tag", "value", "placeholder"}, []any{ref, strings.ToUpper(row.TagName), row.Value, row.Placeholder})
		case "selects":
			addRow(group, []string{"ref", "tag", "value"}, []any{ref, strings.ToUpper(row.TagName), row.Value})
		case "areas":
			addRow(group, []string{"ref", "tag", "text", "href"}, []any{ref, strings.ToUpper(row.TagName), row.Text, row.Href})
		case "customs":
			addRow(group, []string{"ref", "tag", "role", "text"}, []any{ref, strings.ToUpper(row.TagName), row.Role, row.Text})
		case "texts":
			addRow(group, []string{"ref", "tag", "text", "textLength"}, []any{ref, strings.ToUpper(row.TagName), row.Text, row.TextLength})
		}
	}

	return PageSnapshot{
		URL:    url,
		Title:  title,
		Groups: groups,
	}, refs
}

func inferGroup(row snapshotRow) string {
	switch strings.ToLower(strings.TrimSpace(row.Group)) {
	case "links", "buttons", "inputs", "textareas", "selects", "areas", "customs", "texts":
		return strings.ToLower(strings.TrimSpace(row.Group))
	}
	switch strings.ToLower(strings.TrimSpace(row.TagName)) {
	case "a":
		return "links"
	case "button":
		return "buttons"
	case "input":
		return "inputs"
	case "textarea":
		return "textareas"
	case "select":
		return "selects"
	case "area":
		return "areas"
	}
	if strings.TrimSpace(row.Role) != "" {
		return "customs"
	}
	return "texts"
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

	cmd := exec.Command(chromeBin, buildChromeArgs(info.ProfileDir)...)
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	wsURL, err := waitForDevToolsWS(info.ProfileDir, 5*time.Second)
	if err != nil {
		_ = cmd.Process.Kill()
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
		return nil, err
	}

	s.mu.Lock()
	ab := &activeBrowser{
		cmd:         cmd,
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

func snapshotScript() string {
	return `(() => {
  const normalize = (value, max = 200) => String(value || '').replace(/\s+/g, ' ').trim().slice(0, max);
  const isVisible = (el) => {
    if (!(el instanceof Element)) return false;
    const rect = el.getBoundingClientRect();
    const style = window.getComputedStyle(el);
    return (
      style.display !== 'none' &&
      style.visibility !== 'hidden' &&
      style.opacity !== '0' &&
      rect.width > 0 &&
      rect.height > 0
    );
  };
  const isEnabled = (el) => !el.hasAttribute('disabled') && el.getAttribute('aria-disabled') !== 'true';
  const cssPath = (el) => {
    if (!(el instanceof Element)) return '';
    const parts = [];
    while (el && el.nodeType === Node.ELEMENT_NODE && parts.length < 8) {
      let selector = el.tagName.toLowerCase();
      if (el.id) {
        selector += '#' + CSS.escape(el.id);
        parts.unshift(selector);
        break;
      }
      let nth = 1;
      let sib = el;
      while ((sib = sib.previousElementSibling)) {
        if (sib.tagName === el.tagName) nth++;
      }
      selector += ':nth-of-type(' + nth + ')';
      parts.unshift(selector);
      el = el.parentElement;
    }
    return parts.join(' > ');
  };
  const roleOf = (el) => normalize(el.getAttribute('role') || el.tagName.toLowerCase(), 80);
  const textOf = (el) => normalize(el.innerText || el.textContent || '', 200);
  const nameOf = (el) => normalize(
    el.getAttribute('aria-label') ||
    el.getAttribute('placeholder') ||
    el.getAttribute('title') ||
    el.innerText ||
    el.value ||
    '',
    120
  );
  const groupOf = (el) => {
    const tag = el.tagName.toLowerCase();
    if (tag === 'a') return 'links';
    if (tag === 'button') return 'buttons';
    if (tag === 'input') return 'inputs';
    if (tag === 'textarea') return 'textareas';
    if (tag === 'select') return 'selects';
    if (tag === 'area') return 'areas';
    const role = normalize(el.getAttribute('role'), 40);
    if (role) return 'customs';
    return 'customs';
  };

  const actionableSeen = new Set();
  const out = [];
  const actionableNodes = Array.from(document.querySelectorAll('a,button,input,textarea,select,area,summary,[role],[tabindex]'));
  for (const el of actionableNodes) {
    if (!isVisible(el)) continue;
    if (!isEnabled(el)) continue;
    const selector = cssPath(el);
    if (!selector || actionableSeen.has(selector)) continue;
    actionableSeen.add(selector);
    out.push({
      selector,
      group: groupOf(el),
      role: roleOf(el),
      name: nameOf(el),
      text: textOf(el),
      tagName: el.tagName.toLowerCase(),
      href: normalize(el.getAttribute('href') || '', 200),
      value: normalize(el.value || '', 200),
      placeholder: normalize(el.getAttribute('placeholder') || '', 120),
      textLength: textOf(el).length
    });
  }

  const textCandidates = Array.from(document.querySelectorAll('p,h1,h2,h3,h4,h5,h6,article,section,div,span,li,blockquote,pre,code'));
  const textRows = [];
  for (const el of textCandidates) {
    if (!isVisible(el)) continue;
    const selector = cssPath(el);
    if (!selector) continue;
    const text = textOf(el);
    if (text.length < 6) continue;
    if (el.querySelector('a,button,input,textarea,select,area,[role],[tabindex]')) continue;
    textRows.push({
      selector,
      group: 'texts',
      role: '',
      name: '',
      text,
      tagName: el.tagName.toLowerCase(),
      href: '',
      value: '',
      placeholder: '',
      textLength: text.length
    });
  }

  textRows.sort((a, b) => a.selector.length - b.selector.length);
  const dedupedTexts = [];
  for (const row of textRows) {
    let nested = false;
    for (const kept of dedupedTexts) {
      if (!row.selector.startsWith(kept.selector)) continue;
      try {
        const parentEl = document.querySelector(kept.selector);
        const childEl = document.querySelector(row.selector);
        if (parentEl && childEl && parentEl !== childEl && parentEl.contains(childEl)) {
          nested = true;
          break;
        }
      } catch (err) {
      }
    }
    if (!nested) dedupedTexts.push(row);
  }

  return out.concat(dedupedTexts);
})()`
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

func buildChromeArgs(profileDir string) []string {
	return []string{
		"--headless=new",
		"--disable-gpu",
		"--no-sandbox",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-dev-shm-usage",
		"--remote-debugging-port=0",
		"--user-data-dir=" + profileDir,
		"about:blank",
	}
}
