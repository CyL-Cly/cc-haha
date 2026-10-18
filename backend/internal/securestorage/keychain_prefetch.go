package securestorage

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var ErrKeychainPrefetchState = errors.New("invalid Keychain prefetch lifecycle")

type keychainPrefetchArm struct {
	command                 Command
	started, done, timedOut bool
	running                 bool
	ctx                     context.Context
	cancel                  context.CancelFunc
	process                 *KeychainPrefetchProcess
	launchError             error
	stdout                  string
}

// KeychainPrefetch is the concrete keychainPrefetch.ts two-read producer.
// Its result cannot be supplied by a caller: only these actual Store primitive
// operations fill the private arms, and Publish consumes both original reads.
type KeychainPrefetch struct {
	mu                                 sync.Mutex
	store                              *Store
	ctx                                context.Context
	cancel                             context.CancelFunc
	arms                               [2]keychainPrefetchArm
	published, closed, legacyAvailable bool
}

func (s *Store) NewKeychainPrefetch(ctx context.Context) (*KeychainPrefetch, error) {
	if s == nil || ctx == nil || s.options.Platform != "darwin" {
		return nil, ErrKeychainPrefetchState
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	account, oauthService := s.keychainUsername(), s.keychainServiceName()
	s.mu.Unlock()
	directory, environment := s.options.Home, s.options.Environment
	if s.options.NativeSecurityContext != nil {
		directory, environment = s.options.NativeSecurityContext()
	}
	if !filepath.IsAbs(directory) || environment == nil {
		return nil, ErrKeychainPrefetchState
	}
	launchEnvironment := map[string]string{}
	for name, value := range environment {
		launchEnvironment[name] = value
	}
	legacyService := legacyServiceName(oauthService)
	life, cancel := context.WithCancel(ctx)
	p := &KeychainPrefetch{store: s, ctx: life, cancel: cancel}
	for index, service := range []string{oauthService, legacyService} {
		p.arms[index].command = Command{Args: []string{"find-generic-password", "-a", account, "-w", "-s", service}, prefetch: true, prefetchDirectory: directory, prefetchEnvironment: launchEnvironment}
	}
	return p, nil
}

func (p *KeychainPrefetch) Owns(s *Store) bool { return p != nil && p.store == s }
func (p *KeychainPrefetch) StartOAuth() error  { return p.start(0) }
func (p *KeychainPrefetch) StartLegacy() error { return p.start(1) }
func (p *KeychainPrefetch) RunOAuth() error    { return p.run(0) }
func (p *KeychainPrefetch) RunLegacy() error   { return p.run(1) }

// Start is execFile's synchronous external admission. No headers/output/exit
// is awaited here. A custom blocking read callback cannot acquire this phase
// and must explicitly bind a real started-process primitive instead.
func (p *KeychainPrefetch) start(index int) error {
	if p == nil {
		return ErrKeychainPrefetchState
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.arms[index].started {
		return ErrKeychainPrefetchState
	}
	if err := context.Cause(p.ctx); err != nil {
		return err
	}
	if p.store.options.StartSecurity == nil && p.store.securityPrimitiveProvided {
		return errors.New("Keychain prefetch requires actual subprocess admission primitive")
	}
	p.arms[index].started = true
	command := p.arms[index].command
	command.Args = append([]string(nil), command.Args...)
	ctx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
	p.arms[index].ctx, p.arms[index].cancel = ctx, cancel
	var process *KeychainPrefetchProcess
	var failure error
	if primitive := p.store.options.StartSecurity; primitive != nil {
		process, failure = primitive(ctx, command)
	} else {
		process, failure = StartKeychainPrefetchProcess(ctx, newSecurityCommand(ctx, command, command.prefetchDirectory, command.prefetchEnvironment))
	}
	if process != nil && failure != nil || failure == nil && (process == nil || process.PID() <= 0 || process.ctx != ctx) {
		if process != nil && process.PID() > 0 {
			_ = process.terminate()
			_, _ = process.wait()
		}
		cancel()
		return ErrKeychainPrefetchState
	}
	p.arms[index].process, p.arms[index].launchError = process, failure
	return nil // original spawn failures resolve in the later callback
}

func (p *KeychainPrefetch) run(index int) error {
	if p == nil {
		return ErrKeychainPrefetchState
	}
	p.mu.Lock()
	if p.closed || p.arms[index].running {
		p.mu.Unlock()
		return ErrKeychainPrefetchState
	}
	started := p.arms[index].started
	// Read-only unit adapters retain the existing Store I/O contract. The
	// MainCommand caller always requires StartOAuth/StartLegacy first; these
	// blocking adapters cannot be used as proof of subprocess admission.
	blockingAdapter := !started && p.store.options.StartSecurity == nil && p.store.securityPrimitiveProvided
	p.mu.Unlock()
	if !started && !blockingAdapter {
		if err := p.start(index); err != nil {
			return err
		}
	}
	p.mu.Lock()
	if p.closed || p.arms[index].running {
		p.mu.Unlock()
		return ErrKeychainPrefetchState
	}
	p.arms[index].started, p.arms[index].running = true, true
	command := p.arms[index].command
	command.Args = append([]string(nil), command.Args...)
	ctx, cancel := p.arms[index].ctx, p.arms[index].cancel
	if blockingAdapter {
		ctx, cancel = context.WithTimeout(p.ctx, 10*time.Second)
	}
	process, failure := p.arms[index].process, p.arms[index].launchError
	p.mu.Unlock()
	defer cancel()
	var data []byte
	if blockingAdapter {
		data, failure = p.store.options.RunSecurity(ctx, command)
	} else if process != nil {
		data, failure = process.wait()
	}
	// The default primitive reports its actual timeout/maxBuffer kill. An
	// injected context-aware primitive also exposes our own rejected deadline;
	// an unrelated exit/signal/spawn failure is not err.killed.
	var killed *keychainPrefetchKilledError
	timedOut := errors.As(failure, &killed) || blockingAdapter && failure != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) && p.ctx.Err() == nil
	if err := context.Cause(p.ctx); err != nil {
		return err
	}
	stdout := ""
	if failure == nil {
		stdout = protocol.TrimJavaScriptString(string(protocol.DecodeJavaScriptUTF8(data)))
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrKeychainPrefetchState
	}
	p.arms[index].done, p.arms[index].timedOut, p.arms[index].stdout = true, timedOut, stdout
	// Original spawnSecurity resolves for entry-not-found and spawn failures.
	// Only an actual killed receipt suppresses this arm's later cache publication.
	return nil
}

func (p *KeychainPrefetch) Publish() error {
	if p == nil {
		return ErrKeychainPrefetchState
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrKeychainPrefetchState
	}
	if err := context.Cause(p.ctx); err != nil {
		return err
	}
	if !p.arms[0].done || !p.arms[1].done {
		return ErrKeychainPrefetchState
	}
	if p.published {
		return nil
	}
	p.published = true
	if !p.arms[0].timedOut {
		p.store.mu.Lock()
		// A read/update before Promise.all settled retains its authority.
		if p.store.cachedAt.IsZero() {
			stdout := p.arms[0].stdout
			var raw []byte
			var err error
			if stdout != "" {
				raw, err = protocol.ParseAndStringifyJavaScriptJSON([]byte(stdout))
			}
			if err == nil {
				if string(raw) == "null" {
					raw = nil
				}
				p.store.cachedJSON, p.store.cachedAt = rawCopy(raw), p.store.options.Now()
			}
		}
		p.store.mu.Unlock()
	}
	p.legacyAvailable = !p.arms[1].timedOut
	return nil
}

func (p *KeychainPrefetch) LegacyResult() (string, bool) {
	if p == nil {
		return "", false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || !p.published || !p.legacyAvailable {
		return "", false
	}
	return p.arms[1].stdout, true
}

func (p *KeychainPrefetch) ClearLegacy() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.legacyAvailable = false
}

func (p *KeychainPrefetch) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.closed = true
	p.cancel()
	processes := []*KeychainPrefetchProcess{p.arms[0].process, p.arms[1].process}
	for _, arm := range p.arms {
		if arm.cancel != nil {
			arm.cancel()
		}
	}
	p.mu.Unlock()
	// Also reap a started process if a later launch/dispatch failed before
	// any native Wait worker could acquire it. Wait itself is once-only.
	for _, process := range processes {
		if process != nil {
			_, _ = process.wait()
		}
	}
}
