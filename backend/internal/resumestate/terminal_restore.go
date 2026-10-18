package resumestate

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/filehistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/filehistorycopy"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
)

// BeginTerminalPlanRestore calls the same exported copyPlanForResume producer
// without entering headless loadInitialMessages or awaiting its completion.
func (s *Service) BeginTerminalPlanRestore(scope *cooperative.Scope, log *OriginalLog, sid json.RawMessage) (*ownerasync.OwnerFuture[bool], error) {
	if err := s.checkOwned(scope); err != nil {
		return nil, err
	}
	if log == nil || log.History == nil {
		return nil, cooperative.ErrRestricted
	}
	return s.beginOwnedPlan(scope, log, sid)
}

// BeginTerminalFullLog is loadFullLog alone. It does not activate, deserialize,
// restore costs or invoke startup hooks before the REPL's resume callback.
func (s *Service) BeginTerminalFullLog(scope *cooperative.Scope, log protocol.JSONObject) (*ownerasync.OwnerFuture[protocol.JSONObject], error) {
	if err := s.checkOwned(scope); err != nil {
		return nil, err
	}
	out, err := ownerasync.NewOwnerFuture[protocol.JSONObject](s.owned.state.Owner())
	if err != nil {
		return nil, err
	}
	rows, valid := array(log.Get("messages"))
	if !valid || len(rows) != 0 || log.Get("sessionId") == nil || !truthy(log.Get("fullPath")) {
		resolveOwned(out, scope, log, nil)
		return out, nil
	}
	read, err := s.beginSIDRead(scope, log.String("fullPath"))
	if err != nil {
		return nil, err
	}
	_, err = read.Then(scope, func(next *cooperative.Scope, transcript *sessionhistory.Transcript, failure error) error {
		if failure != nil {
			resolveOwned(out, next, log, nil)
			return nil
		}
		full, failure := s.fullLogFromTranscript(log, transcript, object(encode(transcript)))
		if failure != nil {
			resolveOwned(out, next, log, nil)
			return nil
		}
		if len(pairs(object(encode(transcript)).Get("messages"))) > 0 {
			tip, e := s.latest(object(encode(transcript)), true)
			if e == nil && tip.IsObject() {
				chain, _, e := s.options.Loader.BuildConversationChain(transcript, tip.Get("uuid"))
				if e == nil {
					e = s.terminalLogPresentation(&full, chain)
				}
				if e != nil {
					resolveOwned(out, next, log, nil)
					return nil
				}
				root := object(chain[0])
				for _, name := range []string{"isSidechain", "teamName"} {
					if value := root.Get(name); value != nil && string(value) != "null" {
						full.Set(name, value)
					}
				}
				full.Set("leafUuid", tip.Get("uuid"))
				if branch := tip.Get("gitBranch"); branch != nil {
					full.Set("gitBranch", branch)
				}
			}
		}
		resolveOwned(out, next, full, nil)
		return nil
	})
	return out, err
}

func (s *Service) BeginTerminalFileHistoryRestore(scope *cooperative.Scope, log *filehistorycopy.Log, checkpoints *filehistory.Service) (*ownerasync.OwnerFuture[struct{}], error) {
	if err := s.checkOwned(scope); err != nil {
		return nil, err
	}
	return s.ownedFileHistory.BeginInteractive(scope, log, checkpoints)
}
