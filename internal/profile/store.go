package profile

import (
	"context"
	"errors"
)

var (
	ErrVersionConflict = errors.New("profile version conflict")
	ErrIfMatchRequired = errors.New("ifMatchVersion is required")
)

type Store interface {
	Get(ctx context.Context, path string) (data []byte, version string, found bool, err error)
	Put(ctx context.Context, path string, data []byte, ifMatchVersion string) (newVersion string, err error)
}
