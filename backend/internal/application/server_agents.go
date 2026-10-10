package application

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

type desktopAgentEntry struct {
	owner    *cooperative.Owner
	settings *clibootstrap.NativeProcessSettings
	catalog  *agentcatalog.Service
}

type desktopAgentRuntime struct {
	mu          sync.Mutex
	entries     map[string]*desktopAgentEntry
	closed      bool
	cwd         string
	environment map[string]string
	factory     NativeFactory
	settings    *clibootstrap.NativeProcessSettings
}

func (r *desktopAgentRuntime) entry(ctx context.Context, cwd string) (*desktopAgentEntry, error) {
	if cwd == "" {
		cwd = r.cwd
	}
	if !filepath.IsAbs(cwd) {
		cwd = filepath.Join(r.cwd, cwd)
	}
	cwd = filepath.Clean(cwd)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if entry := r.entries[cwd]; entry != nil {
		return entry, nil
	}
	var native *clibootstrap.NativeBoundary
	var err error
	if r.factory != nil {
		native, err = r.factory(r.environment, cwd, runtime.GOOS)
	} else {
		native, err = clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: r.environment, CWD: cwd, Platform: runtime.GOOS})
	}
	if err != nil {
		return nil, err
	}
	settings, err := r.settings.ScopedSettings(native)
	if err != nil {
		return nil, err
	}
	if _, err = settings.InitialSettings(); err != nil {
		_ = settings.Close(context.Background())
		return nil, err
	}
	owner := cooperative.New()
	catalog, err := settings.AgentCatalog(owner, cwd, true)
	if err != nil {
		owner.Close()
		_ = settings.Close(context.Background())
		return nil, err
	}
	entry := &desktopAgentEntry{owner, settings, catalog}
	if r.entries == nil {
		r.entries = map[string]*desktopAgentEntry{}
	}
	r.entries[cwd] = entry
	return entry, nil
}

func (r *desktopAgentRuntime) load(ctx context.Context, cwd string) (*agentcatalog.Result, error) {
	entry, err := r.entry(ctx, cwd)
	if err != nil {
		return nil, err
	}
	var output *agentcatalog.Result
	receipt, err := entry.owner.Submit(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		loaded, err := entry.catalog.List(scope)
		if err != nil {
			return err
		}
		clone := func(values []agentcatalog.Definition) []agentcatalog.Definition {
			copies := append([]agentcatalog.Definition{}, values...)
			for i := range copies {
				copies[i].Fields = copies[i].Fields.Clone()
				copies[i].Frontmatter = copies[i].Frontmatter.Clone()
			}
			return copies
		}
		output = &agentcatalog.Result{All: clone(loaded.All), Active: clone(loaded.Active), FailedFiles: append([]agentcatalog.Failure{}, loaded.FailedFiles...)}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return output, err
}

func (r *desktopAgentRuntime) locked(ctx context.Context) (bool, error) {
	entry, err := r.entry(ctx, r.cwd)
	if err != nil {
		return false, err
	}
	var locked bool
	receipt, err := entry.owner.Submit(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		var failure error
		locked, failure = entry.catalog.Restricted(scope)
		return failure
	})
	if err == nil {
		err = receipt.Wait()
	}
	return locked, err
}

func (r *desktopAgentRuntime) tools(ctx context.Context, cwd string) ([]string, error) {
	entry, err := r.entry(ctx, cwd)
	if err != nil {
		return nil, err
	}
	return entry.settings.CustomAgentTools(ctx)
}

func (r *desktopAgentRuntime) invalidate(ctx context.Context) error {
	r.mu.Lock()
	entries := make([]*desktopAgentEntry, 0, len(r.entries))
	for _, entry := range r.entries {
		entries = append(entries, entry)
	}
	r.mu.Unlock()
	for _, entry := range entries {
		receipt, err := entry.owner.Submit(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
			entry.settings.InvalidateSettingsCache()
			return entry.catalog.Clear(scope)
		})
		if err == nil {
			err = receipt.Wait()
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *desktopAgentRuntime) close(ctx context.Context) error {
	r.mu.Lock()
	r.closed = true
	entries := r.entries
	r.entries = nil
	r.mu.Unlock()
	var failures []error
	for _, entry := range entries {
		entry.owner.Close()
		failures = append(failures, entry.settings.Close(ctx))
	}
	return errors.Join(failures...)
}
