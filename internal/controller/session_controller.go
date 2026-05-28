package controller

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"browserd/internal/browser"
	"browserd/internal/live"
	"browserd/internal/runtime"
	"browserd/internal/session"
	"browserd/internal/types"
)

type SessionController struct {
	manager       session.Manager
	browser       browserRuntime
	cdpBaseURL    string
	liveBaseURL   string
	noVNCBasePath string
	liveTokenTTL  time.Duration
	handoffGrace  time.Duration
	tokenStore    *live.TokenStore
	handoffsMu    sync.Mutex
	handoffs      map[string]handoffState
}

type browserRuntime interface {
	PrepareSession(runtimeSessionID string) error
	Close(runtimeSessionID string) error
	Navigate(runtimeSessionID string, input browser.NavigateInput) (browser.NavigateOutput, error)
	Snapshot(runtimeSessionID string, input browser.SnapshotInput) (browser.SnapshotOutput, error)
	Act(runtimeSessionID string, input browser.ActInput) (browser.ActOutput, error)
	Screenshot(runtimeSessionID string, input browser.ScreenshotInput) (browser.ScreenshotOutput, error)
}

type browserLiveProxyRuntime interface {
	LiveProxyTarget(runtimeSessionID string) (string, error)
}

type SessionControllerOptions struct {
	Manager       session.Manager
	Browser       browserRuntime
	CDPBaseURL    string
	LiveBaseURL   string
	NoVNCBasePath string
	LiveTokenTTL  time.Duration
	// HandoffDisconnectGrace controls how long a control noVNC disconnect may last
	// before the handoff is automatically completed.
	HandoffDisconnectGrace time.Duration
	TokenStore             *live.TokenStore
}

func NewSessionController(manager session.Manager, browserSvc browserRuntime, cdpBaseURL string) *SessionController {
	return NewSessionControllerWithLive(SessionControllerOptions{
		Manager:    manager,
		Browser:    browserSvc,
		CDPBaseURL: cdpBaseURL,
	})
}

func NewSessionControllerWithLive(opts SessionControllerOptions) *SessionController {
	ttl := opts.LiveTokenTTL
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	handoffGrace := opts.HandoffDisconnectGrace
	if handoffGrace <= 0 {
		handoffGrace = 60 * time.Second
	}
	noVNCBasePath := strings.TrimSpace(opts.NoVNCBasePath)
	if noVNCBasePath == "" {
		noVNCBasePath = "/v"
	}
	if !strings.HasPrefix(noVNCBasePath, "/") {
		noVNCBasePath = "/" + noVNCBasePath
	}
	noVNCBasePath = strings.TrimRight(noVNCBasePath, "/")
	if noVNCBasePath == "" {
		noVNCBasePath = "/v"
	}
	tokenStore := opts.TokenStore
	if tokenStore == nil {
		tokenStore = live.NewTokenStore(live.TokenStoreOptions{})
	}
	return &SessionController{
		manager:       opts.Manager,
		browser:       opts.Browser,
		cdpBaseURL:    opts.CDPBaseURL,
		liveBaseURL:   strings.TrimRight(strings.TrimSpace(opts.LiveBaseURL), "/"),
		noVNCBasePath: noVNCBasePath,
		liveTokenTTL:  ttl,
		handoffGrace:  handoffGrace,
		tokenStore:    tokenStore,
		handoffs:      map[string]handoffState{},
	}
}

type createSessionRequest struct {
	S3ProfilePath   string `json:"s3ProfilePath"`
	ExpectedVersion string `json:"expectedVersion,omitempty"`
	TTLSeconds      int    `json:"ttlSec,omitempty"`
	LeaseID         string `json:"leaseId,omitempty"`
}

type commitSessionRequest struct {
	IfMatchVersion string `json:"ifMatchVersion"`
}

type navigateRequest struct {
	URL                       string `json:"url"`
	WaitUntil                 string `json:"waitUntil,omitempty"`
	TimeoutMs                 int    `json:"timeoutMs,omitempty"`
	AfterLoadScreenshotS3Path string `json:"afterLoadScreenshotS3Path,omitempty"`
}

type actRequest struct {
	Action    string   `json:"action"`
	Ref       string   `json:"ref,omitempty"`
	Text      string   `json:"text,omitempty"`
	Key       string   `json:"key,omitempty"`
	Value     string   `json:"value,omitempty"`
	Values    []string `json:"values,omitempty"`
	TimeoutMs int      `json:"timeoutMs,omitempty"`
}

type screenshotRequest struct {
	Ref      string `json:"ref,omitempty"`
	FullPage bool   `json:"fullPage,omitempty"`
	Format   string `json:"format,omitempty"`
	Quality  int    `json:"quality,omitempty"`
}

type liveViewRequest struct {
	Permission string `json:"permission,omitempty"`
	TTLSeconds int    `json:"ttlSeconds,omitempty"`
}

type liveViewOutput struct {
	HandoffID  string          `json:"handoffId"`
	ViewerURL  string          `json:"viewerUrl"`
	ExpiresAt  time.Time       `json:"expiresAt"`
	Permission live.Permission `json:"permission"`
}

type handoffState struct {
	HandoffID         string
	Token             string
	ActiveConnections int
	DisconnectTimer   *time.Timer
}

func (h *SessionController) CreateSession(w http.ResponseWriter, r *http.Request) {
	var req createSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		types.WriteErr(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid json body")
		return
	}
	out, err := h.manager.Create(session.CreateInput{
		S3ProfilePath:   req.S3ProfilePath,
		ExpectedVersion: req.ExpectedVersion,
		TTLSeconds:      req.TTLSeconds,
		LeaseID:         req.LeaseID,
	})
	if err != nil {
		types.WriteErr(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	if h.browser != nil {
		if err := h.browser.PrepareSession(out.RuntimeSessionID); err != nil {
			_ = h.browser.Close(out.RuntimeSessionID)
			_ = h.manager.Delete(out.RuntimeSessionID)
			types.WriteErr(w, http.StatusServiceUnavailable, "SESSION_INIT_FAILED", err.Error())
			return
		}
	}
	types.WriteOK(w, http.StatusOK, out)
}

func (h *SessionController) CommitSession(w http.ResponseWriter, r *http.Request, runtimeSessionID string) {
	var req commitSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		types.WriteErr(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid json body")
		return
	}
	if h.browser != nil {
		_ = h.browser.Close(runtimeSessionID)
	}
	h.revokeRuntimeSession(runtimeSessionID)
	out, err := h.manager.Commit(runtimeSessionID, session.CommitInput{
		IfMatchVersion: req.IfMatchVersion,
	})
	if err != nil {
		switch err {
		case session.ErrSessionNotFound:
			types.WriteErr(w, http.StatusNotFound, "SESSION_NOT_FOUND", err.Error())
			return
		case session.ErrProfileVersionConflict:
			types.WriteErr(w, http.StatusConflict, "PROFILE_VERSION_CONFLICT", err.Error())
			return
		default:
			types.WriteErr(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
			return
		}
	}
	types.WriteOK(w, http.StatusOK, out)
}

func (h *SessionController) DeleteSession(w http.ResponseWriter, _ *http.Request, runtimeSessionID string) {
	if h.browser != nil {
		_ = h.browser.Close(runtimeSessionID)
	}
	h.revokeRuntimeSession(runtimeSessionID)
	err := h.manager.Delete(runtimeSessionID)
	if err != nil {
		if err == session.ErrSessionNotFound {
			types.WriteErr(w, http.StatusNotFound, "SESSION_NOT_FOUND", err.Error())
			return
		}
		types.WriteErr(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	types.WriteOK(w, http.StatusOK, map[string]any{"deleted": true})
}

func (h *SessionController) Navigate(w http.ResponseWriter, r *http.Request, runtimeSessionID string) {
	if h.browser == nil {
		types.WriteErr(w, http.StatusNotImplemented, "PLAYWRIGHT_NOT_AVAILABLE", "browser runtime not configured")
		return
	}
	if h.hasActiveHandoff(runtimeSessionID) {
		types.WriteErr(w, http.StatusConflict, "HANDOFF_ACTIVE", "browser session is under human handoff")
		return
	}
	var req navigateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		types.WriteErr(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid json body")
		return
	}
	out, err := h.browser.Navigate(runtimeSessionID, browser.NavigateInput{
		URL:                       req.URL,
		WaitUntil:                 req.WaitUntil,
		TimeoutMs:                 req.TimeoutMs,
		AfterLoadScreenshotS3Path: req.AfterLoadScreenshotS3Path,
	})
	if err != nil {
		writeBrowserErr(w, err)
		return
	}
	types.WriteOK(w, http.StatusOK, out)
}

func (h *SessionController) Snapshot(w http.ResponseWriter, r *http.Request, runtimeSessionID string) {
	if h.browser == nil {
		types.WriteErr(w, http.StatusNotImplemented, "PLAYWRIGHT_NOT_AVAILABLE", "browser runtime not configured")
		return
	}
	mode := strings.TrimSpace(r.URL.Query().Get("mode"))
	if mode == "" {
		mode = "refs"
	}
	out, err := h.browser.Snapshot(runtimeSessionID, browser.SnapshotInput{Mode: mode})
	if err != nil {
		writeBrowserErr(w, err)
		return
	}
	types.WriteOK(w, http.StatusOK, out)
}

func (h *SessionController) Act(w http.ResponseWriter, r *http.Request, runtimeSessionID string) {
	if h.browser == nil {
		types.WriteErr(w, http.StatusNotImplemented, "PLAYWRIGHT_NOT_AVAILABLE", "browser runtime not configured")
		return
	}
	if h.hasActiveHandoff(runtimeSessionID) {
		types.WriteErr(w, http.StatusConflict, "HANDOFF_ACTIVE", "browser session is under human handoff")
		return
	}
	var req actRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		types.WriteErr(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid json body")
		return
	}
	out, err := h.browser.Act(runtimeSessionID, browser.ActInput{
		Action:    req.Action,
		Ref:       req.Ref,
		Text:      req.Text,
		Key:       req.Key,
		Value:     req.Value,
		Values:    req.Values,
		TimeoutMs: req.TimeoutMs,
	})
	if err != nil {
		writeBrowserErr(w, err)
		return
	}
	types.WriteOK(w, http.StatusOK, out)
}

func (h *SessionController) Screenshot(w http.ResponseWriter, r *http.Request, runtimeSessionID string) {
	if h.browser == nil {
		types.WriteErr(w, http.StatusNotImplemented, "PLAYWRIGHT_NOT_AVAILABLE", "browser runtime not configured")
		return
	}
	var req screenshotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		types.WriteErr(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid json body")
		return
	}
	out, err := h.browser.Screenshot(runtimeSessionID, browser.ScreenshotInput{
		Ref:      req.Ref,
		FullPage: req.FullPage,
		Format:   req.Format,
		Quality:  req.Quality,
	})
	if err != nil {
		writeBrowserErr(w, err)
		return
	}
	types.WriteOK(w, http.StatusOK, out)
}

func (h *SessionController) LiveView(w http.ResponseWriter, r *http.Request, runtimeSessionID string) {
	req, ok := h.decodeLiveViewRequest(w, r)
	if !ok {
		return
	}
	out, err := h.issueViewer(runtimeSessionID, "", req, live.PermissionView)
	if err != nil {
		h.writeLiveErr(w, err)
		return
	}
	types.WriteOK(w, http.StatusOK, out)
}

func (h *SessionController) StartHandoff(w http.ResponseWriter, r *http.Request, runtimeSessionID string) {
	req, ok := h.decodeLiveViewRequest(w, r)
	if !ok {
		return
	}

	h.handoffsMu.Lock()
	if _, exists := h.handoffs[runtimeSessionID]; exists {
		h.handoffsMu.Unlock()
		types.WriteErr(w, http.StatusConflict, "HANDOFF_ACTIVE", "browser session already has an active handoff")
		return
	}
	h.handoffsMu.Unlock()

	handoffID, err := newOpaqueID("ho")
	if err != nil {
		types.WriteErr(w, http.StatusInternalServerError, "TOKEN_GENERATION_FAILED", err.Error())
		return
	}
	out, err := h.issueViewer(runtimeSessionID, handoffID, req, live.PermissionControl)
	if err != nil {
		h.writeLiveErr(w, err)
		return
	}

	token := tokenFromViewerURL(out.ViewerURL, h.liveBaseURL, h.noVNCBasePath)
	h.handoffsMu.Lock()
	h.handoffs[runtimeSessionID] = handoffState{
		HandoffID: out.HandoffID,
		Token:     token,
	}
	h.handoffsMu.Unlock()

	types.WriteOK(w, http.StatusOK, out)
}

func (h *SessionController) CompleteHandoff(w http.ResponseWriter, _ *http.Request, runtimeSessionID string, handoffID string) {
	if ok := h.completeHandoff(runtimeSessionID, handoffID); !ok {
		types.WriteErr(w, http.StatusNotFound, "HANDOFF_NOT_FOUND", "handoff not found")
		return
	}
	types.WriteOK(w, http.StatusOK, map[string]any{"completed": true})
}

func (h *SessionController) ServeLiveView(w http.ResponseWriter, r *http.Request, token string) {
	state, ok := h.tokenStore.Lookup(token)
	if !ok {
		types.WriteErr(w, http.StatusGone, "LIVE_TOKEN_EXPIRED", "live view token is expired or revoked")
		return
	}
	if liveRuntime, ok := h.browser.(browserLiveProxyRuntime); ok {
		target, err := liveRuntime.LiveProxyTarget(state.RuntimeSessionID)
		if err != nil {
			writeBrowserErr(w, err)
			return
		}
		if strings.TrimSpace(target) == "" {
			types.WriteErr(w, http.StatusServiceUnavailable, "LIVE_RUNTIME_UNHEALTHY", "live proxy target is empty")
			return
		}
		if h.proxyLiveView(w, r, target, token, state) {
			return
		}
		types.WriteErr(w, http.StatusServiceUnavailable, "LIVE_RUNTIME_UNHEALTHY", "live proxy target is invalid")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, "<!doctype html><title>browserd live view</title><body data-runtime-session-id=%q data-permission=%q>browserd live view</body>", html.EscapeString(state.RuntimeSessionID), html.EscapeString(string(state.Permission)))
}

func (h *SessionController) proxyLiveView(w http.ResponseWriter, r *http.Request, target string, token string, state live.TokenState) bool {
	targetURL, err := url.Parse(target)
	if err != nil {
		return false
	}
	tracked := h.shouldTrackHandoffConnection(r, token, state)
	if tracked {
		h.beginHandoffConnection(state.RuntimeSessionID, state.HandoffID)
		defer h.endHandoffConnection(state.RuntimeSessionID, state.HandoffID)
	}
	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.URL.Path = h.liveProxyPath(r.URL.Path, token)
		req.URL.RawPath = ""
		req.Host = targetURL.Host
	}
	proxy.ServeHTTP(w, r)
	return true
}

func (h *SessionController) shouldTrackHandoffConnection(r *http.Request, token string, state live.TokenState) bool {
	if state.Permission != live.PermissionControl {
		return false
	}
	path := strings.TrimRight(h.noVNCBasePath, "/") + "/" + token + "/websockify"
	return r.URL.Path == path
}

func (h *SessionController) beginHandoffConnection(runtimeSessionID string, handoffID string) {
	h.handoffsMu.Lock()
	defer h.handoffsMu.Unlock()

	state, ok := h.handoffs[runtimeSessionID]
	if !ok || state.HandoffID != handoffID {
		return
	}
	if state.DisconnectTimer != nil {
		state.DisconnectTimer.Stop()
		state.DisconnectTimer = nil
	}
	state.ActiveConnections++
	h.handoffs[runtimeSessionID] = state
}

func (h *SessionController) endHandoffConnection(runtimeSessionID string, handoffID string) {
	h.handoffsMu.Lock()
	defer h.handoffsMu.Unlock()

	state, ok := h.handoffs[runtimeSessionID]
	if !ok || state.HandoffID != handoffID {
		return
	}
	if state.ActiveConnections > 0 {
		state.ActiveConnections--
	}
	if state.ActiveConnections == 0 {
		state.DisconnectTimer = time.AfterFunc(h.handoffGrace, func() {
			h.completeHandoff(runtimeSessionID, handoffID)
		})
	}
	h.handoffs[runtimeSessionID] = state
}

func (h *SessionController) liveProxyPath(path string, token string) string {
	prefix := strings.TrimRight(h.noVNCBasePath, "/") + "/" + token
	rest := strings.TrimPrefix(path, prefix)
	if rest == "" || rest == "/" {
		return "/"
	}
	if !strings.HasPrefix(rest, "/") {
		rest = "/" + rest
	}
	return rest
}

func writeBrowserErr(w http.ResponseWriter, err error) {
	switch err {
	case nil:
		return
	}
	switch {
	case errors.Is(err, session.ErrSessionNotFound):
		types.WriteErr(w, http.StatusNotFound, "SESSION_NOT_FOUND", err.Error())
	case errors.Is(err, runtime.ErrSnapshotNotFound):
		types.WriteErr(w, http.StatusBadRequest, "SNAPSHOT_NOT_FOUND", err.Error())
	case errors.Is(err, runtime.ErrStaleRef):
		types.WriteErr(w, http.StatusConflict, "STALE_REF", err.Error())
	case errors.Is(err, runtime.ErrInvalidRef):
		types.WriteErr(w, http.StatusBadRequest, "INVALID_REF", err.Error())
	case errors.Is(err, browser.ErrInvalidRequest):
		types.WriteErr(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
	case errors.Is(err, browser.ErrPlaywrightUnavailable):
		types.WriteErr(w, http.StatusNotImplemented, "PLAYWRIGHT_NOT_AVAILABLE", err.Error())
	case errors.Is(err, browser.ErrLiveRuntimeUnhealthy):
		types.WriteErr(w, http.StatusServiceUnavailable, "LIVE_RUNTIME_UNHEALTHY", err.Error())
	case errors.Is(err, browser.ErrNavigationFailed):
		types.WriteErr(w, http.StatusBadGateway, "NAVIGATION_FAILED", err.Error())
	case errors.Is(err, browser.ErrActionFailed):
		types.WriteErr(w, http.StatusBadGateway, "ACTION_FAILED", err.Error())
	case errors.Is(err, browser.ErrScreenshotFailed):
		types.WriteErr(w, http.StatusBadGateway, "SCREENSHOT_FAILED", err.Error())
	default:
		types.WriteErr(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
	}
}

func (h *SessionController) decodeLiveViewRequest(w http.ResponseWriter, r *http.Request) (liveViewRequest, bool) {
	var req liveViewRequest
	if r.Body == nil {
		return req, true
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		types.WriteErr(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid json body")
		return liveViewRequest{}, false
	}
	return req, true
}

func (h *SessionController) issueViewer(runtimeSessionID string, handoffID string, req liveViewRequest, defaultPermission live.Permission) (liveViewOutput, error) {
	if strings.TrimSpace(h.liveBaseURL) == "" {
		return liveViewOutput{}, errLiveBaseURLMissing
	}
	if _, err := h.manager.Get(runtimeSessionID); err != nil {
		return liveViewOutput{}, err
	}
	permission := defaultPermission
	if strings.TrimSpace(req.Permission) != "" {
		permission = live.Permission(strings.TrimSpace(req.Permission))
	}
	ttl := h.liveTokenTTL
	if req.TTLSeconds > 0 {
		ttl = time.Duration(req.TTLSeconds) * time.Second
	}
	if strings.TrimSpace(handoffID) == "" {
		var err error
		handoffID, err = newOpaqueID("lv")
		if err != nil {
			return liveViewOutput{}, err
		}
	}
	token, state, err := h.tokenStore.Issue(live.IssueRequest{
		RuntimeSessionID: runtimeSessionID,
		HandoffID:        handoffID,
		Permission:       permission,
		TTL:              ttl,
	})
	if err != nil {
		return liveViewOutput{}, err
	}
	return liveViewOutput{
		HandoffID:  handoffID,
		ViewerURL:  h.viewerURL(token),
		ExpiresAt:  state.ExpiresAt,
		Permission: permission,
	}, nil
}

var errLiveBaseURLMissing = errors.New("live base url is not configured")

func (h *SessionController) writeLiveErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrSessionNotFound):
		types.WriteErr(w, http.StatusNotFound, "SESSION_NOT_FOUND", err.Error())
	case errors.Is(err, live.ErrInvalidTokenRequest):
		types.WriteErr(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
	case errors.Is(err, errLiveBaseURLMissing):
		types.WriteErr(w, http.StatusServiceUnavailable, "LIVE_VIEW_NOT_CONFIGURED", err.Error())
	default:
		types.WriteErr(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
	}
}

func (h *SessionController) viewerURL(token string) string {
	base := h.liveBaseURL + h.noVNCBasePath + "/" + token
	path := strings.TrimLeft(h.noVNCBasePath+"/"+token+"/websockify", "/")
	return base + "/vnc.html?autoconnect=true&resize=remote&path=" + url.QueryEscape(path)
}

func (h *SessionController) hasActiveHandoff(runtimeSessionID string) bool {
	h.handoffsMu.Lock()
	defer h.handoffsMu.Unlock()
	_, ok := h.handoffs[runtimeSessionID]
	return ok
}

func (h *SessionController) revokeRuntimeSession(runtimeSessionID string) {
	h.handoffsMu.Lock()
	if state, ok := h.handoffs[runtimeSessionID]; ok {
		if state.DisconnectTimer != nil {
			state.DisconnectTimer.Stop()
		}
		h.tokenStore.Revoke(state.Token)
		delete(h.handoffs, runtimeSessionID)
	}
	h.handoffsMu.Unlock()
	h.tokenStore.RevokeSession(runtimeSessionID)
}

func (h *SessionController) completeHandoff(runtimeSessionID string, handoffID string) bool {
	h.handoffsMu.Lock()
	state, exists := h.handoffs[runtimeSessionID]
	if !exists || state.HandoffID != strings.TrimSpace(handoffID) {
		h.handoffsMu.Unlock()
		return false
	}
	if state.DisconnectTimer != nil {
		state.DisconnectTimer.Stop()
	}
	delete(h.handoffs, runtimeSessionID)
	h.handoffsMu.Unlock()

	h.tokenStore.Revoke(state.Token)
	return true
}

func newOpaqueID(prefix string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func tokenFromViewerURL(viewerURL string, liveBaseURL string, basePath string) string {
	parsed, err := url.Parse(viewerURL)
	if err == nil && parsed.Path != "" {
		prefix := strings.TrimRight(basePath, "/") + "/"
		rest := strings.TrimPrefix(parsed.Path, prefix)
		rest = strings.TrimLeft(rest, "/")
		token, _, _ := strings.Cut(rest, "/")
		return strings.TrimSpace(token)
	}
	token := strings.TrimPrefix(viewerURL, strings.TrimRight(liveBaseURL, "/")+strings.TrimRight(basePath, "/")+"/")
	token, _, _ = strings.Cut(strings.Trim(token, "/"), "/")
	return strings.TrimSpace(token)
}

func ExtractRuntimeSessionID(path string) (string, bool) {
	// expected:
	// /v1/sessions/{runtimeSessionId}/commit
	// /v1/sessions/{runtimeSessionId}
	if !strings.HasPrefix(path, "/v1/sessions/") {
		return "", false
	}
	rest := strings.TrimPrefix(path, "/v1/sessions/")
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return "", false
	}
	parts := strings.Split(rest, "/")
	return parts[0], true
}
