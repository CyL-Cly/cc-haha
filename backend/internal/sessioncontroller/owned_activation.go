package sessioncontroller

import (
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/resumestate"
)

// BeginActivate is the finite print-style resume producer. It does not prove
// the ordinary print/REPL entry, null/error exit policy or active host hooks.
func (c *Controller) BeginActivate(scope *cooperative.Scope, input Activation) (*ownerasync.OwnerFuture[struct{}], error) {
	if c == nil || c.options.PublicResumeStartup == nil || c.options.Bootstrap == nil || !c.options.AsyncPersistence {
		return nil, cooperative.ErrRestricted
	}
	if e := c.options.Bootstrap.CheckScope(scope); e != nil {
		return nil, e
	}
	if c.active || c.activationPending {
		return nil, capability("repeated headless startup activation")
	}
	out, e := ownerasync.NewOwnerFuture[struct{}](c.options.Bootstrap.Owner())
	if e != nil {
		return nil, e
	}
	c.activationPending = true
	complete := func(next *cooperative.Scope, failure error) {
		c.activationPending = false
		var e error
		if failure != nil {
			e = out.Reject(next, failure)
		} else {
			e = out.Resolve(next, struct{}{})
		}
		if e != nil {
			_ = out.RejectAdministrative(e)
		}
	}
	c.options.OnPhase("resume:start")
	input.Resume.ResumeAt = input.ResumeAt
	loaded, e := c.resume.BeginLoad(scope, input.Resume)
	if e != nil {
		complete(scope, e)
		return out, nil
	}
	_, e = loaded.Then(scope, func(next *cooperative.Scope, result *resumestate.Result, failure error) error {
		if failure != nil {
			complete(next, failure)
			return nil
		}
		c.snapshotMu.Lock()
		c.resumeReturnedSnapshotCount = len(c.snapshotRecords)
		c.snapshotMu.Unlock()
		c.options.OnPhase("resume:return")
		if input.ValidateLoaded != nil {
			if failure := input.ValidateLoaded(result); failure != nil {
				complete(next, failure)
				return nil
			}
		}
		if input.RemoteEmptyStartup && (result == nil || len(result.Messages) == 0) {
			if !c.options.NativeTools || !c.options.PublicPrint {
				complete(next, cooperative.ErrRestricted)
				return nil
			}
			fresh, failure := c.BeginActivateFreshOwned(next, FreshPrintProfile)
			if failure != nil {
				complete(next, failure)
				return nil
			}
			_, failure = fresh.Then(next, func(after *cooperative.Scope, _ struct{}, failure error) error { complete(after, failure); return nil })
			if failure != nil {
				complete(next, failure)
			}
			return nil
		}
		if result == nil {
			complete(next, errors.New("No conversation found"))
			return nil
		}
		history := make([]*protocol.JSONObject, 0, len(result.Messages))
		for _, message := range result.Messages {
			var record protocol.JSONObject
			if e := json.Unmarshal(raw(message), &record); e != nil {
				complete(next, e)
				return nil
			}
			history = append(history, &record)
		}
		restore := func(after *cooperative.Scope) {
			if !input.ForkSession && text(result.Metadata.Get("sessionId")) != "" {
				if c.options.RestoreCosts != nil {
					if _, err := c.options.RestoreCosts(after, text(result.Metadata.Get("sessionId"))); err != nil {
						complete(after, err)
						return
					}
				} else if c.options.PublicPrint {
					complete(after, capability("public project cost-state restore producer"))
					return
				}
			}
			failure := c.restoreActivated(result, history, input)
			if failure == nil && c.options.NativeWorktreeResume != nil {
				failure = c.options.NativeWorktreeResume(after, result.MetadataSnapshot().Get("worktreeSession"), input.ForkSession)
			}
			complete(after, failure)
		}
		sid := text(result.Metadata.Get("sessionId"))
		if !input.ForkSession && sid != "" {
			projectDir := ""
			var project *string
			if path := text(result.Metadata.Get("fullPath")); path != "" {
				projectDir = directory(path)
				project = &projectDir
			}
			c.options.OnPhase("switch")
			if e := c.options.Bootstrap.SwitchSession(next, sid, project); e != nil {
				complete(next, e)
				return nil
			}
			c.writer.Switch(sid, projectDir)
			if !c.options.WriterOptions.SessionPersistenceDisabled {
				c.options.OnPhase("reset")
				reset, e := c.writer.BeginResetSessionFile(next)
				if e != nil {
					complete(next, e)
					return nil
				}
				_, e = reset.Then(next, func(after *cooperative.Scope, _ struct{}, failure error) error {
					if failure != nil {
						complete(after, failure)
					} else {
						restore(after)
					}
					return nil
				})
				if e != nil {
					complete(next, e)
				}
				return nil
			}
		}
		restore(next)
		return nil
	})
	if e != nil {
		complete(scope, e)
	}
	return out, nil
}
