package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port              int
	CDPBaseURL        string
	Workdir           string
	ProfileStore      string
	S3Endpoint        string
	S3Region          string
	S3AccessKeyID     string
	S3SecretAccessKey string
	S3ForcePathStyle  bool
}

func Load() Config {
	port := 7011
	if p := strings.TrimSpace(os.Getenv("BROWSERD_PORT")); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			port = v
		}
	}
	cdpBase := strings.TrimSpace(os.Getenv("BROWSERD_CDP_BASE_URL"))
	if cdpBase == "" {
		cdpBase = "ws://browserd:9222/devtools/browser"
	}
	workdir := strings.TrimSpace(os.Getenv("BROWSERD_WORKDIR"))
	if workdir == "" {
		workdir = "/tmp/browserd"
	}
	profileStore := strings.ToLower(strings.TrimSpace(os.Getenv("BROWSERD_PROFILE_STORE")))
	if profileStore == "" {
		profileStore = "memory"
	}
	forcePathStyle := strings.EqualFold(strings.TrimSpace(os.Getenv("BROWSERD_S3_FORCE_PATH_STYLE")), "true") ||
		strings.TrimSpace(os.Getenv("BROWSERD_S3_FORCE_PATH_STYLE")) == "1"
	return Config{
		Port:              port,
		CDPBaseURL:        strings.TrimRight(cdpBase, "/"),
		Workdir:           workdir,
		ProfileStore:      profileStore,
		S3Endpoint:        strings.TrimSpace(os.Getenv("BROWSERD_S3_ENDPOINT")),
		S3Region:          strings.TrimSpace(os.Getenv("BROWSERD_S3_REGION")),
		S3AccessKeyID:     strings.TrimSpace(os.Getenv("BROWSERD_S3_ACCESS_KEY_ID")),
		S3SecretAccessKey: strings.TrimSpace(os.Getenv("BROWSERD_S3_SECRET_ACCESS_KEY")),
		S3ForcePathStyle:  forcePathStyle,
	}
}
