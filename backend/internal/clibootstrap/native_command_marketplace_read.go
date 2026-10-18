package clibootstrap

import (
	"context"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/plugincli"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainCommand) ownsNativeCommandFactory() bool {
	if m == nil || m.nativeCommandInit == nil || m.Native == nil || !m.Native.NativeProcess || m.Owner == nil || m.Settings == nil || m.Settings.environment == nil {
		return false
	}
	bound := m.nativeCommandInit
	return bound.owner == m.Owner && bound.native == m.Native && bound.settings == m.Settings && bound.environment == m.Settings.environment
}

// loadKnownMarketplacesConfig reads only bytes outside the original command
// Owner. Its FsOperations async-return adoption, parsing/catch and handler await
// are distinct promises on that Owner, not business inside a physical worker.
func (m *MainCommand) nativeMarketplaceListOnOwner(ctx context.Context, scope *cooperative.Scope, command plugincli.Command) (plugincli.Result, error) {
	if !m.ownsNativeCommandFactory() || !scope.BelongsTo(m.Owner) || command.Action != "marketplace-list" {
		return plugincli.Result{}, cooperative.ErrRestricted
	}
	if err := ctx.Err(); err != nil {
		return plugincli.Result{}, err
	}
	m.Settings.mu.Lock()
	err := m.Settings.ensureSettings()
	path := ""
	if err == nil {
		path = filepath.Join(m.Settings.plugins.directories.Primary, "known_marketplaces.json")
	}
	m.Settings.mu.Unlock()
	if err != nil {
		return plugincli.Result{}, err
	}
	binding, err := m.nativeInitRefs.Bind(m.Owner)
	if err != nil {
		return plugincli.Result{}, err
	}
	read, err := nativeOwnedWork(m.Owner, binding, scope, func() ([]byte, error) { return m.Native.readFile(path) })
	if err != nil {
		return plugincli.Result{}, err
	}
	fsReturn, err := ownerasync.NewOwnerFuture[[]byte](m.Owner)
	if err != nil {
		return plugincli.Result{}, err
	}
	if err := fsReturn.ResolveFrom(scope, read); err != nil {
		return plugincli.Result{}, err
	}
	loaded, err := ownerasync.NewOwnerFuture[protocol.JSONObject](m.Owner)
	if err != nil {
		return plugincli.Result{}, err
	}
	_, err = fsReturn.Then(scope, func(next *cooperative.Scope, data []byte, readFailure error) error {
		m.nativeAPIPreconnect.life.mu.Lock()
		closed := m.nativeAPIPreconnect.life.closed
		m.nativeAPIPreconnect.life.mu.Unlock()
		if closed {
			return loaded.Reject(next, cooperative.ErrClosed)
		}
		known, failure := pluginsapi.ParseKnownMarketplaces(data, readFailure)
		if failure != nil {
			return loaded.Reject(next, failure)
		}
		return loaded.Resolve(next, known)
	})
	if err != nil {
		return plugincli.Result{}, err
	}
	known, failure, administrative := loaded.AwaitClassified(scope)
	if administrative {
		return plugincli.Result{}, failure
	}
	if failure != nil {
		if m.commandErrors != nil {
			m.commandErrors.Log(failure)
		}
		return plugincli.Result{Stderr: "✘ Failed to list marketplaces: " + failure.Error() + "\n", ExitCode: 1}, nil
	}
	return plugincli.FormatMarketplaceList(known, command.JSON), nil
}
