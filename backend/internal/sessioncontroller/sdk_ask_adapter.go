package sessioncontroller

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

// SerialASK borrows the actual Controller store and provider envelopes. It
// permits a new original SDK input only after the previous ASK's finally.
// It does not implement concurrent admission, controls, results or accounting.
type SerialASK struct {
	controller *Controller
	owner      *cooperative.Owner
	live       map[*protocol.JSONObject]*streamhistory.Envelope
	running    bool
	cache      *ReadCache
	pending    *PendingWriter
	primed     bool
}

func NewSerialASK(controller *Controller, owner *cooperative.Owner) (*SerialASK, error) {
	if controller == nil || owner == nil {
		return nil, errors.New("serial ASK requires actual Controller and Owner")
	}
	if controller.options.AsyncPersistence && controller.writer.async == nil {
		if _, err := NewPendingWriter(controller.writer, owner); err != nil {
			return nil, err
		}
	}
	if controller.options.AsyncPersistence && controller.writer.async.owner != owner {
		return nil, errors.New("serial ASK requires its canonical independent Writer")
	}
	return &SerialASK{controller: controller, owner: owner, pending: controller.writer.async, live: map[*protocol.JSONObject]*streamhistory.Envelope{}}, nil
}
func (a *SerialASK) Begin(scope *cooperative.Scope, input protocol.JSONObject) (*protocol.JSONObject, error) {
	return a.beginInputMode(scope, input, "")
}

// BeginTerminalBash is a trusted Owner entry. Parsed SDK JSON retains its
// original field validation and cannot select this local input mode.
func (a *SerialASK) BeginTerminalBash(scope *cooperative.Scope, input protocol.JSONObject) (*protocol.JSONObject, error) {
	if !scope.BelongsTo(a.owner) || !a.controller.options.PublicPrint || !a.controller.options.NativeTools || !a.controller.options.AsyncPersistence || a.controller.inputProcessor == nil || a.controller.inputProcessor.ProcessTerminalBash == nil {
		return nil, cooperative.ErrRestricted
	}
	return a.beginInputMode(scope, input, "bash")
}
func (a *SerialASK) beginInputMode(scope *cooperative.Scope, input protocol.JSONObject, mode string) (*protocol.JSONObject, error) {
	if !scope.BelongsTo(a.owner) || a.running {
		return nil, capability("serial ASK owner/lifecycle")
	}
	// AdmitSDK validates all original fields before the controller mutates state.
	if _, _, _, err := a.controller.validateSDKInput(input); err != nil {
		return nil, err
	}
	if input.String("priority") == "now" {
		return nil, capability("dynamic SDK cancellation/admission")
	}
	if err := a.prime(scope); err != nil {
		return nil, err
	}
	a.controller.newSDKAdmitted = false
	var admissions []Admission
	var err error
	if a.controller.options.AsyncPersistence {
		admissions, err = a.controller.admitOriginalBatch(scope, []protocol.JSONObject{input}, mode)
	} else {
		admissions, err = a.controller.AdmitOriginalBatch([]protocol.JSONObject{input})
	}
	if err != nil {
		return nil, err
	}
	if admissions[0].Duplicate {
		return nil, capability("serial duplicate acknowledgement without ASK")
	}
	return a.BeginDequeued(scope)
}

func (a *SerialASK) prime(scope *cooperative.Scope) error {
	if a.controller.options.AsyncPersistence && !a.primed {
		if value := a.controller.resume.UUIDTranscript(a.controller.SessionID()); value != nil {
			if _, _, err := a.pending.PrimeSessionUUIDCache(scope, a.controller.options.ResumeOptions.Loader, a.controller.SessionID(), value); err != nil {
				return err
			}
		}
		a.primed = true
	}
	return nil
}

// BeginDequeued is the sole ASK admission. Session input may have queued a
// follower while the previous iterator was suspended; only this boundary
// accepts it and snapshots the ASK read cache.
func (a *SerialASK) BeginDequeued(scope *cooperative.Scope) (*protocol.JSONObject, error) {
	if !scope.BelongsTo(a.owner) || a.running {
		return nil, capability("serial ASK owner/lifecycle")
	}
	var accepted *protocol.JSONObject
	var err error
	if a.controller.options.AsyncPersistence {
		accepted, err = a.controller.acceptNext(scope)
	} else {
		accepted, err = a.controller.AcceptNext()
	}
	if err != nil || accepted == nil {
		return accepted, err
	}
	a.running = true
	a.cache = a.controller.readCache.Clone()
	return accepted, err
}
func (a *SerialASK) History() []queryloop.Message {
	result := make([]queryloop.Message, 0, len(a.controller.messages))
	for _, record := range a.controller.messages {
		if live := a.live[record]; live != nil {
			result = append(result, queryloop.Message{Assistant: live})
		} else {
			result = append(result, queryloop.Message{Record: *record})
		}
	}
	return result
}
func (a *SerialASK) Accept(scope *cooperative.Scope, message queryloop.Message) error {
	if !scope.BelongsTo(a.owner) || !a.running {
		return capability("serial ASK acceptance owner/lifecycle")
	}
	if message.Assistant != nil {
		record := message.Assistant.Record
		a.controller.messages = append(a.controller.messages, &record)
		a.live[&record] = message.Assistant
		return nil
	}
	if !message.Record.IsObject() {
		return capability("original yielded message object")
	}
	record := message.Record
	a.controller.messages = append(a.controller.messages, &record)
	return nil
}
func (a *SerialASK) Record(scope *cooperative.Scope, history []queryloop.Message) error {
	if a.controller.options.AsyncPersistence {
		future, err := a.BeginRecord(scope, history)
		if err != nil {
			return err
		}
		_, err = future.Await(scope)
		return err
	}
	if !scope.BelongsTo(a.owner) || !a.running {
		return capability("serial ASK record owner/lifecycle")
	}
	values := make([]LiveRecord, 0, len(history))
	for _, message := range history {
		if message.Assistant != nil {
			values = append(values, LiveRecord{Assistant: message.Assistant})
		} else {
			record := message.Record
			values = append(values, LiveRecord{Record: &record})
		}
	}
	_, err := a.controller.writer.RecordLive(LiveRecordProfile{QuerySource: "sdk"}, values, nil)
	return err
}
func (a *SerialASK) Raw(input requestcontext.Context) (RawContext, error) {
	raw, err := a.controller.ResolveRawContext(input)
	if err != nil {
		return RawContext{}, err
	}
	raw.Context.History = nil
	for _, message := range a.History() {
		data, err := message.MarshalJSON()
		if err != nil {
			return RawContext{}, err
		}
		var object protocol.JSONObject
		if err = object.UnmarshalJSON(data); err != nil {
			return RawContext{}, err
		}
		raw.Context.History = append(raw.Context.History, object)
	}
	return raw, nil
}
func (a *SerialASK) Finally(scope *cooperative.Scope) error {
	if !scope.BelongsTo(a.owner) || !a.running {
		return capability("serial ASK finally owner/lifecycle")
	}
	a.controller.readCache = a.cache
	a.running = false
	a.controller.options.OnPhase("read-cache:return")
	a.controller.options.OnPhase("ask:finally")
	return nil
}
func (a *SerialASK) Resolve(ctx context.Context, raw RawContext, history []protocol.JSONObject) (requestcontext.Context, error) {
	return a.controller.ProjectUserContextWithHistory(raw, func([]protocol.JSONObject) ([]protocol.JSONObject, error) { return history, nil })
}

// BeginRecord returns the independent original Record promise. SDK policy, not
// the writer, chooses fire-and-forget or awaited yielded-user behavior.
func (a *SerialASK) BeginRecord(scope *cooperative.Scope, history []queryloop.Message) (*RecordFuture, error) {
	if !scope.BelongsTo(a.owner) || !a.running || !a.controller.options.AsyncPersistence || a.pending == nil {
		return nil, capability("independent serial ASK record owner/lifecycle")
	}
	values := make([]LiveRecord, 0, len(history))
	for _, message := range history {
		if message.Assistant != nil {
			values = append(values, LiveRecord{Assistant: message.Assistant})
		} else {
			record := message.Record
			values = append(values, LiveRecord{Record: &record})
		}
	}
	options, err := a.controller.recordOptions(scope)
	if err != nil {
		return nil, err
	}
	source := a.controller.options.QuerySource
	if source == "" {
		source = "sdk"
	}
	return a.pending.BeginRecordLive(scope, a.controller.options.ResumeOptions.Loader, LiveRecordProfile{QuerySource: source}, values, options)
}
func (controller *Controller) UsesIndependentPersistence() bool {
	return controller.options.AsyncPersistence
}
