package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"browserd/internal/browser"
	"browserd/internal/runtime"
	"browserd/internal/session"
	"browserd/internal/types"
)

type SessionController struct {
	manager    session.Manager
	browser    browserRuntime
	cdpBaseURL string
}

type browserRuntime interface {
	PrepareSession(runtimeSessionID string) error
	Close(runtimeSessionID string) error
	Navigate(runtimeSessionID string, input browser.NavigateInput) (browser.NavigateOutput, error)
	Snapshot(runtimeSessionID string, input browser.SnapshotInput) (browser.SnapshotOutput, error)
	Act(runtimeSessionID string, input browser.ActInput) (browser.ActOutput, error)
	Screenshot(runtimeSessionID string, input browser.ScreenshotInput) (browser.ScreenshotOutput, error)
}

func NewSessionController(manager session.Manager, browserSvc browserRuntime, cdpBaseURL string) *SessionController {
	return &SessionController{manager: manager, browser: browserSvc, cdpBaseURL: cdpBaseURL}
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
	URL       string `json:"url"`
	WaitUntil string `json:"waitUntil,omitempty"`
	TimeoutMs int    `json:"timeoutMs,omitempty"`
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
	var req navigateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		types.WriteErr(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid json body")
		return
	}
	out, err := h.browser.Navigate(runtimeSessionID, browser.NavigateInput{
		URL:       req.URL,
		WaitUntil: req.WaitUntil,
		TimeoutMs: req.TimeoutMs,
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
