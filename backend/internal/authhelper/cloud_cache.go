package authhelper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// CloudCache ports memoizeWithTTLAsync for auth.ts's two zero-argument cloud
// producers. Its stale-error policy differs from apiKeyHelper: it deletes the
// stale entry, rather than retaining the prior credential for another TTL.
type CloudCache struct {
	mu      sync.Mutex
	options Options
	entry   *cloudEntry
	flight  *cloudFlight
	ctx     context.Context
	cancel  context.CancelFunc
	jobs    sync.WaitGroup
	closed  bool
}
type cloudEntry struct {
	value      json.RawMessage
	timestamp  float64
	refreshing bool
}
type cloudFlight struct {
	done    chan struct{}
	value   json.RawMessage
	failure error
}
type CloudLookup struct {
	immediate json.RawMessage
	pending   *cloudFlight
	cached    bool
}

const CloudCredentialTTLMillis = 60 * 60 * 1000

func NewCloudCache(options Options) *CloudCache {
	if options.NowMillis == nil {
		options.NowMillis = func() float64 { return float64(time.Now().UnixMilli()) }
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &CloudCache{options: options, ctx: ctx, cancel: cancel}
}
func (c *CloudCache) Clear() { c.mu.Lock(); c.entry = nil; c.flight = nil; c.mu.Unlock() }
func (c *CloudCache) Cached() (json.RawMessage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entry == nil {
		return nil, false
	}
	return bytes.Clone(c.entry.value), true
}
func (c *CloudCache) Begin(execute func(context.Context) (json.RawMessage, error)) (*CloudLookup, error) {
	if execute == nil {
		return nil, errors.New("cloud helper requires actual source producer")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("cloud helper cache closed")
	}
	now := c.options.NowMillis()
	if c.entry != nil {
		lookup := &CloudLookup{immediate: bytes.Clone(c.entry.value), cached: true}
		if now-c.entry.timestamp > CloudCredentialTTLMillis && !c.entry.refreshing {
			c.entry.refreshing = true
			c.startLocked(execute, now, c.entry)
		}
		return lookup, nil
	}
	if c.flight == nil {
		c.startLocked(execute, now, nil)
	}
	return &CloudLookup{pending: c.flight}, nil
}
func (l *CloudLookup) Wait(ctx context.Context) (json.RawMessage, error) {
	if l.cached {
		return bytes.Clone(l.immediate), nil
	}
	select {
	case <-l.pending.done:
		return bytes.Clone(l.pending.value), l.pending.failure
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (c *CloudCache) startLocked(execute func(context.Context) (json.RawMessage, error), started float64, stale *cloudEntry) {
	flight := &cloudFlight{done: make(chan struct{})}
	if stale == nil {
		c.flight = flight
	}
	c.jobs.Add(1)
	go func() {
		defer c.jobs.Done()
		value, failure := execute(c.ctx)
		settle := func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if stale == nil {
				if c.flight == flight {
					if failure == nil && !c.closed {
						c.entry = &cloudEntry{value: bytes.Clone(value), timestamp: started}
					}
					c.flight = nil
				}
			} else {
				if failure != nil && c.options.Error != nil {
					c.options.Error(failure.Error())
				}
				if c.entry == stale {
					if failure == nil && !c.closed {
						c.entry = &cloudEntry{value: bytes.Clone(value), timestamp: c.options.NowMillis()}
					} else {
						c.entry = nil
					}
				}
			}
			flight.value, flight.failure = bytes.Clone(value), failure
			close(flight.done)
		}
		if c.options.Dispatch != nil {
			if err := c.options.Dispatch(settle); err != nil {
				flight.failure = err
				close(flight.done)
			}
		} else {
			settle()
		}
	}()
}
func (c *CloudCache) Close(ctx context.Context) error {
	c.mu.Lock()
	c.closed = true
	c.entry = nil
	c.flight = nil
	c.cancel()
	c.mu.Unlock()
	done := make(chan struct{})
	go func() { c.jobs.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
