package resumestate

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"path/filepath"
	"sync"
)

type sidReadLease struct {
	file    sessionstore.File
	once    sync.Once
	failure error
}

func (l *sidReadLease) close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() { l.failure = l.file.Close() })
	return l.failure
}

type sidPhysical struct {
	result sessionhistory.PhysicalReadResult
	lease  *sidReadLease
}
type sidReadJob struct {
	service *Service
	reader  *sessionhistory.ReadContinuation
	io      sessionhistory.PhysicalIO
	lease   *sidReadLease
	out     *ownerasync.OwnerFuture[*sessionhistory.Transcript]
}

// Public SID uses the same original transcript reducer as Writer UUID loading.
// Only immutable syscall requests cross the physical native boundary; selection,
// cache priming, plan/history/hooks and restoration remain on the owning scope.
func (s *Service) beginOwnedSID(scope *cooperative.Scope, rawSID json.RawMessage) (*ownerasync.OwnerFuture[*Result], error) {
	out, e := ownerasync.NewOwnerFuture[*Result](s.owned.state.Owner())
	if e != nil {
		return nil, e
	}
	sid, valid := originalUTF8String(rawSID)
	if !valid || !uuidName.MatchString(sid) {
		resolveOwned(out, scope, (*Result)(nil), &CapabilityError{Capability: "SID selector outside original ASCII UUID profile"})
		return out, nil
	}
	read, e := s.beginSIDRead(scope, filepath.Join(s.projectDir(), sid+".jsonl"))
	if e != nil {
		resolveOwned(out, scope, (*Result)(nil), e)
		return out, nil
	}
	_, e = read.Then(scope, func(next *cooperative.Scope, transcript *sessionhistory.Transcript, failure error) error {
		if failure != nil {
			s.options.OnError(failure)
			resolveOwned(out, next, (*Result)(nil), failure)
			return nil
		}
		data := object(encode(transcript))
		items := pairs(data.Get("messages"))
		if len(items) == 0 {
			resolveOwned(out, next, (*Result)(nil), nil)
			return nil
		}
		if failure = s.owned.prime(next, sid, transcript); failure != nil {
			resolveOwned(out, next, (*Result)(nil), failure)
			return nil
		}
		s.mu.Lock()
		if _, exists := s.state.SessionUUIDs[sid]; !exists {
			s.uuidTranscripts[sid] = transcript
			s.state.SessionUUIDs[sid] = []json.RawMessage{}
			for _, p := range items {
				s.state.SessionUUIDs[sid] = append(s.state.SessionUUIDs[sid], p[0])
			}
		}
		s.mu.Unlock()
		tip, failure := s.latest(data, false)
		if failure != nil || !tip.IsObject() {
			resolveOwned(out, next, (*Result)(nil), failure)
			return nil
		}
		chain, messages, failure := s.options.Loader.BuildConversationChain(transcript, tip.Get("uuid"))
		if failure != nil {
			resolveOwned(out, next, (*Result)(nil), failure)
			return nil
		}
		fields := protocol.NewJSONObject(protocol.JSONField{Name: "messages", Value: encode(messages)}, protocol.JSONField{Name: "fullPath", Value: quote(filepath.Join(s.projectDir(), sid+".jsonl"))})
		fields.Set("agentName", object(chain[0]).Get("agentName"))
		for _, p := range [][2]string{{"customTitle", "customTitles"}, {"tag", "tags"}, {"agentSetting", "agentSettings"}, {"worktreeSession", "worktreeStates"}} {
			key := quote(sid)
			if p[0] == "customTitle" || p[0] == "tag" {
				key = tip.Get("sessionId")
			}
			v, _ := transcript.MetadataValue(p[1], key)
			fields.Set(p[0], v)
		}
		fields.Set("fileHistorySnapshots", encode(s.snapshots(data, chain)))
		fields.Set("attributionSnapshots", encode(metadataValues(data, "attributionSnapshots")))
		sessionFields(&fields, data, quote(sid))
		log, failure := DecodeOriginalLog(fields)
		if failure != nil {
			resolveOwned(out, next, (*Result)(nil), failure)
			return nil
		}
		// This graph is produced here from own native disk data. No caller-supplied
		// prepared LogOption enters the public selector.
		loaded, failure := s.BeginLoad(next, Input{Original: log})
		if failure != nil {
			resolveOwned(out, next, (*Result)(nil), failure)
			return nil
		}
		_, failure = loaded.Then(next, func(after *cooperative.Scope, r *Result, err error) error {
			resolveOwned(out, after, r, err)
			return nil
		})
		return failure
	})
	if e != nil {
		resolveOwned(out, scope, (*Result)(nil), e)
	}
	return out, nil
}
func (s *Service) beginSIDRead(scope *cooperative.Scope, path string) (*ownerasync.OwnerFuture[*sessionhistory.Transcript], error) {
	reader, e := s.options.Loader.BeginRead(path, false)
	if e != nil {
		return nil, e
	}
	out, e := ownerasync.NewOwnerFuture[*sessionhistory.Transcript](s.owned.state.Owner())
	if e != nil {
		return nil, e
	}
	job := &sidReadJob{service: s, reader: reader, io: s.options.Loader.PhysicalIO(), out: out}
	job.next(scope)
	return out, nil
}
func (j *sidReadJob) next(scope *cooperative.Scope) {
	req := j.reader.Request()
	if req.Operation == sessionhistory.ReadComplete {
		v, e := j.service.options.Loader.FinishRead(j.reader)
		resolveOwned(j.out, scope, v, e)
		return
	}
	ref, e := j.service.owned.binding.Acquire(scope)
	if e != nil {
		resolveOwned(j.out, scope, (*sessionhistory.Transcript)(nil), e)
		return
	}
	io, lease, owner := j.io, j.lease, j.service.owned.state.Owner()
	go func() {
		p := sidPhysical{lease: lease}
		switch req.Operation {
		case sessionhistory.ReadStat:
			p.result.Size, p.result.Error = io.Stat(req.Path)
		case sessionhistory.ReadWhole:
			p.result.Bytes, p.result.Error = io.ReadFile(req.Path)
		case sessionhistory.ReadOpen:
			f, e := io.Files.Open(req.Path)
			p.result.Error = e
			if e == nil {
				p.lease = &sidReadLease{file: f}
			}
		case sessionhistory.ReadAt:
			b := make([]byte, req.Length)
			n, e := lease.file.ReadAt(b, req.Offset)
			p.result.Bytes, p.result.Error = b[:n], e
		case sessionhistory.ReadClose:
			p.result.Error = lease.close()
			p.lease = nil
		}
		receipt, e := owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
			j.lease = p.lease
			if failure := j.reader.Complete(p.result); failure != nil {
				_ = j.lease.close()
				resolveOwned(j.out, next, (*sessionhistory.Transcript)(nil), failure)
			} else {
				j.next(next)
			}
			release, e := owner.Microtask(cooperative.Read, func(*cooperative.Scope) error { ref.DeliveryFinished(); return nil })
			if e == nil {
				ref.ReclaimRejectedReceipt(release)
			}
			return e
		})
		if e == nil {
			e = receipt.Wait()
		}
		if e != nil {
			_ = p.lease.close()
			_ = j.out.RejectAdministrative(e)
			ref.DeliveryFinished()
		}
	}()
}
