package resumestate

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

func (s *Service) proceedOwnedOriginal(next *cooperative.Scope, original *OriginalLog, out *ownerasync.OwnerFuture[*Result], fail func(*cooperative.Scope, error), resumeAt string) {
	sid := original.Fields.Get("sessionId")
	if truthy(original.sourceSessionID) {
		sid = original.sourceSessionID
	}
	if !truthy(sid) {
		sid = nil
		if len(original.History.Messages) > 0 {
			sid = original.History.Messages[0].Get("sessionId")
		}
	}
	afterPlan := func(next *cooperative.Scope) { s.finishOwnedHistory(next, original, sid, out, fail, resumeAt) }
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
