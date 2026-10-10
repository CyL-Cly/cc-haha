package httpapi

import (
	"context"
	"errors"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/h5access"
)

// H5GraceCache is the synchronous WebSocket cleanup value. Bootstrap must
// refresh this same instance at startup and pass it to the close handler.
// Construction preserves the source's default and does not wait for disk.
type H5GraceCache struct {
	service *h5access.Service
	mu      sync.RWMutex
	value   int
}

func NewH5GraceCache(service *h5access.Service) (*H5GraceCache, error) {
	if service == nil {
		return nil, errors.New("H5 grace cache requires the actual H5 service")
	}
	return &H5GraceCache{service: service, value: h5access.DefaultDisconnectGraceMS}, nil
}

func (cache *H5GraceCache) Milliseconds() int {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	return cache.value
}

// Refresh is best-effort, including after a successful settings commit. A
// failed read keeps the previous value; it must not turn that PUT into a 500.
func (cache *H5GraceCache) Refresh(ctx context.Context) int {
	if value, err := cache.service.GetDisconnectGraceMS(ctx); err == nil {
		cache.mu.Lock()
		cache.value = value
		cache.mu.Unlock()
	}
	return cache.Milliseconds()
}
