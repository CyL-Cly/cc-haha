package skillcommands

import (
	"context"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
)

// PluginDefinitionLoadFuture is the original per-kind getPluginCommands /
// getPluginSkills operation. It remains valid across explicit cache clear.
type PluginDefinitionLoadFuture struct {
	service     *Service
	skills      bool
	once        sync.Once
	done        chan struct{}
	definitions []Definition
}

type pluginDefinitionMemo = PluginDefinitionLoadFuture

func (s *Service) PluginDefinitionProducer(skills bool) *PluginDefinitionLoadFuture {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pluginMemos == nil {
		s.pluginMemos = map[bool]*pluginDefinitionMemo{}
	}
	if pending := s.pluginMemos[skills]; pending != nil {
		return pending
	}
	pending := &PluginDefinitionLoadFuture{service: s, skills: skills, done: make(chan struct{})}
	s.pluginMemos[skills] = pending
	return pending
}

func (future *PluginDefinitionLoadFuture) Ready() ([]Definition, bool) {
	select {
	case <-future.done:
		return append([]Definition{}, future.definitions...), true
	default:
		return nil, false
	}
}

func (future *PluginDefinitionLoadFuture) Load(ctx context.Context, enabled []pluginregistry.Loaded) []Definition {
	start := false
	future.once.Do(func() { start = true })
	if start {
		if future.skills {
			future.definitions = future.service.PluginSkills(ctx, enabled)
		} else {
			future.definitions = future.service.PluginCommands(ctx, enabled)
		}
		close(future.done)
	}
	if definitions, ready := future.Ready(); ready {
		return definitions
	}
	select {
	case <-future.done:
		return append([]Definition{}, future.definitions...)
	case <-ctx.Done():
		return nil
	}
}

func (s *Service) MemoizedPluginCommands(ctx context.Context, enabled []pluginregistry.Loaded) []Definition {
	return s.PluginDefinitionProducer(false).Load(ctx, enabled)
}
func (s *Service) MemoizedPluginSkills(ctx context.Context, enabled []pluginregistry.Loaded) []Definition {
	return s.PluginDefinitionProducer(true).Load(ctx, enabled)
}
