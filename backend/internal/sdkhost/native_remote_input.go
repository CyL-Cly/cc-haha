package sdkhost

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/remoteio"
)

// Orphan and duplicate control responses never reach print's main input
// consumer. Source closes each of their lifecycle IDs at the parser boundary.
func (input *PlainStructuredInput) completeRemoteResponseLifecycle(message protocol.JSONObject) {
	if input.remote == nil || message.String("type") != "control_response" {
		return
	}
	var id string
	if json.Unmarshal(message.Get("uuid"), &id) == nil && id != "" {
		input.remote.ReportDelivery(id, "processed")
	}
}

// Other non-user messages close at print's inline consumer, before dispatch;
// control_response was already closed above and user completes after ASK.
func (input *PlainStructuredInput) completeRemoteInlineLifecycle(message protocol.JSONObject) {
	if input.remote == nil || message.String("type") == "user" || message.String("type") == "control_response" {
		return
	}
	var id string
	if json.Unmarshal(message.Get("uuid"), &id) == nil && id != "" {
		input.remote.ReportDelivery(id, "processed")
	}
}

// BindRemoteIO keeps the existing StructuredIO parser and Owner iterator. Only
// the class's real internal-event getters/flush Promise differ from PlainIO.
func (input *PlainStructuredInput) BindRemoteIO(scope *cooperative.Scope, remote *remoteio.Service) error {
	if remote == nil || !scope.BelongsTo(input.owner) {
		return cooperative.ErrRestricted
	}
	input.remote = remote
	return nil
}

func (input *PlainStructuredInput) flushRemoteInternalEvents(scope *cooperative.Scope) (*OwnerFuture[struct{}], error) {
	future, err := NewOwnerFuture[struct{}](input.owner)
	if err != nil {
		return nil, err
	}
	go func() {
		err := input.remote.FlushInternalEvents(context.Background())
		_, submitError := input.owner.SubmitIndependent(context.Background(), cooperative.Flush, func(next *cooperative.Scope) error {
			if err != nil {
				return future.Reject(next, err)
			}
			return future.Resolve(next, struct{}{})
		})
		if submitError != nil {
			_ = future.RejectAdministrative(submitError)
		}
	}()
	return future, nil
}
