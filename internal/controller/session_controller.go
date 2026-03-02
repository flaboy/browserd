package controller

import (
	"encoding/json"
	"net/http"
	"strings"

	"browserd/internal/session"
	"browserd/internal/types"
)

type SessionController struct {
	manager    session.Manager
	cdpBaseURL string
}

func NewSessionController(manager session.Manager, cdpBaseURL string) *SessionController {
	return &SessionController{manager: manager, cdpBaseURL: cdpBaseURL}
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
	types.WriteOK(w, http.StatusOK, out)
}

func (h *SessionController) CommitSession(w http.ResponseWriter, r *http.Request, runtimeSessionID string) {
	var req commitSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		types.WriteErr(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid json body")
		return
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
