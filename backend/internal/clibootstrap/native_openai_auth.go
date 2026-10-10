package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type NativeOpenAITokenRefresh func(context.Context, *cooperative.Scope, json.RawMessage) (protocol.JSONObject, error)
type NativeOpenAIAuthAdministrativeError struct{ Cause error }

func (e *NativeOpenAIAuthAdministrativeError) Error() string { return e.Cause.Error() }
func (e *NativeOpenAIAuthAdministrativeError) Unwrap() error { return e.Cause }
func nativeOpenAIAuthOwnerFailure(err error) error {
	if errors.Is(err, cooperative.ErrRestricted) || errors.Is(err, cooperative.ErrClosed) || errors.Is(err, cooperative.ErrStop) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &NativeOpenAIAuthAdministrativeError{err}
	}
	return err
}

// NativeOpenAIAuth holds one CLI storage/cache producer. Each method receives
// the current owner scope; no scope from an earlier request is retained.
type NativeOpenAIAuth struct {
	main                *MainPrint
	service             *openaiauth.Service
	claudeTokens        *openaiauth.Value
	claudeTokensLoaded  bool
	legacyAPIKeyLoaded  bool
	legacyAPIKey        string
	legacyAPIKeyPresent bool
}
type nativeOpenAIAuthScopeKey struct{}

type nativeOpenAIAuthFiles struct{ n *NativeBoundary }

func (f nativeOpenAIAuthFiles) ReadFile(path string) ([]byte, error) { return f.n.readFile(path) }
func (f nativeOpenAIAuthFiles) MkdirAll(path string, mode fs.FileMode) error {
	if err := f.n.check("openai-auth-mkdir", path); err != nil {
		return err
	}
	return os.MkdirAll(path, mode)
}
func (f nativeOpenAIAuthFiles) WriteFile(path string, data []byte, mode fs.FileMode) error {
	if err := f.n.check("openai-auth-write", path); err != nil {
		return err
	}
	return os.WriteFile(path, data, mode)
}
func (f nativeOpenAIAuthFiles) Rename(from, to string) error {
	if err := f.n.check("openai-auth-rename-from", from); err != nil {
		return err
	}
	if err := f.n.check("openai-auth-rename-to", to); err != nil {
		return err
	}
	return os.Rename(from, to)
}
func (f nativeOpenAIAuthFiles) Remove(path string) error {
	if err := f.n.check("openai-auth-remove", path); err != nil {
		return err
	}
	return os.Remove(path)
}
func (f nativeOpenAIAuthFiles) Exists(path string) bool { _, err := f.n.stat(path); return err == nil }

func (m *MainPrint) NewNativeOpenAIAuth(refresh NativeOpenAITokenRefresh) (*NativeOpenAIAuth, error) {
	if m == nil || m.Owner == nil || m.options.Native == nil || m.environment == nil || m.secure == nil {
		return nil, cooperative.ErrRestricted
	}
	binding := &NativeOpenAIAuth{main: m}
	scopeFor := func(ctx context.Context) (*cooperative.Scope, error) {
		scope, _ := ctx.Value(nativeOpenAIAuthScopeKey{}).(*cooperative.Scope)
		if !scope.BelongsTo(m.Owner) {
			return nil, cooperative.ErrRestricted
		}
		return scope, nil
	}
	options := openaiauth.Options{Home: m.options.Native.Home, CWD: m.options.Native.CWD, Files: nativeOpenAIAuthFiles{m.options.Native}, PID: m.options.PID, NowMillis: func() int64 { return int64(m.options.Native.NowMillis()) }, Environment: func() map[string]string {
		env := map[string]string{}
		for _, field := range m.environment.Snapshot().OwnProperties() {
			var name, value string
			_ = json.Unmarshal(field.NameJSON, &name)
			_ = json.Unmarshal(field.Value, &value)
			env[name] = value
		}
		return env
	}, LogError: func(err error) { _, _ = fmt.Fprintln(m.options.Error, err) }, IsAdministrativeError: func(err error) bool {
		var ownerError *NativeOpenAIAuthAdministrativeError
		return errors.As(err, &ownerError) || errors.Is(err, cooperative.ErrRestricted) || errors.Is(err, cooperative.ErrClosed) || errors.Is(err, cooperative.ErrStop)
	}}
	if m.state != nil {
		options.WorkDir = func() string { return m.state.Snapshot().CWD }
	}
	options.SecureRead = func(ctx context.Context) (json.RawMessage, error) {
		if _, err := scopeFor(ctx); err != nil {
			return nil, err
		}
		return m.secure.ReadSourceJSON(ctx)
	}
	options.SecureReadAsync = func(ctx context.Context) (json.RawMessage, error) {
		scope, err := scopeFor(ctx)
		if err != nil {
			return nil, err
		}
		if m.Controller == nil && !m.commandBinding.Owns(m.Owner) {
			return nil, cooperative.ErrRestricted
		}
		future, err := nativeSkillWork(m, scope, func() ([]byte, error) { return m.secure.ReadSourceJSONAsync(ctx) })
		if err != nil {
			return nil, err
		}
		raw, err := future.Await(scope)
		return raw, nativeOpenAIAuthOwnerFailure(err)
	}
	options.SecureUpdate = func(ctx context.Context, raw json.RawMessage) (openaiauth.Result, error) {
		if _, err := scopeFor(ctx); err != nil {
			return openaiauth.Result{}, err
		}
		result := m.secure.UpdateSourceJSON(ctx, raw)
		out := openaiauth.Result{Success: result.Success}
		if result.Warning != "" {
			warning := result.Warning
			out.Warning = &warning
		}
		return out, nil
	}
	options.AwaitRead = func(ctx context.Context, work func(context.Context) ([]byte, error)) ([]byte, error) {
		scope, err := scopeFor(ctx)
		if err != nil {
			return nil, err
		}
		if m.Controller == nil && !m.commandBinding.Owns(m.Owner) {
			return nil, cooperative.ErrRestricted
		}
		future, err := nativeSkillWork(m, scope, func() ([]byte, error) { return work(ctx) })
		if err != nil {
			return nil, err
		}
		raw, err := future.Await(scope)
		return raw, nativeOpenAIAuthOwnerFailure(err)
	}
	if refresh != nil {
		options.Refresh = func(ctx context.Context, raw json.RawMessage) (protocol.JSONObject, error) {
			scope, err := scopeFor(ctx)
			if err != nil {
				return protocol.JSONObject{}, err
			}
			value, err := refresh(ctx, scope, raw)
			if err != nil && ctx.Err() != nil {
				return protocol.JSONObject{}, &NativeOpenAIAuthAdministrativeError{context.Cause(ctx)}
			}
			return value, err
		}
	}
	service, err := openaiauth.New(options)
	if err != nil {
		return nil, err
	}
	binding.service = service
	return binding, nil
}
func (b *NativeOpenAIAuth) owned(ctx context.Context, scope *cooperative.Scope) (context.Context, error) {
	if b == nil || b.main == nil || !scope.BelongsTo(b.main.Owner) {
		return nil, cooperative.ErrRestricted
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return context.WithValue(ctx, nativeOpenAIAuthScopeKey{}, scope), nil
}
func (b *NativeOpenAIAuth) GetCached(ctx context.Context, scope *cooperative.Scope) (*openaiauth.Value, error) {
	ctx, err := b.owned(ctx, scope)
	if err != nil {
		return nil, err
	}
	return b.service.GetCached(ctx), nil
}
func (b *NativeOpenAIAuth) HasAuth(ctx context.Context, scope *cooperative.Scope) (bool, error) {
	value, err := b.GetCached(ctx, scope)
	if err != nil {
		return false, err
	}
	return openaiauth.HasOpenAIAuth(value), nil
}
func (b *NativeOpenAIAuth) GetAsync(ctx context.Context, scope *cooperative.Scope) (*openaiauth.Value, error) {
	ctx, err := b.owned(ctx, scope)
	if err != nil {
		return nil, err
	}
	return b.service.GetAsync(ctx)
}
func (b *NativeOpenAIAuth) EnsureFresh(ctx context.Context, scope *cooperative.Scope) (*openaiauth.Value, error) {
	ctx, err := b.owned(ctx, scope)
	if err != nil {
		return nil, err
	}
	return b.service.EnsureFresh(ctx)
}
func (b *NativeOpenAIAuth) Save(ctx context.Context, scope *cooperative.Scope, tokens protocol.JSONObject) (openaiauth.Result, error) {
	ctx, err := b.owned(ctx, scope)
	if err != nil {
		return openaiauth.Result{}, err
	}
	return b.service.Save(ctx, tokens), nil
}
func (b *NativeOpenAIAuth) Delete(ctx context.Context, scope *cooperative.Scope) (bool, error) {
	ctx, err := b.owned(ctx, scope)
	if err != nil {
		return false, err
	}
	return b.service.Delete(ctx), nil
}
func (b *NativeOpenAIAuth) ClearCache(scope *cooperative.Scope) error {
	if b == nil || !scope.BelongsTo(b.main.Owner) {
		return cooperative.ErrRestricted
	}
	b.service.ClearCache()
	return nil
}
