package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

// SideQuestionControl borrows the already started canonical Host. It never
// submits a human turn or changes the main abort/permission controller.
func (m *MainPrint) SideQuestionControl(ctx context.Context, request protocol.JSONObject) (protocol.JSONObject, error) {
	var future *sdkhost.OwnerFuture[protocol.JSONObject]
	var result protocol.JSONObject
	receipt, e := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if m.Host == nil || !m.initialized || m.closed {
			return cooperative.ErrClosed
		}
		var failure error
		if request.String("subtype") == "cancel_side_question" {
			result, failure = m.Host.CancelSideQuestionJSON(scope, request.Get("question_id"))
			return failure
		}
		if request.String("subtype") != "side_question" {
			return &MainCapabilityError{Boundary: "side-question control subtype"}
		}
		future, failure = m.Host.BeginSideQuestion(scope, ctx, request)
		return failure
	})
	if e == nil {
		e = receipt.Wait()
	}
	if e != nil {
		return protocol.JSONObject{}, e
	}
	if future != nil {
		return future.Wait(ctx)
	}
	return result, nil
}
