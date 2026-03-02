package router

import (
	"log"
	"net/http"
	"strings"

	"browserd/internal/config"
	"browserd/internal/controller"
	"browserd/internal/profile"
	"browserd/internal/session"
	"browserd/internal/types"
)

func New(cfg config.Config) http.Handler {
	var store profile.Store
	switch strings.ToLower(strings.TrimSpace(cfg.ProfileStore)) {
	case "s3":
		s3Store, err := profile.NewS3Store(profile.S3StoreConfig{
			Endpoint:        cfg.S3Endpoint,
			Region:          cfg.S3Region,
			AccessKeyID:     cfg.S3AccessKeyID,
			SecretAccessKey: cfg.S3SecretAccessKey,
			ForcePathStyle:  cfg.S3ForcePathStyle,
		})
		if err != nil {
			log.Printf("browserd: init s3 store failed, fallback to memory store: %v", err)
			store = profile.NewMemoryStore()
		} else {
			store = s3Store
		}
	default:
		store = profile.NewMemoryStore()
	}
	manager := session.NewManager(session.ManagerOptions{
		Store:      store,
		Workdir:    cfg.Workdir,
		CDPBaseURL: cfg.CDPBaseURL,
	})
	handler := controller.NewSessionController(manager, cfg.CDPBaseURL)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/healthz":
			types.WriteOK(w, http.StatusOK, map[string]any{"ok": true})
			return
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sessions":
			handler.CreateSession(w, r)
			return
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/sessions/") && strings.HasSuffix(r.URL.Path, "/commit"):
			id, ok := controller.ExtractRuntimeSessionID(strings.TrimSuffix(r.URL.Path, "/commit"))
			if !ok {
				types.WriteErr(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid runtimeSessionId")
				return
			}
			handler.CommitSession(w, r, id)
			return
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1/sessions/"):
			id, ok := controller.ExtractRuntimeSessionID(r.URL.Path)
			if !ok {
				types.WriteErr(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid runtimeSessionId")
				return
			}
			handler.DeleteSession(w, r, id)
			return
		default:
			types.WriteErr(w, http.StatusNotFound, "NOT_FOUND", "route not found")
			return
		}
	})
}
