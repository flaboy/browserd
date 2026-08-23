package profile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type FileStoreConfig struct {
	Root string
}

type FileStore struct {
	root string
}

func NewFileStore(cfg FileStoreConfig) (*FileStore, error) {
	root := strings.TrimSpace(cfg.Root)
	if root == "" {
		return nil, fmt.Errorf("profile file root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, err
	}
	return &FileStore{root: filepath.Clean(abs)}, nil
}

func (s *FileStore) Get(_ context.Context, uri string) ([]byte, string, bool, error) {
	target, err := s.resolve(uri)
	if err != nil {
		return nil, "", false, err
	}
	body, err := os.ReadFile(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", false, nil
		}
		return nil, "", false, err
	}
	return body, fileStoreVersion(body), true, nil
}

func (s *FileStore) Put(_ context.Context, uri string, data []byte, ifMatchVersion string) (string, error) {
	if strings.TrimSpace(ifMatchVersion) == "" {
		return "", ErrIfMatchRequired
	}
	target, err := s.resolve(uri)
	if err != nil {
		return "", err
	}

	current, err := os.ReadFile(target)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err == nil {
		if fileStoreVersion(current) != strings.TrimSpace(ifMatchVersion) {
			return "", ErrVersionConflict
		}
	} else if strings.TrimSpace(ifMatchVersion) != "new" {
		return "", ErrVersionConflict
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".profile-*.tmp")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, target); err != nil {
		return "", err
	}
	return fileStoreVersion(data), nil
}

func (s *FileStore) resolve(uri string) (string, error) {
	key, err := parseFileStoreURI(uri)
	if err != nil {
		return "", err
	}
	target := filepath.Join(s.root, filepath.FromSlash(key))
	cleanTarget := filepath.Clean(target)
	rel, err := filepath.Rel(s.root, cleanTarget)
	if err != nil {
		return "", err
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return "", fmt.Errorf("file profile path escapes root")
	}
	return cleanTarget, nil
}

func parseFileStoreURI(uri string) (string, error) {
	trimmed := strings.TrimSpace(uri)
	if trimmed == "" {
		return "", fmt.Errorf("file profile path is required")
	}
	if strings.Contains(trimmed, "://") {
		return "", fmt.Errorf("file profile path must be logical path without scheme")
	}
	if !strings.HasPrefix(trimmed, "/") {
		return "", fmt.Errorf("file profile path must start with /")
	}
	key := strings.Trim(trimmed, "/")
	if key == "" {
		return "", fmt.Errorf("file profile key is required")
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("invalid file profile key segment")
		}
	}
	cleanKey := path.Clean(key)
	if cleanKey == "." || strings.HasPrefix(cleanKey, "../") || cleanKey == ".." || path.IsAbs(cleanKey) {
		return "", fmt.Errorf("invalid file profile key")
	}
	if !strings.HasSuffix(cleanKey, "profile.tgz") {
		return "", fmt.Errorf("file profile key must end with profile.tgz")
	}
	return cleanKey, nil
}

func fileStoreVersion(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
