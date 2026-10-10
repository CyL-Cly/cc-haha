package sessioncontroller

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
)

// FileHistorySink is a factory-produced view of the existing real Writer and
// loader. No callback may replace append, tracking, cache or native liveness.
type FileHistorySink struct {
	pending *PendingWriter
	loader  *sessionhistory.Loader
	binding *ownerasync.NativeBinding
}

func (p *PendingWriter) NativeCopyBinding() *ownerasync.NativeBinding {
	if p == nil || p.writer == nil || p.owner == nil {
		return nil
	}
	b, e := p.runtimeNative.refs.Bind(p.owner)
	if e != nil {
		return nil
	}
	return b
}
func NewFileHistorySink(p *PendingWriter, loader *sessionhistory.Loader) (*FileHistorySink, error) {
	if p == nil || p.writer == nil || loader == nil {
		return nil, cooperative.ErrRestricted
	}
	b := p.NativeCopyBinding()
	if b == nil {
		return nil, cooperative.ErrRestricted
	}
	return &FileHistorySink{p, loader, b}, nil
}
func (sink *FileHistorySink) NativeCopyBinding() *ownerasync.NativeBinding {
	if sink == nil {
		return nil
	}
	return sink.binding
}

// BeginFileHistorySnapshot starts the actual insert's outer trackWrite before
// constructing an entry/entering append. Completion preserves the source await
// and finally, async insert return adoption, then exported record's own await.
func (sink *FileHistorySink) BeginFileHistorySnapshot(scope *cooperative.Scope, messageID json.RawMessage, snapshot *protocol.JSONObject, update bool) (*ownerasync.OwnerFuture[struct{}], error) {
	if sink == nil || sink.pending == nil || !scope.BelongsTo(sink.pending.owner) || !sink.binding.Same(sink.pending.NativeCopyBinding()) {
		return nil, cooperative.ErrRestricted
	}
	p := sink.pending
	exported, _ := ownerasync.NewOwnerFuture[struct{}](p.owner)
	insertion, _ := ownerasync.NewOwnerFuture[struct{}](p.owner)
	trackedBody, _ := ownerasync.NewOwnerFuture[struct{}](p.owner)
	err := scope.Inline(cooperative.Read, func(next *cooperative.Scope) error {
		token, e := p.BeginTrackedWrite(next)
		if e != nil {
			_ = trackedBody.Reject(next, e)
			return insertion.ResolveFrom(next, trackedBody)
		}
		// The nested snapshot is the original object. Its raw bytes are acquired
		// only by the existing lazy marshal boundary, even when initially buffered.
		entry := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("file-history-snapshot")}, protocol.JSONField{Name: "messageId", Value: messageID}, protocol.JSONField{Name: "snapshot", Value: json.RawMessage("null")}, protocol.JSONField{Name: "isSnapshotUpdate", Value: raw(update)})
		complete := func(next *cooperative.Scope, failure error) {
			if e := token.Complete(next); failure == nil {
				failure = e
			}
			if failure != nil {
				_ = trackedBody.Reject(next, failure)
			} else {
				_ = trackedBody.Resolve(next, struct{}{})
			}
		}
		if snapshot == nil || !snapshot.IsObject() {
			complete(next, capability("original file-history snapshot object"))
		} else {
			p.writer.mu.Lock()
			p.writer.liveFileHistoryEntries[&entry] = snapshot
			p.writer.mu.Unlock()
			append, e := p.BeginAppend(next, sink.loader, &entry, "")
			if e != nil {
				p.writer.mu.Lock()
				delete(p.writer.liveFileHistoryEntries, &entry)
				p.writer.mu.Unlock()
				complete(next, e)
			} else {
				// append admission may buffer the original entry or queue it. A skipped
				// entry has neither destination; release only that reference, not the
				// still-live buffered object or any enqueued write.
				_ = append.thenOwner(p.owner, func(next *cooperative.Scope, failure error) {
					if failure != nil {
						p.writer.mu.Lock()
						delete(p.writer.liveFileHistoryEntries, &entry)
						p.writer.mu.Unlock()
					}
					complete(next, failure)
				}, func(failure error) { _ = trackedBody.RejectAdministrative(failure) })
				p.writer.mu.Lock()
				retained := false
				for _, v := range p.writer.pending {
					retained = retained || v == &entry
				}
				for _, q := range p.writer.queues {
					for _, v := range q {
						retained = retained || v.entry == &entry
					}
				}
				// Active append may still be at its genuine native open/stat await.
				select {
				case <-append.Done():
					if !retained {
						delete(p.writer.liveFileHistoryEntries, &entry)
					}
				default:
				}
				p.writer.mu.Unlock()
			}
		}
		return insertion.ResolveFrom(next, trackedBody)
	})
	if err != nil {
		return nil, err
	}
	_, err = insertion.Then(scope, func(next *cooperative.Scope, _ struct{}, failure error) error {
		if failure != nil {
			return exported.Reject(next, failure)
		}
		return exported.Resolve(next, struct{}{})
	})
	return exported, err
}
