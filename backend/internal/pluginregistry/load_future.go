package pluginregistry

import (
	"context"
	"strings"
)

// LoadFuture is the actual memoized loader operation. Its identity survives
// completion; a caller can observe the same producer without restarting IO or
// substituting the flattened Result returned by LoadCached.
type LoadFuture = pluginLoadFuture

func (l *Loader) CachedProducer() *LoadFuture {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cachedPending != nil {
		return l.cachedPending
	}
	pending := &pluginLoadFuture{loader: l, done: make(chan struct{})}
	// The cache-only async function samples SYNC once, inside its memoized
	// body. Returning the full Promise does not make the wrapper identical.
	switch strings.ToLower(strings.TrimSpace(l.options.Environment("CLAUDE_CODE_SYNC_PLUGIN_INSTALL"))) {
	case "1", "true", "yes", "on":
		pending.delegate = l.fullProducerLocked()
		select {
		case <-pending.delegate.done:
			pending.result, pending.err = snapshotResult(pending.delegate.result), pending.delegate.err
			close(pending.done)
		default:
			pending.delegate.aliases = append(pending.delegate.aliases, pending)
		}
	}
	l.cachedPending = pending
	return pending
}

func (l *Loader) FullProducer() *LoadFuture {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fullProducerLocked()
}

func (l *Loader) fullProducerLocked() *pluginLoadFuture {
	if l.fullPending != nil {
		return l.fullPending
	}
	pending := &pluginLoadFuture{loader: l, done: make(chan struct{}), full: true}
	if l.options.Prepare == nil {
		pending.err = &CapabilityError{"full plugin source materialization"}
		pending.once.Do(func() { close(pending.done) })
		return pending
	}
	l.fullPending = pending
	return pending
}

// DelegatedProducer is the actual async `return loadAllPlugins()` arm. It
// exposes no result, callback, or replacement producer to a consumer.
func (future *pluginLoadFuture) DelegatedProducer() *LoadFuture { return future.delegate }

// Ready observes only a completed native operation. It does not run its body,
// wait for it, or create another Promise/job for an already cached value.
func (future *pluginLoadFuture) Ready() (Result, error, bool) {
	select {
	case <-future.done:
		future.loader.mu.Lock()
		defer future.loader.mu.Unlock()
		return snapshotResult(future.result), future.err, true
	default:
		return Result{}, nil, false
	}
}

// Load starts this exact producer once. Existing synchronous APIs still run
// their original body on the calling native worker; Owner consumers dispatch
// that same operation through their existing physical-work binding.
func (future *pluginLoadFuture) Load(ctx context.Context) (Result, error) {
	if future.delegate != nil {
		if _, failure := future.delegate.Load(ctx); failure != nil {
			if _, _, ready := future.Ready(); !ready {
				return Result{}, failure
			}
		}
		return future.wait(ctx)
	}
	start := false
	future.once.Do(func() { start = true })
	if start {
		l := future.loader
		result, failure := l.assemble(ctx, "", future.full)
		l.mu.Lock()
		if failure == nil {
			l.cacheID++
			result.cacheID = l.cacheID
			if future.full {
				l.cached = &result
				// loadAllPlugins warms cache-only with Promise.resolve(result),
				// a distinct completed operation rather than the full Promise.
				warm := &pluginLoadFuture{loader: l, done: make(chan struct{}), result: snapshotResult(result)}
				warm.once.Do(func() { close(warm.done) })
				l.cachedPending = warm
			} else if l.cachedPending == future {
				l.cached = &result
			}
		}
		future.result, future.err = snapshotResult(result), failure
		for _, alias := range future.aliases {
			alias.result, alias.err = snapshotResult(result), failure
			close(alias.done)
		}
		future.aliases = nil
		close(future.done)
		l.mu.Unlock()
	}
	return future.wait(ctx)
}

// BelongsTo checks the immutable factory that owns this admitted producer,
// including receipts retired by Clear; it grants no loading capability.
func (future *pluginLoadFuture) BelongsTo(loader *Loader) bool {
	return future != nil && loader != nil && future.loader == loader
}
