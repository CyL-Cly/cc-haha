// Package authhelper owns the original process apiKeyHelper SWR cache. It
// executes only the configured credential command; it owns no Agent loop.
package authhelper

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Request struct {
	Command                                       string
	ProjectOrLocal, TrustAccepted, NonInteractive bool
	TTL                                           string
	Execute                                       func(context.Context, string) (string, error)
}
type Options struct {
	NowMillis func() float64
	Error     func(string)
	Dispatch  func(func()) error
}
type cached struct {
	value     string
	timestamp float64
}
type flight struct {
	done    chan struct{}
	value   *string
	failure error
	started float64
}
type Cache struct {
	mu       sync.Mutex
	options  Options
	value    *cached
	inflight *flight
	epoch    uint64
	ctx      context.Context
	cancel   context.CancelFunc
	jobs     sync.WaitGroup
	closed   bool
}

func New(options Options) *Cache {
	if options.NowMillis == nil {
		options.NowMillis = func() float64 { return float64(time.Now().UnixMilli()) }
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Cache{options: options, ctx: ctx, cancel: cancel}
}
func (c *Cache) Clear() { c.mu.Lock(); defer c.mu.Unlock(); c.epoch++; c.value = nil; c.inflight = nil }
func (c *Cache) Cached() *string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.value == nil {
		return nil
	}
	copy := c.value.value
	return &copy
}
func (c *Cache) ElapsedMillis() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inflight == nil || c.inflight.started == 0 {
		return 0
	}
	return c.options.NowMillis() - c.inflight.started
}
func TTL(raw string) float64 {
	if raw == "" {
		return 300000
	}
	text := strings.TrimFunc(raw, jsWhitespace)
	negative := false
	if strings.HasPrefix(text, "+") || strings.HasPrefix(text, "-") {
		negative = text[0] == '-'
		text = text[1:]
	}
	end := 0
	for end < len(text) {
		char := text[end]
		valid := char >= '0' && char <= '9'
		if !valid {
			break
		}
		end++
	}
	if end == 0 {
		return 300000
	}
	number, _ := strconv.ParseFloat(text[:end], 64)
	if negative {
		number = -number
	}
	if number < 0 || math.IsNaN(number) {
		return 300000
	}
	return number
}
func jsWhitespace(r rune) bool {
	return r >= 9 && r <= 13 || r == 32 || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
}

type Lookup struct {
	immediate *string
	pending   *flight
}

func (l *Lookup) Wait(ctx context.Context) (*string, error) {
	if l.pending == nil {
		return l.immediate, nil
	}
	select {
	case <-l.pending.done:
		return l.pending.value, l.pending.failure
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}
func (c *Cache) Begin(r Request) (*Lookup, error) {
	if r.Command == "" {
		return &Lookup{}, nil
	}
	if r.Execute == nil {
		return nil, errors.New("apiKeyHelper requires its actual command executor")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("apiKeyHelper cache closed")
	}
	if c.value != nil {
		copy := c.value.value
		if c.options.NowMillis()-c.value.timestamp >= TTL(r.TTL) && c.inflight == nil {
			c.startLocked(r, false)
		}
		return &Lookup{immediate: &copy}, nil
	}
	if c.inflight == nil {
		c.startLocked(r, true)
	}
	return &Lookup{pending: c.inflight}, nil
}
func (c *Cache) Get(ctx context.Context, r Request) (*string, error) {
	lookup, err := c.Begin(r)
	if err != nil {
		return nil, err
	}
	return lookup.Wait(ctx)
}
func (c *Cache) startLocked(r Request, cold bool) {
	pending := &flight{done: make(chan struct{})}
	if cold {
		pending.started = c.options.NowMillis()
	}
	c.inflight = pending
	epoch := c.epoch
	c.jobs.Add(1)
	go func() {
		defer c.jobs.Done()
		var value *string
		var failure error
		if !r.ProjectOrLocal || r.TrustAccepted || r.NonInteractive {
			active, cancel := context.WithTimeout(c.ctx, 10*time.Minute)
			text, err := r.Execute(active, r.Command)
			cancel()
			failure = err
			if err == nil {
				value = &text
			}
		}
		settle := func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if failure != nil {
				if epoch == c.epoch && c.options.Error != nil {
					c.options.Error(failure.Error())
				}
				sentinel := " "
				value = &sentinel
				if epoch == c.epoch {
					if !cold && c.value != nil && c.value.value != " " {
						c.value = &cached{c.value.value, c.options.NowMillis()}
						copy := c.value.value
						value = &copy
					} else {
						c.value = &cached{sentinel, c.options.NowMillis()}
					}
				}
			} else if epoch == c.epoch && value != nil {
				c.value = &cached{*value, c.options.NowMillis()}
			}
			if epoch == c.epoch {
				c.inflight = nil
			}
			pending.value = value
			close(pending.done)
		}
		if c.options.Dispatch != nil {
			if err := c.options.Dispatch(settle); err != nil {
				c.mu.Lock()
				pending.failure = err
				close(pending.done)
				c.mu.Unlock()
			}
		} else {
			settle()
		}

	}()
}
func (c *Cache) Close(ctx context.Context) error {
	c.mu.Lock()
	c.closed = true
	c.epoch++
	c.inflight = nil
	c.cancel()
	c.mu.Unlock()
	done := make(chan struct{})
	go func() { c.jobs.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
