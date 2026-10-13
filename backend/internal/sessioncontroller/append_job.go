package sessioncontroller

import (
	"context"
	"errors"
	"io/fs"
	"os"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
)

// BeginAppend ports the current-session Project.appendEntry/enqueueWrite path.
// It retains the original entry pointer until the existing lazy drain encodes
// it. The cache wait is untracked; only enqueueWrite starts nested tracking.
// Empty SID captures the actual current SID at admission, not at completion.
func (pending *PendingWriter) BeginAppend(scope *cooperative.Scope, loader *sessionhistory.Loader, entry *protocol.JSONObject, sid string) (*WriteFuture, error) {
	if !scope.BelongsTo(pending.owner) {
		return nil, cooperative.ErrRestricted
	}
	if loader == nil || entry == nil {
		return nil, errors.New("append requires actual history loader and original entry")
	}
	var future *WriteFuture
	err := scope.Inline(cooperative.Read, func(s *cooperative.Scope) error {
		if pending.closing {
			return ErrPendingClosed
		}
		writer := pending.writer
		writer.mu.Lock()
		current := writer.currentSessionID()
		path := writer.path
		skipped := writer.skipped()
		writer.mu.Unlock()
		if sid == "" {
			sid = current
		}
		if sid != current {
			return capability("other-session append path resolution")
		}
		if truthy(entry.Get("isSidechain")) && entry.Get("agentId") != nil || entry.String("type") == "content-replacement" && truthy(entry.Get("agentId")) {
			if writer.options.AgentID == "" || entry.String("agentId") != writer.options.AgentID {
				return capability("agent transcript append target")
			}
		}
		// The existing excluded alias is string-keyed. Admit JSON message UUIDs
		// only where missing-target/disabled persistence can preserve exclusions.
		if uuid := entry.Get("uuid"); uuid != nil {
			if _, e := stringKey(uuid); e != nil {
				return e
			}
		}
		future = newWriteFuture()
		job := &appendContinuation{pending: pending, entry: entry, sid: sid, path: path, future: future}
		if skipped {
			writer.mu.Lock()
			if entry.Get("uuid") != nil {
				writer.discard([]queuedEntry{{entry: entry}})
				writer.pending = nil
			}
			writer.mu.Unlock()
			future.settle(nil)
			return nil
		}
		if path == "" {
			writer.mu.Lock()
			writer.pending = append(writer.pending, entry)
			writer.mu.Unlock()
			future.settle(nil)
			return nil
		}
		if appendMetadata(entry.String("type")) {
			job.enqueue(s)
			return nil
		}
		cache, e := pending.SessionUUIDs(s, loader, sid)
		if e != nil {
			return e
		}
		_ = cache.future.thenOwner(pending.owner, func(s *cooperative.Scope, e error) {
			if e != nil {
				future.settle(e)
				return
			}
			job.set = cache.set
			if entry.String("type") != "queue-operation" {
				found, e := job.set.OwnerHas(s, entry.Get("uuid"))
				if e != nil {
					future.settle(e)
					return
				}
				if found {
					future.settle(nil)
					return
				}
				job.add = true
			}
			job.enqueue(s)
		}, future.settle)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return future, nil
}
func appendMetadata(kind string) bool {
	switch kind {
	case "session-meta", "summary", "custom-title", "ai-title", "last-prompt", "task-summary", "tag", "agent-name", "agent-color", "agent-setting", "pr-link", "file-history-snapshot", "attribution-snapshot", "speculation-accept", "mode", "worktree-state", "content-replacement", "marble-origami-commit", "marble-origami-snapshot":
		return true
	}
	return false
}

type appendContinuation struct {
	pending   *PendingWriter
	entry     *protocol.JSONObject
	sid, path string
	future    *WriteFuture
	set       *sessionhistory.MessageSet
	add       bool
	token     *TrackedWrite
}

// Each command receives a fresh owner scope; the worker never receives a Scope,
// JSONObject, Set, Factory or entry. Unlike model cancellation, owner lifetime
// rejection closes unadopted leases and settles the independent append future.
func dispatchAppendOwned[T any](pending *PendingWriter, future *WriteFuture, work func() T, cleanup func(T), complete func(*cooperative.Scope, T)) {
	pending.runtimeNative.begin()
	pending.workers.Add(1)
	go func() {
		defer pending.workers.Done()
		defer pending.runtimeNative.end()
		result := work()
		receipt, err := pending.owner.SubmitIndependent(context.Background(), cooperative.Flush, func(s *cooperative.Scope) error { complete(s, result); return nil })
		if err == nil {
			err = receipt.Wait()
		}
		if err != nil {
			if cleanup != nil {
				cleanup(result)
			}
			future.settle(err)
		}
	}()
}
func (job *appendContinuation) enqueue(s *cooperative.Scope) {
	token, e := job.pending.BeginTrackedWrite(s)
	if e != nil {
		job.future.settle(e)
		return
	}
	job.token = token
	writer := job.pending.writer
	writer.mu.Lock()
	id, known := writer.identities[job.path]
	files := writer.options.Files
	writer.mu.Unlock()
	if known {
		job.queued(s, id)
		return
	}
	path := job.path
	dispatchAppendOwned(job.pending, job.future, func() openResult {
		file, e := files.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
		if e != nil {
			return openResult{err: e}
		}
		return openResult{lease: &descriptorLease{file: file}}
	}, func(v openResult) {
		if v.lease != nil {
			_ = v.lease.close()
		}
	}, func(s *cooperative.Scope, v openResult) {
		if errors.Is(v.err, fs.ErrNotExist) {
			writer.mu.Lock()
			writer.discard([]queuedEntry{{entry: job.entry}})
			writer.mu.Unlock()
			job.complete(s, nil)
			return
		}
		if v.err != nil {
			job.complete(s, v.err)
			return
		}
		job.stat(v.lease)
	})
}
func (job *appendContinuation) stat(lease *descriptorLease) {
	dispatchAppendOwned(job.pending, job.future, func() statResult { info, e := lease.file.Stat(); return statResult{info, e} }, func(statResult) { _ = lease.close() }, func(s *cooperative.Scope, v statResult) {
		err := v.err
		var id identity
		if err == nil && !v.info.Mode().IsRegular() {
			err = errors.New("Refusing non-regular transcript target: " + job.path)
		}
		if err == nil {
			id, err = fileIdentity(v.info)
			if err == nil {
				job.pending.writer.mu.Lock()
				job.pending.writer.identities[job.path] = id
				job.pending.writer.mu.Unlock()
			}
		}
		dispatchAppendOwned(job.pending, job.future, func() error { return lease.close() }, nil, func(s *cooperative.Scope, closed error) {
			if closed != nil {
				err = closed
			}
			if err != nil {
				job.complete(s, err)
				return
			}
			job.queued(s, id)
		})
	})
}
func (job *appendContinuation) queued(s *cooperative.Scope, id identity) {
	writer := job.pending.writer
	writer.mu.Lock()
	writer.queues[job.path] = append(writer.queues[job.path], queuedEntry{job.entry, id})
	if writer.stop == nil {
		stop, err := job.pending.scheduleDrainReference(s)
		if err != nil {
			writer.mu.Unlock()
			job.complete(s, err)
			return
		}
		writer.stop = stop
	}
	writer.mu.Unlock()
	// The async enqueue body has returned a fulfilled Promise. Source's
	// trackWrite finally runs after awaiting it, never in the admission stack.
	// This is an explicit owner continuation, not a timer or a business worker.
	future := job.future
	receipt, err := job.pending.owner.Microtask(cooperative.Flush, func(next *cooperative.Scope) error { job.complete(next, nil); return nil })
	if err != nil {
		future.settle(err)
		return
	}
	job.pending.workers.Add(1)
	go func() {
		defer job.pending.workers.Done()
		if err := receipt.Wait(); err != nil {
			future.settle(err)
		}
	}()
}
func (job *appendContinuation) complete(s *cooperative.Scope, err error) {
	if completeErr := job.token.Complete(s); err == nil {
		err = completeErr
	}
	if err == nil && job.add {
		err = job.set.Add(s, job.entry.Get("uuid"))
	}
	if err == nil && job.persistRemote(s) {
		return
	}
	job.future.settle(err)
}
