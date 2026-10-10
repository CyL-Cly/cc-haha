package clibootstrap

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

func (m *MainPrint) runAgent(ctx context.Context, scope *cooperative.Scope, request builtintools.AgentRequest) (*ownerasync.OwnerFuture[builtintools.AgentRunResult], error) {
	if !scope.BelongsTo(m.Owner) || m.prompt == nil || !m.options.Native.NativeProcess {
		return nil, cooperative.ErrRestricted
	}
	future, err := ownerasync.NewOwnerFuture[builtintools.AgentRunResult](m.Owner)
	if err != nil {
		return nil, err
	}
	result, err := m.runAgentOnOwner(ctx, scope, request)
	if err != nil {
		err = future.Reject(scope, err)
	} else {
		err = future.Resolve(scope, result)
	}
	return future, err
}

func (m *MainPrint) runAgentOnOwner(ctx context.Context, scope *cooperative.Scope, request builtintools.AgentRequest) (builtintools.AgentRunResult, error) {
	future, e := m.startNativeForegroundAgent(ctx, scope, request)
	if e != nil {
		return builtintools.AgentRunResult{}, e
	}
	return future.Await(scope)
}

func mainSettingsFilesMkdir(n *NativeBoundary, path string) error {
	return (mainSettingsFiles{n: n}).MkdirAll(path)
}

func nativeAgentID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "a" + hex.EncodeToString(b), nil
}
