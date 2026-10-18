package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	weburl "github.com/nlnwa/whatwg-url/url"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// One process authority is borrowed by scoped agents. Promise state and cache
// publication belong to its original Owner; workers see immutable request data.
// A cold synchronous getter never starts HTTP or proves remote policy absent.
type nativeRemoteManagedPolicy struct {
	n                             *NativeBoundary
	owner                         *cooperative.Owner
	mu                            sync.Mutex
	life                          context.Context
	cancel                        context.CancelFunc
	closed, started               bool
	closeDone                     chan struct{}
	loadingWaiters                []*ownerasync.OwnerFuture[struct{}]
	cacheListeners                map[uint64]func()
	cacheListenerSequence         uint64
	eligible                      *bool
	session                       *protocol.JSONObject // nil is absent; an empty object is present.
	refs                          ownerasync.NativeReferences
	workers                       sync.WaitGroup
	loading                       *ownerasync.OwnerFuture[struct{}]
	stopLoadingTimer, stopPolling func()
	sample                        func(*cooperative.Scope, bool) (nativeManagedCredentials, error)
	refresh                       *nativeClaudeMCPRefresh
	client                        *http.Client
	endpoint                      string
	configurationError            error
	interactive                   bool
	commandDialog                 func(context.Context, string, []string) (bool, error)
	notify                        func(*cooperative.Scope) error
	invalidate                    func()
	configure                     func(*cooperative.Scope) error
	failureContext                context.Context
	failSecurity                  context.CancelCauseFunc
	consentSequence               uint64
	consents                      map[string]*nativeManagedConsent
	listeners                     map[uint64]func(NativeManagedPolicyConsentEvent)
	listenerSequence              uint64
}

type nativeManagedCredentials struct {
	APIKey string
	OAuth  *protocol.JSONObject
}
type nativeManagedLoad struct{ settings *protocol.JSONObject }

func (s *nativeRemoteManagedPolicy) check(scope *cooperative.Scope) error {
	if s == nil || !scope.BelongsTo(s.owner) {
		return cooperative.ErrRestricted
	}
	if err := scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil }); err != nil {
		return err
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return cooperative.ErrClosed
	}
	return nil
}

func (s *nativeRemoteManagedPolicy) onOwner(ctx context.Context, work func(*cooperative.Scope) error) error {
	// Only the admitted native load/poll/refresh producer uses this completion
	// bridge. It does not start another ASK or retain an earlier Scope.
	receipt, err := s.owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if err := s.check(scope); err != nil {
			return err
		}
		return work(scope)
	})
	if err != nil {
		return err
	}
	return receipt.Wait()
}

func remoteManagedProviderEligible(env map[string]string) bool {
	if env["CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH"] == "1" || env["CLAUDE_CODE_ENTRYPOINT"] == "local-agent" || !nativeCommandFirstParty(env) {
		return false
	}
	base := env["ANTHROPIC_BASE_URL"]
	if base == "" {
		return true
	}
	parsed, err := weburl.Parse(base)
	if err != nil {
		return false
	}
	return parsed.Host() == "api.anthropic.com" || env["USER_TYPE"] == "ant" && parsed.Host() == "api-staging.anthropic.com"
}
func remoteManagedCredentialEligible(c nativeManagedCredentials) bool {
	return remoteManagedOAuthEligible(c.OAuth) || c.APIKey != ""
}
func remoteManagedOAuthEligible(tokens *protocol.JSONObject) bool {
	if tokens == nil || tokens.String("accessToken") == "" {
		return false
	}
	if string(tokens.Get("subscriptionType")) == "null" {
		return true
	}
	if tokens.String("subscriptionType") == "team" || tokens.String("subscriptionType") == "enterprise" {
		var scopes []string
		_ = json.Unmarshal(tokens.Get("scopes"), &scopes)
		for _, scope := range scopes {
			if scope == "user:inference" {
				return true
			}
		}
	}
	return false
}

func (s *nativeRemoteManagedPolicy) prepare(scope *cooperative.Scope) error {
	if err := s.check(scope); err != nil {
		return err
	}
	s.mu.Lock()
	known := s.eligible != nil
	s.mu.Unlock()
	if known {
		return nil
	}
	eligible := false
	if remoteManagedProviderEligible(s.n.nativeEnvironmentSnapshot()) {
		credentials, err := s.sample(scope, true)
		if err != nil {
			return err
		}
		eligible = remoteManagedCredentialEligible(credentials)
	}
	s.mu.Lock()
	s.eligible = &eligible
	s.mu.Unlock()
	if eligible {
		// The Source leaf invalidates merged settings when a disk cache first becomes
		// available, including an empty object. Publication does not perform HTTP.
		_ = s.snapshot()
		return nil
	}
	return nil
}

func (s *nativeRemoteManagedPolicy) snapshot() *protocol.JSONObject {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.eligible == nil || !*s.eligible {
		return nil
	}
	if s.session != nil {
		return s.session
	}
	if s.closed {
		return nil
	}
	raw, err := s.n.readFile(filepath.Join(s.n.ConfigHomeDirectory(), "remote-settings.json"))
	if err != nil {
		return nil
	}
	var value protocol.JSONObject
	if value.UnmarshalJSON(bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})) != nil || !value.IsObject() {
		return nil
	}
	s.session = &value
	if s.invalidate != nil {
		s.invalidate()
	}
	return s.session
}

func (s *nativeRemoteManagedPolicy) after(delay time.Duration, work func(*cooperative.Scope) error) func() {
	var once sync.Once
	done := make(chan struct{})
	fire := func() {
		once.Do(func() { close(done) })
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return
		}
		_, _ = s.owner.SubmitIndependent(s.life, cooperative.Read, func(scope *cooperative.Scope) error {
			if err := s.check(scope); err != nil {
				return err
			}
			return work(scope)
		})
	}
	var stop func()
	if s.n.Timer != nil {
		stop = s.n.Timer(delay, fire)
	} else {
		timer := time.AfterFunc(delay, fire)
		stop = func() { timer.Stop() }
	}
	go func() {
		select {
		case <-s.life.Done():
			stop()
		case <-done:
		}
	}()
	return func() { stop(); once.Do(func() { close(done) }) }
}
func (s *nativeRemoteManagedPolicy) initializeLoading(scope *cooperative.Scope) error {
	s.mu.Lock()
	existing := s.loading
	s.mu.Unlock()
	if existing != nil {
		return nil
	}
	future, err := ownerasync.NewOwnerFuture[struct{}](s.owner)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.loading = future
	s.loadingWaiters = append(s.loadingWaiters, future)
	s.mu.Unlock()
	s.stopLoadingTimer = s.after(30*time.Second, func(next *cooperative.Scope) error { return s.resolveLoading(next) })
	return nil
}
func (s *nativeRemoteManagedPolicy) resolveLoading(scope *cooperative.Scope) error {
	if s.stopLoadingTimer != nil {
		s.stopLoadingTimer()
		s.stopLoadingTimer = nil
	}
	s.mu.Lock()
	loading := s.loading
	s.mu.Unlock()
	if loading == nil {
		return nil
	}
	return loading.Resolve(scope, struct{}{})
}
func (s *nativeRemoteManagedPolicy) wait(scope *cooperative.Scope) error {
	if err := s.check(scope); err != nil {
		return err
	}
	s.mu.Lock()
	loading := s.loading
	s.mu.Unlock()
	if loading == nil {
		return nil
	}
	_, err := loading.Await(scope)
	return err
}
func (s *nativeRemoteManagedPolicy) waitOffOwner(ctx context.Context) error {
	s.mu.Lock()
	loading := s.loading
	s.mu.Unlock()
	if loading == nil {
		return nil
	}
	_, err := loading.Wait(ctx)
	return err
}
func (s *nativeRemoteManagedPolicy) changed(scope *cooperative.Scope) error {
	if s.notify != nil {
		if err := s.notify(scope); err != nil {
			return err
		}
	}
	s.mu.Lock()
	listeners := make([]func(), 0, len(s.cacheListeners))
	for _, listener := range s.cacheListeners {
		listeners = append(listeners, listener)
	}
	s.mu.Unlock()
	for _, listener := range listeners {
		listener()
	}
	return nil
}
func (s *nativeRemoteManagedPolicy) begin(scope *cooperative.Scope) error {
	if err := s.check(scope); err != nil {
		return err
	}
	if err := s.prepare(scope); err != nil {
		return err
	}
	if s.started {
		return nil
	}
	s.started = true
	s.mu.Lock()
	eligible := s.eligible != nil && *s.eligible
	s.mu.Unlock()
	if !eligible {
		return nil
	}
	if err := s.initializeLoading(scope); err != nil {
		return err
	}
	if s.snapshot() != nil {
		if err := s.resolveLoading(scope); err != nil {
			return err
		}
	}
	return s.load(scope, false, false)
}

func (s *nativeRemoteManagedPolicy) load(scope *cooperative.Scope, poll, auth bool) error {
	if err := s.check(scope); err != nil {
		return err
	}
	cached := s.snapshot()
	var previous []byte
	if cached != nil {
		raw, _ := cached.MarshalJSON()
		previous = protocol.StringifyValidatedJavaScriptJSON(raw)
	}
	binding, err := s.refs.Bind(s.owner)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return cooperative.ErrClosed
	}
	s.workers.Add(1)
	s.mu.Unlock()
	future, err := nativeOwnedWork(s.owner, binding, scope, func() (nativeManagedLoad, error) {
		defer s.workers.Done()
		return s.fetchAndLoad(cached)
	})
	if err != nil {
		s.workers.Done()
		return err
	}
	_, err = future.Then(scope, func(next *cooperative.Scope, value nativeManagedLoad, failure error) error {
		if err := s.check(next); err != nil {
			return nil
		}
		// Source's loading resolver is a finally obligation, including a listener
		// failure. The listener's original error still settles this return Promise.
		defer func() { _ = s.resolveLoading(next) }()
		s.mu.Lock()
		eligible := s.eligible != nil && *s.eligible
		s.mu.Unlock()
		if eligible && !auth && !poll {
			s.startPolling(next)
		}
		if auth || failure == nil && (poll || value.settings != nil) {
			changed := true
			if poll {
				current := s.snapshot()
				var raw []byte
				if current != nil {
					encoded, _ := current.MarshalJSON()
					raw = protocol.StringifyValidatedJavaScriptJSON(encoded)
				}
				changed = !bytes.Equal(previous, raw)
			}
			if changed {
				if err := s.changed(next); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return err
}
func (s *nativeRemoteManagedPolicy) startPolling(scope *cooperative.Scope) {
	if s.stopPolling != nil {
		return
	}
	s.stopPolling = s.after(time.Hour, func(next *cooperative.Scope) error {
		s.stopPolling = nil
		s.mu.Lock()
		eligible := s.eligible != nil && *s.eligible
		s.mu.Unlock()
		if eligible {
			s.startPolling(next)
			return s.load(next, true, false)
		}
		return nil
	})
}

func (s *nativeRemoteManagedPolicy) clear(scope *cooperative.Scope) error {
	if err := s.check(scope); err != nil {
		return err
	}
	if s.stopPolling != nil {
		s.stopPolling()
		s.stopPolling = nil
	}
	// Source does not cancel an already armed initialization timeout here.
	s.mu.Lock()
	s.eligible, s.session = nil, nil
	s.mu.Unlock()
	// Source clear drops the global resolver, not the old Promise, and does not
	// cancel a previously in-flight fetch. No invented auth epoch is added here.
	s.mu.Lock()
	s.loading = nil
	s.mu.Unlock()
	path := filepath.Join(s.n.ConfigHomeDirectory(), "remote-settings.json")
	binding, err := s.refs.Bind(s.owner)
	if err != nil {
		return err
	}
	future, err := nativeOwnedWork(s.owner, binding, scope, func() (struct{}, error) { _ = s.removeCache(path); return struct{}{}, nil })
	if err != nil {
		return err
	}
	_, err = future.Await(scope)
	return err
}
func (s *nativeRemoteManagedPolicy) refreshAfterAuth(scope *cooperative.Scope) error {
	if err := s.clear(scope); err != nil {
		return err
	}
	if err := s.prepare(scope); err != nil {
		return err
	}
	s.mu.Lock()
	eligible := s.eligible != nil && *s.eligible
	s.mu.Unlock()
	if !eligible {
		return s.changed(scope)
	}
	if s.configure != nil {
		if err := s.configure(scope); err != nil {
			return err
		}
	}
	// Source refresh does not start the one-hour poll or initialize loading.
	return s.load(scope, false, true)
}

func (s *nativeRemoteManagedPolicy) close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		done := s.closeDone
		s.mu.Unlock()
		if done != nil {
			<-done
		}
		return
	}
	s.closed = true
	s.closeDone = make(chan struct{})
	s.cancel()
	s.failSecurity(cooperative.ErrClosed)
	pending := make([]*nativeManagedConsent, 0, len(s.consents))
	for _, consent := range s.consents {
		pending = append(pending, consent)
	}
	waiters := append([]*ownerasync.OwnerFuture[struct{}]{}, s.loadingWaiters...)
	refresh, client := s.refresh, s.client
	s.mu.Unlock()
	for _, loading := range waiters {
		_ = loading.RejectAdministrative(cooperative.ErrClosed)
	}
	for _, consent := range pending {
		_ = consent.result.RejectAdministrative(cooperative.ErrClosed)
	}
	if refresh != nil {
		refresh.close()
	}
	s.workers.Wait()
	if count, idle := s.refs.Snapshot(); count != 0 {
		<-idle
	}
	if client != nil {
		client.CloseIdleConnections()
	}
	close(s.closeDone)
}

func (s *nativeRemoteManagedPolicy) liveError(err error) error {
	if s.life.Err() != nil {
		return context.Cause(s.life)
	}
	if errors.Is(err, cooperative.ErrClosed) || errors.Is(err, cooperative.ErrRestricted) {
		return err
	}
	return nil
}

// Ordinary request cancellation does not own the detached fetch. A rejected
// actual security dialog, however, is Source's process-exit boundary.
func (s *nativeRemoteManagedPolicy) bindProcessContext(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	stop := context.AfterFunc(s.failureContext, func() { cancel(context.Cause(s.failureContext)) })
	return ctx, func() { stop(); cancel(nil) }
}
