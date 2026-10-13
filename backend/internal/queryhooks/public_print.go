package queryhooks

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

// EmptyEvent evaluates the real config snapshot, registry and session stores.
// It cannot silently discard an active callback, matcher, or programmatic hook.
func (s *Service) EmptyEvent(event, sid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.simple() {
		return nil
	}
	switch event {
	case "SessionStart", "PreToolUse", "PostToolUse", "PostToolUseFailure":
	case "SubagentStart", "SubagentStop", "TaskCreated", "TaskCompleted", "ConfigChange", "WorktreeCreate", "WorktreeRemove":
		if s.options.Profile != ResolvedSettingsProfile {
			return unsupported("public print hook event " + event)
		}
	default:
		return unsupported("public print hook event " + event)
	}
	if _, e := s.exists(event, sid); e != nil {
		return e
	}
	if s.active(event, sid) || s.registry.programmatic != 0 {
		return unsupported("active " + event + " hook execution")
	}
	return nil
}
func (s *Service) BeginSessionStart(scope *cooperative.Scope, owner *cooperative.Owner, sid, trigger string) (*ownerasync.OwnerFuture[struct{}], error) {
	if !scope.BelongsTo(owner) {
		return nil, cooperative.ErrRestricted
	}
	if trigger != "startup" && trigger != "resume" && trigger != "clear" && trigger != "compact" {
		return nil, unsupported("SessionStart trigger")
	}
	f, e := ownerasync.NewOwnerFuture[struct{}](owner)
	if e != nil {
		return nil, e
	}
	s.mu.Lock()
	simple := s.simple()
	s.mu.Unlock()
	if simple {
		_ = f.Resolve(scope, struct{}{})
		return f, nil
	}
	if s.options.Commands != nil && s.options.Commands.Owner == owner {
		_, e = owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
			input := NativeSessionStartInput{SessionID: sid, Source: trigger, Call: Context{Scope: next, ForceSyncExecution: s.options.ForceSyncSessionStart}}
			if s.options.SessionStartBase != nil {
				var baseErr error
				input.Base, baseErr = s.options.SessionStartBase(next, sid)
				if baseErr != nil {
					return f.Reject(next, baseErr)
				}
			}
			if s.options.SessionStartMetadata != nil {
				var metadataErr error
				input.AgentType, input.Model, metadataErr = s.options.SessionStartMetadata(next)
				if metadataErr != nil {
					return f.Reject(next, metadataErr)
				}
			}
			result, failure := s.ExecuteSessionStart(context.Background(), input)
			if failure == nil && len(result.WatchPaths) > 0 {
				if s.options.UpdateWatchPaths == nil {
					failure = unsupported("actual SessionStart watchPaths consumer")
				} else {
					failure = s.options.UpdateWatchPaths(next, result.WatchPaths)
				}
			}
			if failure != nil {
				return f.Reject(next, failure)
			}
			s.mu.Lock()
			if s.startupResults == nil {
				s.startupResults = map[string]SessionStartResult{}
			}
			s.startupResults[sid] = result
			if hookTruthy(result.InitialUserMessage) {
				s.initialUserMessage = result.InitialUserMessage
			}
			s.mu.Unlock()
			return f.Resolve(next, struct{}{})
		})
		if e != nil {
			return nil, e
		}
		return f, nil
	}
	if e = s.EmptyEvent("SessionStart", sid); e != nil {
		_ = f.Reject(scope, e)
	} else {
		_ = f.Resolve(scope, struct{}{})
	}
	return f, nil
}
