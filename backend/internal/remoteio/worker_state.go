package remoteio

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type workerStateUploader struct {
	c                *ccrClient
	mu               sync.Mutex
	pending          protocol.JSONObject
	draining, closed bool
}

func newWorkerStateUploader(c *ccrClient) *workerStateUploader { return &workerStateUploader{c: c} }
func coalesce(base, overlay protocol.JSONObject) protocol.JSONObject {
	merged := base.Clone()
	if !merged.IsObject() {
		merged = protocol.NewJSONObject()
	}
	for _, field := range overlay.OwnProperties() {
		var name string
		_ = json.Unmarshal(field.NameJSON, &name)
		if name == "external_metadata" || name == "internal_metadata" {
			previous, next := object(merged.Get(name)), object(field.Value)
			if previous.IsObject() && next.IsObject() {
				previous = previous.Clone()
				previous.Assign(next)
				merged.Set(name, raw(previous))
				continue
			}
		}
		_ = merged.SetPropertyJSON(field.NameJSON, field.Value)
	}
	return merged
}
func (u *workerStateUploader) enqueue(patch protocol.JSONObject) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closed {
		return
	}
	u.pending = coalesce(u.pending, patch)
	if !u.draining {
		u.draining = true
		if !u.c.s.spawn(u.drain) {
			u.draining = false
		}
	}
}
func (u *workerStateUploader) drain() {
	for {
		u.mu.Lock()
		if u.closed || !u.pending.IsObject() {
			u.draining = false
			u.mu.Unlock()
			return
		}
		current := u.pending
		u.pending = protocol.JSONObject{}
		u.mu.Unlock()
		failures := 0
		for u.c.s.ctx.Err() == nil {
			body := protocol.NewJSONObject(protocol.JSONField{Name: "worker_epoch", Value: raw(u.c.epoch)})
			body.Assign(current)
			ok, _ := u.c.request(u.c.s.ctx, http.MethodPut, "/worker", body, 10*time.Second)
			if ok {
				break
			}
			failures++
			delay := 500 * time.Millisecond
			for i := 1; i < failures && delay < 30*time.Second; i++ {
				delay *= 2
			}
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
			delay += time.Duration(u.c.s.options.Random() * float64(500*time.Millisecond))
			if u.c.s.options.Wait(u.c.s.ctx, delay) != nil {
				return
			}
			u.mu.Lock()
			if u.closed {
				u.mu.Unlock()
				return
			}
			if u.pending.IsObject() {
				current = coalesce(current, u.pending)
				u.pending = protocol.JSONObject{}
			}
			u.mu.Unlock()
		}
		if u.c.s.ctx.Err() != nil {
			return
		}
	}
}
func (u *workerStateUploader) close() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.closed = true
	u.pending = protocol.JSONObject{}
}
