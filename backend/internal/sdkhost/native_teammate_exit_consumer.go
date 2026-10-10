package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

func (h *Host) afterTeammateToolProjection(scope *cooperative.Scope, action requestretry.NoticeAction, failure error) (requestretry.NoticeAction, error) {
	if failure != nil || action == requestretry.Stop || h.options.Print == nil || h.options.Print.NativeTeammateExit == nil {
		return action, failure
	}
	exit, err := h.options.Print.NativeTeammateExit.check(scope, h.options.Owner, h.options.Controller, h.options.Print.AgentID)
	if err != nil {
		return 0, err
	}
	if exit {
		return requestretry.Stop, nil
	}
	return action, nil
}
