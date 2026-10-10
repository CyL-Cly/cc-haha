package cloudauth

import (
	"context"
	"sync"
)

// AWSFileCache owns Smithy readFile's module-level promises. This stores the raw
// read (including rejection), not a parsed profile or a derived credential.
// The native caller runs Read in its existing physical-work future, so a waiter
// never blocks the cooperative owner that must settle another file read.
type AWSFileCache struct {
	mu      sync.Mutex
	entries map[string]*awsFileEntry
}
type awsFileEntry struct {
	done chan struct{}
	raw  []byte
	err  error
}

func (c *AWSFileCache) Read(ctx context.Context, path string, ignore bool, load func(context.Context, string) ([]byte, error)) ([]byte, error) {
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[string]*awsFileEntry{}
	}
	entry := c.entries[path]
	if entry != nil && !ignore {
		c.mu.Unlock()
		select {
		case <-entry.done:
			return append([]byte(nil), entry.raw...), entry.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	entry = &awsFileEntry{done: make(chan struct{})}
	c.entries[path] = entry
	c.mu.Unlock()
	raw, err := load(ctx, path)
	c.mu.Lock()
	entry.raw = append([]byte(nil), raw...)
	entry.err = err
	close(entry.done)
	if ctx.Err() != nil && c.entries[path] == entry {
		delete(c.entries, path)
	}
	c.mu.Unlock()
	return append([]byte(nil), raw...), err
}
