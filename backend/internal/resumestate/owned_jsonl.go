package resumestate

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
)

// Explicit JSONL resume takes the original file selector branch. It does not
// copy SID metadata, plans, backups, or prime the SID writer cache.
func (s *Service) beginOwnedJSONL(scope *cooperative.Scope, path, resumeAt string) (*ownerasync.OwnerFuture[*Result], error) {
	out, e := ownerasync.NewOwnerFuture[*Result](s.owned.state.Owner())
	if e != nil {
		return nil, e
	}
	fail := func(next *cooperative.Scope, failure error) {
		s.options.OnError(failure)
		resolveOwned(out, next, (*Result)(nil), failure)
	}
	read, e := s.beginSIDRead(scope, path)
	if e != nil {
		fail(scope, e)
		return out, nil
	}
	_, e = read.Then(scope, func(next *cooperative.Scope, transcript *sessionhistory.Transcript, failure error) error {
		if failure != nil {
			fail(next, failure)
			return nil
		}
		loaded, failure := s.options.Loader.SelectJSONLMessages(transcript)
		if failure != nil {
			fail(next, failure)
			return nil
		}
		s.finishOwnedMessages(next, loaded.Messages, protocol.NewJSONObject(), nil, loaded.SessionID, out, fail, resumeAt)
		return nil
	})
	if e != nil {
		fail(scope, e)
	}
	return out, nil
}
