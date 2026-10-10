package resumestate

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/filehistorycopy"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionresume"
)

// OriginalLog is a caller-owned original LogOption graph, decoded independently
// from original input. Copy captures SnapshotArray, while public return samples
// its current property. It is not a prepared history or a second transcript cache.
type OriginalLog struct {
	Fields  protocol.JSONObject
	History *filehistorycopy.Log
}

func DecodeOriginalLog(fields protocol.JSONObject) (*OriginalLog, error) {
	if !fields.IsObject() {
		return nil, &CapabilityError{Capability: "non-object original LogOption"}
	}
	if raw := fields.Get("messages"); len(raw) == 0 || raw[0] != '[' {
		return nil, &CapabilityError{Capability: "original LogOption.messages array"}
	}
	history, e := filehistorycopy.DecodeLog(fields)
	if e != nil {
		return nil, e
	}
	return &OriginalLog{fields, history}, nil
}
func (log *OriginalLog) ReplaceSnapshots(value *filehistorycopy.SnapshotArray) {
	log.History.Snapshots = value
	if value == nil {
		log.Fields.Set("fileHistorySnapshots", nil)
	} else {
		log.Fields.Set("fileHistorySnapshots", encode(value.Items))
	}
}
func (log *OriginalLog) messages() []json.RawMessage {
	rows := make([]json.RawMessage, 0, len(log.History.Messages))
	for _, message := range log.History.Messages {
		rows = append(rows, encode(message))
	}
	return rows
}

type ownedResume struct {
	state   *bootstrapstate.State
	startup *clistartupenv.Service
	binding *ownerasync.NativeBinding
	prime   func(*cooperative.Scope, string, *sessionhistory.Transcript) error
}

// NewOwned is constructor binding only: no settings getter, policy/cache seeding,
// environment assignment or native plan work. New retains its legacy recipe.
func NewOwned(options Options, state *bootstrapstate.State, startup *clistartupenv.Service) (*Service, error) {
	if state == nil || startup == nil {
		return nil, cooperative.ErrRestricted
	}
	service, e := newBoundService(options)
	if e != nil {
		return nil, e
	}
	service.owned = &ownedResume{state: state, startup: startup}
	return service, nil
}

// BindOwnedNative borrows the concrete preactivation PendingWriter's one ledger
// and cache admission. Identity guards do not certify arbitrary interface hosts.
func (s *Service) BindOwnedNative(binding *ownerasync.NativeBinding, prime func(*cooperative.Scope, string, *sessionhistory.Transcript) error) error {
	if s == nil || s.owned == nil || s.owned.binding != nil || binding == nil || !binding.Owns(s.owned.state.Owner()) || prime == nil {
		return cooperative.ErrRestricted
	}
	s.owned.binding = binding
	s.owned.prime = prime
	return nil
}
func (s *Service) checkOwned(scope *cooperative.Scope) error {
	if s == nil || s.owned == nil || s.owned.binding == nil || s.ownedFileHistory == nil {
		return cooperative.ErrRestricted
	}
	return s.owned.state.CheckScope(scope)
}
func resolveOwned[T any](future *ownerasync.OwnerFuture[T], scope *cooperative.Scope, value T, failure error) {
	var e error
	if failure != nil {
		e = future.Reject(scope, failure)
	} else {
		e = future.Resolve(scope, value)
	}
	if e != nil {
		_ = future.RejectAdministrative(e)
	}
}

// BeginLoad is the exported async recovery producer, not a worker calling Load.
// Unsupported selectors remain explicit; no sync legacy fallback is used.
func (s *Service) BeginLoad(scope *cooperative.Scope, input Input) (*ownerasync.OwnerFuture[*Result], error) {
	if e := s.checkOwned(scope); e != nil {
		return nil, e
	}
	out, e := ownerasync.NewOwnerFuture[*Result](s.owned.state.Owner())
	if e != nil {
		return nil, e
	}
	fail := func(next *cooperative.Scope, e error) {
		s.options.OnError(e)
		resolveOwned(out, next, (*Result)(nil), e)
	}
	if input.Original != nil && (input.Source != nil || input.JSONLPath != "") {
		fail(scope, &CapabilityError{Capability: "mutually exclusive original LogOption/raw selector"})
		return out, nil
	}
	var log *OriginalLog
	if input.Original != nil {
		log = input.Original
	} else if input.Source != nil && input.JSONLPath != "" {
		return s.beginOwnedJSONL(scope, input.JSONLPath)
	} else if input.Source == nil {
		fail(scope, &CapabilityError{Capability: "public continue enumeration/BG-session producer"})
		return out, nil
	} else if _, ok := units(input.Source); ok {
		if !s.options.PublicSID {
			fail(scope, &CapabilityError{Capability: "public SID transcript producer"})
			return out, nil
		}
		return s.beginOwnedSID(scope, input.Source)
	} else if isNull(input.Source) {
		resolveOwned(out, scope, (*Result)(nil), nil)
		return out, nil
	} else {
		log, e = DecodeOriginalLog(object(input.Source))
		if e != nil {
			fail(scope, e)
			return out, nil
		}
	}
	if log == nil || log.History == nil {
		fail(scope, &CapabilityError{Capability: "original LogOption graph binding"})
		return out, nil
	}
	proceed := func(next *cooperative.Scope, original *OriginalLog) {
		sid := original.Fields.Get("sessionId")
		if !truthy(sid) {
			sid = nil
			if len(original.History.Messages) > 0 {
				sid = original.History.Messages[0].Get("sessionId")
			}
		}
		afterPlan := func(next *cooperative.Scope) { s.finishOwnedHistory(next, original, sid, out, fail) }
		if truthy(sid) {
			plan, e := s.beginOwnedPlan(next, original, sid)
			if e != nil {
				fail(next, e)
				return
			}
			_, e = plan.Then(next, func(after *cooperative.Scope, _ bool, failure error) error {
				if failure != nil {
					fail(after, failure)
				} else {
					afterPlan(after)
				}
				return nil
			})
			if e != nil {
				fail(next, e)
			}
		} else {
			afterPlan(next)
		}
	}
	if len(log.History.Messages) == 0 && log.Fields.Get("sessionId") != nil {
		// loadFullLog is async even when it returns the input for missing fullPath.
		if truthy(log.Fields.Get("fullPath")) {
			fail(scope, &CapabilityError{Capability: "public lite transcript producer"})
			return out, nil
		}
		full, _ := ownerasync.NewOwnerFuture[*OriginalLog](s.owned.state.Owner())
		resolveOwned(full, scope, log, nil)
		_, e = full.Then(scope, func(next *cooperative.Scope, value *OriginalLog, failure error) error {
			if failure != nil {
				fail(next, failure)
			} else {
				proceed(next, value)
			}
			return nil
		})
		if e != nil {
			fail(scope, e)
		}
	} else {
		proceed(scope, log)
	}
	return out, nil
}
func (s *Service) finishOwnedHistory(scope *cooperative.Scope, log *OriginalLog, sid json.RawMessage, out *ownerasync.OwnerFuture[*Result], fail func(*cooperative.Scope, error)) {
	// Enter actual copy prefix, discard its Promise. No dependent callback/await
	// is added by loadConversationForResume; copy owns its catches/native refs.
	if _, e := s.BeginCopy(scope, log.History); e != nil {
		fail(scope, e)
		return
	}
	rows := log.messages()
	if e := s.checkConsistency(rows); e != nil {
		fail(scope, e)
		return
	}
	s.finishOwnedMessages(scope, rows, log.Fields, func() *filehistorycopy.SnapshotArray { return log.History.Snapshots }, sid, out, fail)
}

func (s *Service) finishOwnedMessages(scope *cooperative.Scope, rows []json.RawMessage, fields protocol.JSONObject, currentSnapshots func() *filehistorycopy.SnapshotArray, sid json.RawMessage, out *ownerasync.OwnerFuture[*Result], fail func(*cooperative.Scope, error)) {
	history, e := sessionresume.ParseMessages(rows)
	if e != nil {
		fail(scope, e)
		return
	}
	history, e = s.options.Reducer.FilterMalformedAttachments(history)
	if e != nil {
		fail(scope, e)
		return
	}
	for _, m := range history {
		if text(m.Field("type")) == "attachment" {
			kind := object(m.Field("attachment")).String("type")
			if (kind == "invoked_skills" || kind == "skill_listing") && (s.options.NativeSkillBindings == nil || !s.options.NativeSkillBindings()) {
				fail(scope, &CapabilityError{Capability: "public resumed Skills authority/next-listing latch"})
				return
			}
		}
	}
	// The same invocation authority and process-local listing latch are restored
	// before deserialization, matching conversationRecovery's original stage.
	if e := s.restoreSkills(history); e != nil {
		fail(scope, e)
		return
	}
	reduced, e := s.options.Reducer.Deserialize(history)
	if e != nil {
		fail(scope, e)
		return
	}
	hooks, e := s.beginOwnedSessionStart(scope, text(sid))
	if e != nil {
		fail(scope, e)
		return
	}
	_, e = hooks.Then(scope, func(next *cooperative.Scope, _ struct{}, failure error) error {
		if failure != nil {
			fail(next, failure)
			return nil
		}
		metadata := fields.Clone()
		if s.options.StartupHooks != nil {
			hookSID := text(sid)
			if hookSID == "" {
				hookSID = s.owned.state.SessionID()
			}
			startup, err := s.options.StartupHooks.TakeSessionStartResult(next, hookSID)
			if err != nil {
				fail(next, err)
				return nil
			}
			for _, message := range startup.Messages {
				raw, err := message.MarshalJSON()
				if err != nil {
					fail(next, err)
					return nil
				}
				parsed, err := sessionresume.ParseMessages([]json.RawMessage{raw})
				if err != nil {
					fail(next, err)
					return nil
				}
				reduced.Messages = append(reduced.Messages, parsed...)
			}
		}
		var snapshots *filehistorycopy.SnapshotArray
		if currentSnapshots != nil {
			snapshots = currentSnapshots()
		}
		if snapshots != nil {
			metadata.Set("fileHistorySnapshots", encode(snapshots.Items))
		}
		metadata.Set("sessionId", sid)
		result := &Result{Result: reduced, Metadata: metadata, ownedSnapshots: snapshots}
		resolveOwned(out, next, result, nil)
		return nil
	})
	if e != nil {
		fail(scope, e)
	}
}
func (s *Service) beginOwnedSessionStart(scope *cooperative.Scope, sessionID ...string) (*ownerasync.OwnerFuture[struct{}], error) {
	out, e := ownerasync.NewOwnerFuture[struct{}](s.owned.state.Owner())
	if e != nil {
		return nil, e
	}
	bare := envTruthy(s.options.Environment["CLAUDE_CODE_SIMPLE"])
	for _, arg := range s.options.Arguments {
		bare = bare || arg == "--bare"
	}
	if !bare {
		if s.options.StartupHooks != nil {
			sid := s.owned.state.SessionID()
			if len(sessionID) > 0 && sessionID[0] != "" {
				sid = sessionID[0]
			}
			return s.options.StartupHooks.BeginSessionStart(scope, s.owned.state.Owner(), sid, "resume")
		}
		resolveOwned(out, scope, struct{}{}, &CapabilityError{Capability: "non-bare SessionStart policy/plugin/registered hooks"})
	} else {
		resolveOwned(out, scope, struct{}{}, nil)
	}
	return out, nil
}
