package imruntime

import (
	"context"
	"sync"
	"time"
)

type MessageDedup struct {
	mu      sync.Mutex
	entries map[string]int64
	keys    []string
	Now     func() time.Time
	TTL     time.Duration
	Max     int
	cancel  context.CancelFunc
	done    chan struct{}
}

func NewMessageDedup(ctx context.Context) *MessageDedup {
	ctx, cancel := context.WithCancel(ctx)
	d := &MessageDedup{entries: map[string]int64{}, Now: time.Now, TTL: 10 * time.Minute, Max: 5000, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(d.done)
		timer := time.NewTicker(time.Minute)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				d.Sweep()
			}
		}
	}()
	return d
}
func (d *MessageDedup) TryRecord(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.Now().UnixMilli()
	if old, ok := d.entries[id]; ok && now-old < d.TTL.Milliseconds() {
		return false
	}
	if len(d.entries) >= d.Max && len(d.keys) > 0 {
		delete(d.entries, d.keys[0])
		d.keys = d.keys[1:]
	}
	if _, ok := d.entries[id]; !ok {
		d.keys = append(d.keys, id)
	}
	d.entries[id] = now
	return true
}
func (d *MessageDedup) Sweep() {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.Now().UnixMilli()
	for len(d.keys) > 0 {
		key := d.keys[0]
		if now-d.entries[key] < d.TTL.Milliseconds() {
			break
		}
		delete(d.entries, key)
		d.keys = d.keys[1:]
	}
}
func (d *MessageDedup) Close() {
	d.cancel()
	<-d.done
	d.mu.Lock()
	d.entries = map[string]int64{}
	d.keys = nil
	d.mu.Unlock()
}
