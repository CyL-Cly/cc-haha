package clibootstrap

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/unicode/norm"
)

type claudeMCPAuthFlight struct {
	done    chan struct{}
	changed bool
	err     error
}

// The original auth promises are process-scoped. Proxy requests share ordinary
// checks and share 401 recovery by the token actually sent, not the current one.
type nativeClaudeMCPRefresh struct {
	command  *MainCommand
	run      func(context.Context, func(*cooperative.Scope) error) error
	read     func(context.Context, bool) (*openaiauth.Value, error)
	client   *cliauth.OAuthClient
	lifetime context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	pending  *claudeMCPAuthFlight
	failed   map[string]*claudeMCPAuthFlight
	closed   bool
	workers  sync.WaitGroup
	// These fields are accessed only on the existing credential owner.
	mtime          float64
	invalidateMemo func()
}

func (a *nativeClaudeMCPRefresh) close() {
	a.mu.Lock()
	a.closed = true
	a.cancel()
	a.mu.Unlock()
	a.workers.Wait()
}

func (a *nativeClaudeMCPRefresh) flight(ctx context.Context, failed *string, work func(context.Context) (bool, error)) (bool, error) {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return false, context.Canceled
	}
	current := a.pending
	if failed != nil {
		current = a.failed[*failed]
	}
	if current == nil {
		current = &claudeMCPAuthFlight{done: make(chan struct{})}
		if failed == nil {
			a.pending = current
		} else {
			if a.failed == nil {
				a.failed = map[string]*claudeMCPAuthFlight{}
			}
			a.failed[*failed] = current
		}
		a.workers.Add(1)
		go func(f *claudeMCPAuthFlight) {
			defer a.workers.Done()
			f.changed, f.err = work(a.lifetime)
			a.mu.Lock()
			if failed == nil {
				a.pending = nil
			} else {
				delete(a.failed, *failed)
			}
			close(f.done)
			a.mu.Unlock()
		}(current)
	}
	a.mu.Unlock()
	select {
	case <-current.done:
		return current.changed, current.err
	case <-ctx.Done():
		return false, context.Cause(ctx)
	}
}

func (a *nativeClaudeMCPRefresh) check(ctx context.Context) error {
	_, err := a.flight(ctx, nil, func(ctx context.Context) (bool, error) { return a.checkImpl(ctx, 0, false) })
	return err
}

func (a *nativeClaudeMCPRefresh) handle401(ctx context.Context, sent string) (bool, error) {
	return a.flight(ctx, &sent, func(ctx context.Context) (bool, error) {
		current, err := a.read(ctx, true)
		if err != nil || current == nil || !openaiauth.AuthTruthy(current.Get("refreshToken")) {
			return false, err
		}
		if cliauth.OAuthFieldString(current.Get("accessToken")) != sent {
			return true, nil
		}
		return a.checkImpl(ctx, 0, true)
	})
}

func mcpRefreshPhysical[T any](a *nativeClaudeMCPRefresh, ctx context.Context, work func() (T, error)) (T, error) {
	var value T
	err := a.run(ctx, func(scope *cooperative.Scope) error {
		var err error
		value, err = cliAuthPhysical(a.command, scope, work)
		return err
	})
	return value, err
}

func (a *nativeClaudeMCPRefresh) checkImpl(ctx context.Context, retry int, force bool) (bool, error) {
	m := a.command
	dir := m.Native.Environment["CLAUDE_CONFIG_DIR"]
	if dir == "" && !m.Native.ConfigDirExplicit {
		dir = filepath.Join(m.Native.Home, ".claude")
	}
	err := a.run(ctx, func(scope *cooperative.Scope) error {
		info, err := cliAuthPhysical(m, scope, func() (os.FileInfo, error) { return m.Native.stat(filepath.Join(dir, ".credentials.json")) })
		if err != nil {
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}
			a.invalidateMemo()
			return nil
		}
		next := float64(info.ModTime().UnixNano()) / 1e6
		if next != a.mtime {
			a.mtime = next
			a.invalidateMemo()
			m.Secure.Clear()
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	tokens, err := a.read(ctx, false)
	if err != nil || tokens == nil || !openaiauth.AuthTruthy(tokens.Get("refreshToken")) {
		return false, err
	}
	if !force && !cliauth.OAuthTokenExpired(tokens.Get("expiresAt"), m.Native.NowMillis()) {
		return false, nil
	}
	subscriber, err := openaiauth.ClaudeSubscriber(true, tokens)
	if err != nil || !subscriber {
		return false, err
	}
	tokens, err = a.read(ctx, true)
	if err != nil || tokens == nil || !openaiauth.AuthTruthy(tokens.Get("refreshToken")) || !cliauth.OAuthTokenExpired(tokens.Get("expiresAt"), m.Native.NowMillis()) {
		return false, err
	}
	dir = norm.NFC.String(dir)
	if err = m.Native.check("oauth-refresh-directory", dir); err != nil {
		return false, err
	}
	if _, err = mcpRefreshPhysical(a, ctx, func() (struct{}, error) { return struct{}{}, os.MkdirAll(dir, 0700) }); err != nil {
		return false, err
	}
	release, err := mcpRefreshPhysical(a, ctx, func() (func() error, error) { return m.Global.AcquireSourceDirectoryLease(dir) })
	if err != nil {
		var busy *cliglobalconfig.SourceLeaseBusyError
		if errors.As(err, &busy) && retry < 5 {
			var random [8]byte
			if _, err = rand.Read(random[:]); err != nil {
				return false, err
			}
			wait := time.Duration(1000+float64(binary.LittleEndian.Uint64(random[:])>>11)/float64(uint64(1)<<53)*1000) * time.Millisecond
			if _, err = mcpRefreshPhysical(a, ctx, func() (struct{}, error) {
				timer := time.NewTimer(wait)
				defer timer.Stop()
				select {
				case <-timer.C:
					return struct{}{}, nil
				case <-ctx.Done():
					return struct{}{}, context.Cause(ctx)
				}
			}); err != nil {
				return false, err
			}
			return a.checkImpl(ctx, retry+1, force)
		}
		if ctx.Err() != nil {
			return false, context.Cause(ctx)
		}
		return false, nil
	}
	defer release()
	tokens, err = a.read(ctx, true)
	if err != nil || tokens == nil || !openaiauth.AuthTruthy(tokens.Get("refreshToken")) || !cliauth.OAuthTokenExpired(tokens.Get("expiresAt"), m.Native.NowMillis()) {
		return false, err
	}
	refreshed, err := mcpRefreshPhysical(a, ctx, func() (protocol.JSONObject, error) {
		return a.client.RefreshClaude(ctx, currentTokenString(tokens, "refreshToken"), nil)
	})
	if err != nil {
		if ctx.Err() != nil {
			return false, context.Cause(ctx)
		}
		current, e := a.read(ctx, true)
		return current != nil && !cliauth.OAuthTokenExpired(current.Get("expiresAt"), m.Native.NowMillis()), e
	}
	err = a.run(ctx, func(scope *cooperative.Scope) error {
		value := openaiauth.ObjectValue(refreshed)
		eligible, e := openaiauth.ClaudeSubscriber(true, value)
		if e != nil {
			return nil
		}
		if eligible && openaiauth.AuthTruthy(value.Get("refreshToken")) && openaiauth.AuthTruthy(value.Get("expiresAt")) {
			_, e = m.Secure.Change(ctx, func(current protocol.JSONObject) error {
				previous, _ := current.Object("claudeAiOauth")
				stored := protocol.NewJSONObject()
				for _, key := range []string{"accessToken", "refreshToken", "expiresAt", "scopes", "subscriptionType", "rateLimitTier"} {
					field := refreshed.Get(key)
					if (key == "subscriptionType" || key == "rateLimitTier") && (field == nil || string(field) == "null") {
						field = previous.Get(key)
						if field == nil {
							field = []byte("null")
						}
					}
					stored.Set(key, field)
				}
				current.Set("claudeAiOauth", rawMain(stored))
				return nil
			})
			// Original save failure is logged and does not turn refresh success into false.
			if e != nil && ctx.Err() != nil {
				return context.Cause(ctx)
			}
		}
		a.invalidateMemo()
		m.Secure.Clear()
		return nil
	})
	return err == nil, err
}
