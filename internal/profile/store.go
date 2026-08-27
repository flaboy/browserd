package profile

import "context"

type Store interface {
	Get(ctx context.Context, path string) (data []byte, version string, found bool, err error)
	Put(ctx context.Context, path string, data []byte) (newVersion string, err error)
}
