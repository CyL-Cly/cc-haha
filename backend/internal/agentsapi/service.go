// Package agentsapi owns the desktop agents API and safe Markdown mutations.
// Its loader is the same native configured-agent producer used by execution.
package agentsapi

import (
	"context"
	"errors"
	"path/filepath"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type Options struct {
	HomeDir, ConfigDir, WorkDir string
	Load                        func(context.Context, string) (*agentcatalog.Result, error)
	AvailableTools              func(context.Context, string) ([]string, error)
	Invalidate                  func(context.Context) error
	Locked                      func(context.Context) (bool, error)
	UpdateOverride              func(context.Context, string, storage.Object) error
	Reload                      func(context.Context, string) (protocol.JSONObject, error)
}
type Service struct{ options Options }

func New(options Options) (*Service, error) {
	for _, path := range []string{options.HomeDir, options.ConfigDir, options.WorkDir} {
		if !filepath.IsAbs(path) {
			return nil, errors.New("Agents require explicit absolute home/config/workdir paths")
		}
	}
	if options.Load == nil || options.Invalidate == nil || options.AvailableTools == nil || options.Locked == nil || options.UpdateOverride == nil || options.Reload == nil {
		return nil, errors.New("Agents require the actual shared loader/cache/tools/settings/reload producers")
	}
	return &Service{options}, nil
}
func (s *Service) cwd(cwd string) string {
	if cwd == "" {
		return s.options.WorkDir
	}
	if filepath.IsAbs(cwd) {
		return filepath.Clean(cwd)
	}
	return filepath.Join(s.options.WorkDir, cwd)
}

type MutationResult struct {
	Fields            protocol.JSONObject
	AgentsDir, Target string
}

var mutationLocks sync.Map

func lockTarget(path string) func() {
	lock, _ := mutationLocks.LoadOrStore(filepath.Clean(path), &sync.Mutex{})
	mutex := lock.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}
func (s *Service) invalidate(ctx context.Context) error { return s.options.Invalidate(ctx) }
