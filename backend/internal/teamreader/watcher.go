package teamreader

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamplan"
)

// Watcher observes the original CLI-owned files. Its events invalidate joined
// desktop workbenches; they do not launch members or grant plan approval.
type Watcher struct {
	reader          *Service
	emit            func(protocol.JSONObject, string)
	mu              sync.Mutex
	flight          chan struct{}
	ctx             context.Context
	cancel          context.CancelFunc
	done            chan struct{}
	started, closed bool
	snapshots       map[string]string
	plans           map[string]string
	fingerprints    map[string]string
	leads           map[string]string
	identities      map[string]protocol.JSONObject
	order           []string
}

func NewWatcher(reader *Service, emit func(protocol.JSONObject, string)) (*Watcher, error) {
	if reader == nil || emit == nil {
		return nil, errors.New("Team watcher requires actual reader and session event producer")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Watcher{reader: reader, emit: emit, ctx: ctx, cancel: cancel, done: make(chan struct{}), snapshots: map[string]string{}, plans: map[string]string{}, fingerprints: map[string]string{}, leads: map[string]string{}, identities: map[string]protocol.JSONObject{}}, nil
}

// Start is idempotent, performs an immediate poll and owns a single interval.
func (w *Watcher) Start(parent context.Context) {
	w.mu.Lock()
	if w.started || w.closed {
		w.mu.Unlock()
		return
	}
	w.started = true
	w.mu.Unlock()
	go func() {
		defer close(w.done)
		stop := context.AfterFunc(parent, w.cancel)
		defer stop()
		// Stop closes the interval, while its already accepted original archive
		// finishes before the Server releases the reader's other resources.
		_ = w.CheckNow(context.WithoutCancel(w.ctx))
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-w.ctx.Done():
				return
			case <-ticker.C:
				_ = w.CheckNow(context.WithoutCancel(w.ctx))
			}
		}
	}()
}

// Concurrent polls share the same physical scan, just as the original Promise.
func (w *Watcher) CheckNow(ctx context.Context) error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return errors.New("Team watcher is closed")
	}
	if pending := w.flight; pending != nil {
		w.mu.Unlock()
		select {
		case <-pending:
			return nil
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	if err := context.Cause(ctx); err != nil {
		w.mu.Unlock()
		return err
	}
	done := make(chan struct{})
	w.flight = done
	w.mu.Unlock()
	defer func() { w.mu.Lock(); w.flight = nil; close(done); w.mu.Unlock() }()
	w.check(ctx)
	return context.Cause(ctx)
}

// Close cancels the interval and waits for the currently accepted scan/archive.
func (w *Watcher) Close(ctx context.Context) error {
	w.mu.Lock()
	w.closed = true
	w.cancel()
	started, pending, done := w.started, w.flight, w.done
	w.mu.Unlock()
	if pending != nil {
		select {
		case <-pending:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	if started {
		select {
		case <-done:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	return nil
}

func (w *Watcher) event(kind, name string, identity protocol.JSONObject, lead string, fields ...protocol.JSONField) {
	message := protocol.NewJSONObject(field("type", kind), field("teamName", name))
	for _, name := range []string{"teamName", "createdAt", "leadSessionId", "incarnationId"} {
		if value := identity.Get(name); value != nil {
			message.Set(name, value)
		}
	}
	for _, f := range fields {
		message.Set(f.Name, f.Value)
	}
	w.emit(message, lead)
}

func (w *Watcher) deleted(ctx context.Context, name string, forget bool) {
	id := w.identities[name]
	lead := id.String("leadSessionId")
	if lead == "" {
		lead = w.leads[name]
	}
	canonical := id.String("teamName")
	if canonical == "" {
		canonical = name
	}
	_ = w.reader.MarkArchiveDeleted(ctx, canonical, lead, id.String("incarnationId"))
	w.event("team_deleted", name, id, lead)
	if forget {
		delete(w.snapshots, name)
		delete(w.fingerprints, name)
		delete(w.leads, name)
		delete(w.identities, name)
	}
}

func (w *Watcher) check(ctx context.Context) {
	dir := filepath.Join(w.reader.options.ConfigDir, "teams")
	entries, err := nativeDirectoryEntries(dir)
	if err != nil {
		for _, name := range w.order {
			w.deleted(ctx, name, false)
		}
		clear(w.plans)
		clear(w.snapshots)
		clear(w.fingerprints)
		clear(w.leads)
		clear(w.identities)
		w.order = nil
		return
	}
	current := map[string]bool{}
	for _, entry := range entries {
		if context.Cause(ctx) != nil {
			return
		}
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		current[name] = true
		bytes, err := os.ReadFile(filepath.Join(dir, name, "config.json"))
		if err != nil {
			continue
		}
		content := string(protocol.DecodeJavaScriptUTF8(bytes))
		config := obj([]byte(content))
		if data, e := os.ReadFile(filepath.Join(dir, name, "plan.json")); e == nil {
			if plan, e := teamplan.ParseRecord(data); e == nil && plan.String("sessionId") == config.String("leadSessionId") && plan.String("incarnationId") == watchPlanIncarnation(config) {
				fingerprint := plan.String("planId") + ":" + string(plan.Get("revision")) + ":" + plan.String("state")
				if w.plans[name] != fingerprint {
					w.plans[name] = fingerprint
					w.event("team_plan_updated", name, protocol.NewJSONObject(), plan.String("sessionId"), field("sessionId", plan.String("sessionId")), field("planId", plan.String("planId")), field("incarnationId", plan.String("incarnationId")), protocol.JSONField{Name: "revision", Value: plan.Get("revision")}, field("state", plan.String("state")))
				}
			}
		}
		previousContent, seen := w.snapshots[name]
		identity, valid := watchIdentity(name, config)
		previous := w.identities[name]
		lead := identity.String("leadSessionId")
		if lead == "" {
			lead = previous.String("leadSessionId")
		}
		if lead == "" {
			lead = w.leads[name]
		}
		fingerprint := watchDirectoryHash(w.reader.options.ConfigDir, name, content)
		if !seen {
			w.order = append(w.order, name)
			w.snapshots[name] = content
			w.fingerprints[name] = fingerprint
			if lead != "" {
				w.leads[name] = lead
			}
			if valid {
				w.identities[name] = identity
			}
			w.event("team_created", name, identity, lead)
			_, _ = w.reader.GetWorkbench(ctx, name)
		} else if content != previousContent {
			if previous.IsObject() && valid && previous.String("incarnationId") != identity.String("incarnationId") {
				w.deleted(ctx, name, false)
				w.snapshots[name] = content
				w.fingerprints[name] = fingerprint
				w.identities[name] = identity
				if identity.String("leadSessionId") != "" {
					w.leads[name] = identity.String("leadSessionId")
				} else {
					delete(w.leads, name)
				}
				w.event("team_created", name, identity, identity.String("leadSessionId"))
				_, _ = w.reader.GetWorkbench(ctx, name)
				continue
			}
			w.snapshots[name] = content
			members, e := watchMembers(config)
			// JSON primitives except null have an undefined members property in
			// Source. A truncated document still follows partial recovery.
			if !config.IsObject() && json.Valid([]byte(content)) && string(protocol.StringifyValidatedJavaScriptJSON([]byte(content))) != "null" {
				members, e = []protocol.JSONObject{}, nil
			}
			if e == nil {
				if lead != "" {
					w.leads[name] = lead
				}
				if valid {
					w.identities[name] = identity
				}
				members = append(members, w.watchDiscoveredMembers(name, config)...)
				w.event("team_update", name, identity, lead, field("members", members))
			} else if recovered := watchPartialMembers(content); len(recovered) > 0 {
				w.event("team_update", name, previous, lead, field("members", recovered))
			}
		}
		if seen && w.fingerprints[name] != fingerprint {
			w.fingerprints[name] = fingerprint
			_, _ = w.reader.GetWorkbench(ctx, name)
			w.event("team_workbench_updated", name, identity, lead)
		}
	}
	retained := []string{}
	for _, name := range w.order {
		if !current[name] {
			w.deleted(ctx, name, true)
		} else {
			retained = append(retained, name)
		}
	}
	w.order = retained
}

func watchPlanIncarnation(config protocol.JSONObject) string {
	lead := config.Get("leadSessionId")
	if lead == nil || string(lead) == "null" {
		lead = raw("")
	}
	return hash(protocol.StringifyValidatedJavaScriptJSON(raw([]json.RawMessage{config.Get("name"), lead, config.Get("createdAt")})))
}
