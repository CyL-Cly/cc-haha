package skillcommands

import (
	"context"
	"path/filepath"
	"sync"
)

// LocalLoadFuture retains the actual Local operation installed before native
// discovery starts. It is not recovered from a flattened registry snapshot.
// Existing finite Local callers retain their synchronous/cancellation policy.
type LocalLoadFuture struct {
	service     *Service
	cwd         string
	once        sync.Once
	done        chan struct{}
	definitions []Definition
	failure     error
}

func (s *Service) LocalProducer(cwd string) *LocalLoadFuture {
	if cwd == "" {
		cwd = s.options.CWD
	}
	if !filepath.IsAbs(cwd) {
		cwd = filepath.Join(s.options.CWD, cwd)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.localProducers == nil {
		s.localProducers = map[string]*LocalLoadFuture{}
	}
	if pending := s.localProducers[cwd]; pending != nil {
		return pending
	}
	pending := &LocalLoadFuture{service: s, cwd: cwd, done: make(chan struct{})}
	s.localProducers[cwd] = pending
	return pending
}

func (future *LocalLoadFuture) Ready() ([]Definition, error, bool) {
	select {
	case <-future.done:
		return append([]Definition{}, future.definitions...), future.failure, true
	default:
		return nil, nil, false
	}
}

func (future *LocalLoadFuture) Load(ctx context.Context) ([]Definition, error) {
	start := false
	future.once.Do(func() { start = true })
	if start {
		future.definitions, future.failure = future.service.Local(ctx, future.cwd)
		close(future.done)
	}
	if value, failure, ready := future.Ready(); ready {
		return value, failure
	}
	select {
	case <-future.done:
		return append([]Definition{}, future.definitions...), future.failure
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
