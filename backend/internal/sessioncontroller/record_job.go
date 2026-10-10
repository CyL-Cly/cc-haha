package sessioncontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/gitbranch"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

// RecordBranchFuture observes the actual shared Git service. The service is
// admitted on this same owner; a worker may only wait for its immutable result.
type RecordBranchFuture interface {
	Done() <-chan struct{}
	Wait(context.Context) (string, error)
}

type RecordOptions struct {
	// AllMessages is Source's full original session for incremental REPL-ID cleaning.
	// Nil defaults to the recorded slice; an explicit empty slice stays empty.
	AllMessages []LiveRecord

	CWDContext          *bootstrapstate.CWDContext
	PromptID            func() json.RawMessage
	StartingParentUUID  json.RawMessage
	TeamName, AgentName json.RawMessage
	Branch              func(*cooperative.Scope) (RecordBranchFuture, error)
}

// RecordFuture is deliberately independent of Project's tracked-write count.
// A caller stopping its observation does not cancel admitted persistence.
type RecordFuture struct {
	future *WriteFuture
	owner  *cooperative.Owner
	uuid   json.RawMessage
}

func (future *RecordFuture) Done() <-chan struct{} { return future.future.Done() }
func (future *RecordFuture) Wait(ctx context.Context) (json.RawMessage, error) {
	if err := future.future.Wait(ctx); err != nil {
		return nil, err
	}
	return append(json.RawMessage(nil), future.uuid...), nil
}
func (future *RecordFuture) Await(scope *cooperative.Scope) (json.RawMessage, error) {
	if !scope.BelongsTo(future.owner) {
		return nil, cooperative.ErrRestricted
	}
	if err := future.future.Await(scope); err != nil {
		return nil, err
	}
	return append(json.RawMessage(nil), future.uuid...), nil
}

type recordOriginal struct {
	record *protocol.JSONObject
	live   *streamhistory.Envelope
}
type recordContinuation struct {
	pending                   *PendingWriter
	loader                    *sessionhistory.Loader
	options                   RecordOptions
	future                    *RecordFuture
	cleaned, added, inserting []recordOriginal
	set                       *sessionhistory.MessageSet
	sid                       string
	parent, prefix            json.RawMessage
	token                     *TrackedWrite
	real                      bool
	buffered                  []*protocol.JSONObject
	index                     int
	branch                    string
	branchError               error
	stampSID                  string
	slug                      json.RawMessage
}

// exclusions is called only with Writer.mu held on the canonical owner. It is
// Source's separate per-session exclusion map, not a copy of MessageSet.
func (writer *Writer) exclusions(sid string) map[string]bool {
	if writer.excludedSessions == nil {
		writer.excludedSessions = map[string]map[string]bool{writer.currentSessionID(): writer.excluded}
	}
	set := writer.excludedSessions[sid]
	if set == nil {
		set = map[string]bool{}
		writer.excludedSessions[sid] = set
	}
	return set
}
func (writer *Writer) excludedFor(sid string, uuid json.RawMessage) (bool, error) {
	key, err := stringKey(uuid)
	if err != nil {
		return false, err
	}
	return writer.exclusions(sid)[key], nil
}
func (writer *Writer) filterRecord(values []recordOriginal, sid string) ([]recordOriginal, error) {
	kept := make([]recordOriginal, 0, len(values))
	skipped := writer.skipped()
	for _, value := range values {
		key, err := stringKey(value.record.Get("uuid"))
		if err != nil {
			return nil, err
		}
		if skipped {
			writer.exclusions(sid)[key] = true
		} else if !writer.exclusions(sid)[key] {
			kept = append(kept, value)
		}
	}
	if skipped {
		writer.pending = nil
		writer.lastPrompt = nil
	}
	return kept, nil
}

func (pending *PendingWriter) BeginRecordLive(scope *cooperative.Scope, loader *sessionhistory.Loader, profile LiveRecordProfile, messages []LiveRecord, options RecordOptions) (*RecordFuture, error) {
	if !scope.BelongsTo(pending.owner) {
		return nil, cooperative.ErrRestricted
	}
	if pending.writer.options.Bootstrap != nil {
		state := pending.writer.options.Bootstrap
		if err := state.CheckScope(scope); err != nil {
			return nil, err
		}
		if options.CWDContext == nil {
			var err error
			options.CWDContext, err = state.Context(scope)
			if err != nil {
				return nil, err
			}
		}
		if !options.CWDContext.Owns(state) {
			return nil, cooperative.ErrRestricted
		}
	}
	if loader == nil || options.PromptID == nil {
		return nil, errors.New("record requires the original loader and live prompt getter")
	}
	if profile.QuerySource != "sdk" && !(pending.writer.options.AgentID != "" && (profile.QuerySource == "agent:custom" || strings.HasPrefix(profile.QuerySource, "agent:builtin:") || profile.QuerySource == "workflow_agent")) || profile.ActiveHooks || profile.DynamicSDKQueue || profile.ResultOrAccounting {
		return nil, capability("record SDK host profile")
	}
	var future *RecordFuture
	err := scope.Inline(cooperative.Read, func(scope *cooperative.Scope) error {
		if pending.closing {
			return ErrPendingClosed
		}
		future = &RecordFuture{future: newWriteFuture(), owner: pending.owner}
		cleaned, err := cleanRecordForEnvironment(messages, options.AllMessages, pending.writer.options.Environment)
		if err != nil {
			var nativeTypeError *LoggingTypeError
			if errors.As(err, &nativeTypeError) {
				// recordTranscript is async even when cleaning throws before its
				// first await: return its rejected promise, preserving the error.
				future.future.settle(err)
				return nil
			}
			future = nil
			return err
		}
		pending.writer.mu.Lock()
		cleaned, err = pending.writer.filterRecord(cleaned, pending.writer.currentSessionID())
		sid := pending.writer.currentSessionID()
		pending.writer.mu.Unlock()
		if err != nil {
			return err
		}
		job := &recordContinuation{pending: pending, loader: loader, options: options, future: future, cleaned: cleaned, sid: sid}
		if len(cleaned) == 0 {
			job.finish(scope, nil)
			return nil
		}
		cache, err := pending.SessionUUIDs(scope, loader, sid)
		if err != nil {
			job.finish(scope, err)
			return nil
		}
		job.wait(cache.future, func(scope *cooperative.Scope) { job.set = cache.set; job.discard(scope) })
		return nil
	})
	return future, err
}

// dispatchRecord has the same pure-I/O boundary as dispatch, but obtains the
// completion Scope from the native Promise reaction. Never retain the ASK scope.
func dispatchRecord[T any](pending *PendingWriter, failure *WriteFuture, work func() T, complete func(*cooperative.Scope, T)) error {
	if _, err := pending.admitRecordWorker(true); err != nil {
		return err
	}
	go func() {
		defer pending.workers.Done()
		defer pending.runtimeNative.end()
		result := work()
		receipt, err := pending.owner.Microtask(cooperative.Flush, func(scope *cooperative.Scope) error { complete(scope, result); return nil })
		if err == nil {
			err = receipt.Wait()
		}
		if err != nil {
			failure.settle(err)
		}
	}()
	return nil
}
func (job *recordContinuation) wait(waiting *WriteFuture, complete func(*cooperative.Scope)) {
	_ = waiting.thenOwner(job.pending.owner, func(scope *cooperative.Scope, err error) {
		if err != nil {
			job.finish(scope, err)
			return
		}
		complete(scope)
	}, job.future.future.settle)
}
func (job *recordContinuation) finish(scope *cooperative.Scope, err error) {
	if job.token != nil {
		if completed := job.token.Complete(scope); completed != nil && err == nil {
			err = completed
		}
		job.token = nil
	}
	if err == nil {
		result := job.prefix
		writer := job.pending.writer
		writer.mu.Lock()
		for i := len(job.added) - 1; i >= 0; i-- {
			value := job.added[i].record
			excluded, e := writer.excludedFor(writer.currentSessionID(), value.Get("uuid"))
			if e != nil {
				err = e
				break
			}
			if value.String("type") != "progress" && !excluded {
				result = value.Get("uuid")
				break
			}
		}
		writer.mu.Unlock()
		if result == nil {
			result = json.RawMessage("null")
		}
		job.future.uuid = append(json.RawMessage(nil), result...)
	}
	job.future.future.settle(err)
}

func (job *recordContinuation) discard(scope *cooperative.Scope) {
	keys, err := job.set.OwnerKeys(scope)
	if err != nil {
		job.finish(scope, err)
		return
	}
	writer := job.pending.writer
	writer.mu.Lock()
	path := writer.path
	if path == "" {
		path = filepath.Join(filepath.Dir(writer.target()), job.sid+".jsonl")
		if writer.options.AgentID != "" {
			path = writer.target()
		}
	}
	observe := len(keys) > 0 || len(writer.queues[path]) > 0
	files := writer.options.Files
	writer.mu.Unlock()
	// Even the no-stat async discard returns through a later owner callback.
	if !observe {
		ready := newWriteFuture()
		ready.settle(nil)
		job.wait(ready, job.selectNew)
		return
	}
	dispatchErr := dispatchRecord(job.pending, job.future.future, func() statResult { info, err := files.Stat(path); return statResult{info, err} }, func(scope *cooperative.Scope, result statResult) {
		writer.mu.Lock()
		replaced := errors.Is(result.err, fs.ErrNotExist)
		if result.err == nil {
			id, idErr := fileIdentity(result.info)
			if idErr != nil {
				writer.mu.Unlock()
				job.finish(scope, idErr)
				return
			}
			previous, exists := writer.identities[path]
			replaced = exists && previous != id
		}
		if replaced {
			// Use keys at stat completion: concurrent records mutate this same Set.
			current, e := job.set.OwnerKeys(scope)
			if e != nil {
				writer.mu.Unlock()
				job.finish(scope, e)
				return
			}
			for _, key := range current {
				name, e := stringKey(key.Raw())
				if e != nil {
					writer.mu.Unlock()
					job.finish(scope, e)
					return
				}
				writer.exclusions(job.sid)[name] = true
			}
			if e := job.set.Clear(scope); e != nil {
				writer.mu.Unlock()
				job.finish(scope, e)
				return
			}
			writer.discard(writer.queues[path])
			writer.queues[path] = nil
			writer.pending = nil
			writer.lastPrompt = nil
		}
		writer.mu.Unlock()
		// stat resumes discard's body, not recordTranscript's caller. Source
		// awaits the async discard return separately, so queued promise reactions
		// can still change the original messages before UUID selection.
		ready := newWriteFuture()
		ready.settle(nil)
		job.wait(ready, job.selectNew)
	})
	if dispatchErr != nil {
		job.finish(scope, dispatchErr)
	}
}

func (job *recordContinuation) selectPrefix(scope *cooperative.Scope) error {
	writer := job.pending.writer
	job.parent = json.RawMessage("null")
	hint := job.options.StartingParentUUID
	if truthy(hint) {
		excluded, err := writer.excludedFor(writer.currentSessionID(), hint)
		if err != nil {
			return err
		}
		found, err := job.set.OwnerHas(scope, hint)
		if err != nil {
			return err
		}
		if !excluded && found {
			job.parent = hint
		}
	}
	seenNew := false
	for _, value := range job.cleaned {
		uuid := value.record.Get("uuid")
		excluded, err := writer.excludedFor(writer.currentSessionID(), uuid)
		if err != nil {
			return err
		}
		if excluded {
			continue
		}
		found, err := job.set.OwnerHas(scope, uuid)
		if err != nil {
			return err
		}
		if found {
			if !seenNew && value.record.String("type") != "progress" {
				job.parent = uuid
			}
		} else {
			seenNew = true
			job.added = append(job.added, value)
		}
	}
	job.prefix = job.parent
	return nil
}
func (job *recordContinuation) selectNew(scope *cooperative.Scope) {
	writer := job.pending.writer
	writer.mu.Lock()
	err := job.selectPrefix(scope)
	if err == nil && len(job.added) > 0 {
		job.inserting, err = writer.filterRecord(job.added, writer.currentSessionID())
	}
	writer.mu.Unlock()
	if err != nil {
		job.finish(scope, err)
		return
	}
	if len(job.added) == 0 {
		job.finish(scope, nil)
		return
	}
	job.token, err = job.pending.BeginTrackedWrite(scope)
	if err != nil {
		job.finish(scope, err)
		return
	}
	if len(job.inserting) == 0 {
		job.finish(scope, nil)
		return
	}
	writer.mu.Lock()
	if truthy(job.parent) {
		var excluded bool
		excluded, err = writer.excludedFor(writer.currentSessionID(), job.parent)
		if excluded {
			job.parent = json.RawMessage("null")
		}
	} else {
		job.parent = json.RawMessage("null")
	}
	for _, value := range job.inserting {
		kind := value.record.String("type")
		job.real = job.real || kind == "user" || kind == "assistant"
	}
	materialize := err == nil && writer.path == "" && job.real && !writer.skipped()
	if materialize {
		writer.path = writer.target()
	}
	path := writer.path
	writer.mu.Unlock()
	if err != nil {
		job.finish(scope, err)
		return
	}
	if materialize {
		prepare, e := job.pending.Prepare(scope, path)
		if e != nil {
			job.finish(scope, e)
		} else {
			job.wait(prepare.future, job.afterMaterialize)
		}
	} else {
		job.prepareAgain(scope)
	}
}
func (job *recordContinuation) afterMaterialize(scope *cooperative.Scope) {
	writer := job.pending.writer
	writer.mu.Lock()
	err := writer.refreshMetadata(true)
	if err == nil {
		job.buffered = writer.pending
		writer.pending = nil
	}
	writer.mu.Unlock()
	if err != nil {
		job.finish(scope, err)
		return
	}
	job.appendBuffered(scope)
}
func (job *recordContinuation) appendBuffered(scope *cooperative.Scope) {
	if len(job.buffered) == 0 {
		job.prepareAgain(scope)
		return
	}
	entry := job.buffered[0]
	job.buffered = job.buffered[1:]
	appendFuture, err := job.pending.BeginAppend(scope, job.loader, entry, "")
	if err != nil {
		job.finish(scope, err)
		return
	}
	job.wait(appendFuture, job.appendBuffered)
}
func (job *recordContinuation) prepareAgain(scope *cooperative.Scope) {
	writer := job.pending.writer
	writer.mu.Lock()
	path := writer.path
	writer.mu.Unlock()
	if !job.real || path == "" {
		job.getBranch(scope)
		return
	}
	prepare, err := job.pending.Prepare(scope, path)
	if err != nil {
		job.finish(scope, err)
		return
	}
	job.wait(prepare.future, job.getBranch)
}

type resolvedRecordBranch struct {
	done   chan struct{}
	branch string
	err    error
}

func (future *resolvedRecordBranch) Done() <-chan struct{} { return future.done }
func (future *resolvedRecordBranch) Wait(context.Context) (string, error) {
	return future.branch, future.err
}

type recordBranchResult struct {
	value string
	err   error
}

func (job *recordContinuation) getBranch(scope *cooperative.Scope) {
	if err := job.pending.recordAdmissionError(); err != nil {
		job.finish(scope, err)
		return
	}
	var future RecordBranchFuture
	var err error
	if job.options.Branch != nil {
		future, err = job.options.Branch(scope)
	} else {
		branch, e := job.pending.writer.options.Branch()
		ready := make(chan struct{})
		close(ready)
		future = &resolvedRecordBranch{ready, branch, e}
	}
	if err != nil {
		job.finish(scope, err)
		return
	}
	if future == nil {
		job.finish(scope, errors.New("Git service returned no branch future"))
		return
	}
	// An actual Git owner Promise is observed through its own reaction, including
	// hot cache returns. Done/Wait below is only the legacy external observer seam.
	if owned, ok := future.(interface {
		ThenOwner(*cooperative.Scope, func(*cooperative.Scope, string, error), func(error)) error
	}); ok {
		err := owned.ThenOwner(scope, func(next *cooperative.Scope, value string, err error) {
			job.afterBranch(next, recordBranchResult{value: value, err: err})
		}, job.future.future.settle)
		if err != nil {
			job.finish(scope, err)
		}
		return
	}
	// Git's Promise settlement is distinct from native I/O readiness. Already
	// fulfilled values keep their await reaction; a cold observer transfers
	// only the immutable value/error and never receives an owner Scope.
	waiting := newWriteFuture()
	var result recordBranchResult
	settled := func() {
		result.value, result.err = future.Wait(context.Background())
		waiting.settle(nil)
	}
	select {
	case <-future.Done():
		settled()
	default:
		done := job.pending.owner.Done()
		stopped, err := job.pending.admitRecordWorker(false)
		if err != nil {
			job.finish(scope, err)
			return
		}
		go func() {
			defer job.pending.workers.Done()
			select {
			case <-future.Done():
				settled()
			case <-stopped:
				// An already settled immutable branch keeps its actual rejection;
				// Close only stops waiting for a still-unresolved pure promise.
				select {
				case <-future.Done():
					settled()
				default:
					waiting.settle(ErrPendingClosed)
				}
			case <-done:
				waiting.settle(cooperative.ErrClosed)
			}
		}()
	}
	_ = waiting.thenOwner(job.pending.owner, func(scope *cooperative.Scope, err error) {
		if err != nil {
			job.finish(scope, err)
			return
		}
		job.afterBranch(scope, result)
	}, job.future.future.settle)
}
func (job *recordContinuation) afterBranch(scope *cooperative.Scope, result recordBranchResult) {
	var unavailable *CapabilityError
	var gitUnavailable *gitbranch.CapabilityError
	if errors.As(result.err, &unavailable) || errors.As(result.err, &gitUnavailable) || errors.Is(result.err, cooperative.ErrClosed) || errors.Is(result.err, cooperative.ErrRestricted) || errors.Is(result.err, context.Canceled) || errors.Is(result.err, context.DeadlineExceeded) {
		job.finish(scope, result.err)
		return
	}
	if err := job.pending.recordAdmissionError(); err != nil {
		job.finish(scope, err)
		return
	}
	job.branch = result.value
	job.branchError = result.err
	writer := job.pending.writer
	writer.mu.Lock()
	job.stampSID = writer.currentSessionID()
	job.slug = writer.metadata.Get("slug")
	if state := writer.options.Bootstrap; state != nil {
		cache, err := state.PlanSlugCache(scope)
		if err != nil {
			writer.mu.Unlock()
			job.finish(scope, err)
			return
		}
		slug, exists, err := cache.Get(scope, job.stampSID)
		if err != nil {
			writer.mu.Unlock()
			job.finish(scope, err)
			return
		}
		job.slug = nil
		if exists {
			job.slug = quote(slug)
		}
	}
	writer.mu.Unlock()
	job.appendNext(scope)
}

func (job *recordContinuation) appendNext(scope *cooperative.Scope) {
	writer := job.pending.writer
	for job.index < len(job.inserting) {
		value := job.inserting[job.index]
		job.index++
		writer.mu.Lock()
		filtered, err := writer.filterRecord([]recordOriginal{value}, writer.currentSessionID())
		if err != nil {
			writer.mu.Unlock()
			job.finish(scope, err)
			return
		}
		if len(filtered) == 0 {
			writer.mu.Unlock()
			continue
		}
		parent := job.parent
		if value.record.String("type") == "user" && truthy(value.record.Get("sourceToolAssistantUUID")) {
			id := value.record.Get("sourceToolAssistantUUID")
			excluded, e := writer.excludedFor(writer.currentSessionID(), id)
			if e != nil {
				writer.mu.Unlock()
				job.finish(scope, e)
				return
			}
			if !excluded {
				parent = id
			}
		}
		entry := protocol.NewJSONObject(protocol.JSONField{Name: "parentUuid", Value: parent}, protocol.JSONField{Name: "logicalParentUuid"}, protocol.JSONField{Name: "isSidechain", Value: raw(false)}, protocol.JSONField{Name: "teamName", Value: job.options.TeamName}, protocol.JSONField{Name: "agentName", Value: job.options.AgentName}, protocol.JSONField{Name: "promptId"}, protocol.JSONField{Name: "agentId"})
		if writer.options.AgentID != "" {
			entry.Set("isSidechain", raw(true))
			entry.Set("agentId", quote(writer.options.AgentID))
		}
		if value.record.String("type") == "system" && value.record.String("subtype") == "compact_boundary" {
			entry.Set("parentUuid", json.RawMessage("null"))
			entry.Set("logicalParentUuid", job.parent)
		}
		if value.record.String("type") == "user" {
			entry.Set("promptId", job.options.PromptID())
		}
		// This is Source's actual spread AFTER cache/prepare/Git awaits.
		entry.Assign(*value.record)
		entry.Set("userType", quote("external"))
		entry.Set("entrypoint", nil)
		if name := transcriptEntrypoint(writer.options.Environment); name != "" {
			entry.Set("entrypoint", quote(name))
		}
		cwd := writer.options.CWD
		if job.options.CWDContext != nil {
			var err error
			cwd, err = job.options.CWDContext.GetCWD(scope)
			if err != nil {
				writer.mu.Unlock()
				job.finish(scope, err)
				return
			}
		}
		entry.Set("cwd", quote(cwd))
		entry.Set("sessionId", quote(job.stampSID))
		entry.Set("version", quote(writer.options.Version))
		entry.Set("gitBranch", nil)
		if job.branchError == nil {
			entry.Set("gitBranch", quote(job.branch))
		}
		entry.Set("slug", job.slug)
		if value.live != nil {
			writer.liveEntries[&entry] = value.live
		}
		writer.mu.Unlock()
		future, err := job.pending.BeginAppend(scope, job.loader, &entry, "")
		if err != nil {
			writer.mu.Lock()
			delete(writer.liveEntries, &entry)
			writer.mu.Unlock()
			job.finish(scope, err)
			return
		}
		job.wait(future, func(scope *cooperative.Scope) {
			writer.mu.Lock()
			excluded, e := writer.excludedFor(writer.currentSessionID(), value.record.Get("uuid"))
			if !excluded && e == nil && value.record.String("type") != "progress" {
				job.parent = value.record.Get("uuid")
			}
			writer.mu.Unlock()
			if e != nil {
				job.finish(scope, e)
				return
			}
			job.appendNext(scope)
		})
		return
	}
	writer.mu.Lock()
	filtered, err := writer.filterRecord(job.inserting, writer.currentSessionID())
	if err == nil && !writer.skipped() {
		writer.updateRecordPrompt(filtered)
	}
	writer.mu.Unlock()
	job.finish(scope, err)
}
func (writer *Writer) updateRecordPrompt(values []recordOriginal) {
	for _, value := range values {
		message := value.record
		if message.String("type") != "user" || truthy(message.Get("isMeta")) || truthy(message.Get("isCompactSummary")) {
			continue
		}
		content := object(message.Get("message")).Get("content")
		var units []uint16
		if data := bytes.TrimSpace(content); len(data) > 0 && data[0] == '"' {
			units, _ = protocol.DecodeJavaScriptJSONString(content)
		} else {
			var blocks []protocol.JSONObject
			_ = json.Unmarshal(content, &blocks)
			for _, block := range blocks {
				if block.String("type") == "text" && truthy(block.Get("text")) {
					units, _ = protocol.DecodeJavaScriptJSONString(block.Get("text"))
					break
				}
			}
		}
		if len(units) == 0 {
			continue
		}
		for i, unit := range units {
			if unit == '\n' {
				units[i] = ' '
			}
		}
		units = trimUnits(units)
		if len(units) > 200 {
			units = append(trimUnits(units[:200]), '…')
		}
		writer.lastPrompt = protocol.QuoteJavaScriptUTF16(units)
		return
	}
}
