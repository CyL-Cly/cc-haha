package sessioncontroller

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// ParsedSDKFuture is the independent session input task's admission, not an
// ASK iterator receipt. Its UUID lookup and attachment await are owner FIFO
// reactions; no model Scope or worker owns the received-UUID/command stores.
type ParsedSDKFuture struct {
	future *WriteFuture
	owner  *cooperative.Owner
	result Admission
}

func (f *ParsedSDKFuture) Done() <-chan struct{} { return f.future.Done() }
func (f *ParsedSDKFuture) Wait(ctx context.Context) (Admission, error) {
	if err := f.future.Wait(ctx); err != nil {
		return Admission{}, err
	}
	return f.result, nil
}
func (f *ParsedSDKFuture) Await(scope *cooperative.Scope) (Admission, error) {
	if !scope.BelongsTo(f.owner) {
		return Admission{}, cooperative.ErrRestricted
	}
	if err := f.future.Await(scope); err != nil {
		return Admission{}, err
	}
	return f.result, nil
}
func (f *ParsedSDKFuture) Then(scope *cooperative.Scope, run func(*cooperative.Scope, Admission, error), rejected func(error)) error {
	if !scope.BelongsTo(f.owner) || run == nil || rejected == nil {
		return cooperative.ErrRestricted
	}
	return f.future.thenOwner(f.owner, func(scope *cooperative.Scope, err error) {
		run(scope, f.result, err)
	}, rejected)
}

// AdmitParsedSDK ports print's plain-user input branch independently of ASK.
// Active controls, now interruption, attachments and file snapshots remain
// explicit capabilities until their real input continuations are migrated.
func (a *SerialASK) AdmitParsedSDK(scope *cooperative.Scope, input protocol.JSONObject) (*ParsedSDKFuture, error) {
	return a.admitParsedSDK(scope, input, nil)
}

// ContinueParsedSDK retains print's input body after attachment/UUID awaits.
// The continuation kicks the existing drain and requests the next input in
// that same owner reaction; it does not add a Promise await of a new wrapper.
func (a *SerialASK) ContinueParsedSDK(scope *cooperative.Scope, input protocol.JSONObject, continuation func(*cooperative.Scope, Admission, error)) (*ParsedSDKFuture, error) {
	if continuation == nil {
		return nil, cooperative.ErrRestricted
	}
	return a.admitParsedSDK(scope, input, continuation)
}

func (a *SerialASK) admitParsedSDK(scope *cooperative.Scope, input protocol.JSONObject, continuation func(*cooperative.Scope, Admission, error)) (*ParsedSDKFuture, error) {
	if !scope.BelongsTo(a.owner) || !a.controller.active || !a.controller.options.AsyncPersistence || a.pending == nil {
		return nil, capability("session-owned parsed SDK admission")
	}
	content, uuid, priority, err := a.controller.validateSDKInput(input)
	if err != nil {
		return nil, err
	}
	if priority == "now" {
		return nil, capability("dynamic SDK cancellation/admission")
	}
	if envTruthy(a.controller.options.ResumeOptions.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"]) && !envTruthy(a.controller.options.ResumeOptions.Environment["CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING"]) {
		return nil, capability("active file-history snapshot creation before query")
	}
	if err := a.prime(scope); err != nil {
		return nil, err
	}
	f := &ParsedSDKFuture{future: newWriteFuture(), owner: a.owner}
	complete := func(scope *cooperative.Scope, err error) {
		if continuation != nil {
			continuation(scope, f.result, err)
		}
		f.future.settle(err)
	}
	controller := a.controller
	afterExistence := func(scope *cooperative.Scope, historical bool) {
		if truthy(uuid) {
			key, _ := stringKey(uuid)
			if historical || controller.received[key] {
				f.result = Admission{Duplicate: true, Historical: historical}
				if controller.options.ReplayUserMessages {
					ack := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("user")}, protocol.JSONField{Name: "message", Value: input.Get("message")}, protocol.JSONField{Name: "session_id", Value: quote(controller.SessionID())}, protocol.JSONField{Name: "parent_tool_use_id", Value: json.RawMessage("null")}, protocol.JSONField{Name: "uuid", Value: uuid}, protocol.JSONField{Name: "timestamp", Value: input.Get("timestamp")}, protocol.JSONField{Name: "isReplay", Value: raw(true)})
					f.result.Acknowledgement = &ack
					controller.replays = append(controller.replays, ack.Clone())
				}
				if historical && continuation == nil {
					controller.options.OnPhase("lifecycle:completed")
				}
				complete(scope, nil)
				return
			}
			controller.trackReceived(key)
		}
		// resolveAndPrepend is an async function even on the actual absent
		// file_attachments fast path. The parsed scalar reference is retained
		// through its fulfilled Promise and enqueue runs in its await reaction.
		attachment := newWriteFuture()
		attachment.settle(nil)
		_ = attachment.thenOwner(a.owner, func(scope *cooperative.Scope, err error) {
			if err == nil {
				err = controller.enqueueOwned(scope, command{Content: content, UUID: uuid, Priority: priority})
			}
			complete(scope, err)
		}, f.future.settle)
	}
	if !truthy(uuid) {
		afterExistence(scope, false)
		return f, nil
	}
	cache, err := a.pending.SessionUUIDs(scope, controller.options.ResumeOptions.Loader, controller.SessionID())
	if err != nil {
		return nil, err
	}
	// doesMessageExistInSession awaits the memoized cache and returns its
	// membership through a distinct Promise. print then awaits that Promise.
	exists := newWriteFuture()
	historical := false
	_ = cache.future.thenOwner(a.owner, func(scope *cooperative.Scope, err error) {
		if err == nil {
			historical, err = cache.set.OwnerHas(scope, uuid)
		}
		exists.settle(err)
	}, exists.settle)
	_ = exists.thenOwner(a.owner, func(scope *cooperative.Scope, err error) {
		if err != nil {
			complete(scope, err)
			return
		}
		afterExistence(scope, historical)
	}, f.future.settle)
	return f, nil
}
