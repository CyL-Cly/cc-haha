package sdkhost

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelyield"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

// QueryRemoteTitle is Source queryHaiku's isolated, no-tools model consumer.
// It shares actual auth, provider requests, accounting and owner settlement.
func (h *Host) QueryRemoteTitle(ctx context.Context, scope *cooperative.Scope, prompt json.RawMessage, model string) (string, error) {
	if h == nil || !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess || !h.nativeRemoteTitleModels.Owns(h.options.Owner) {
		return "", cooperative.ErrRestricted
	}
	var output string
	err := h.nativeRemoteTitleModels.Run(scope, ctx, func(producer *cooperative.Scope) error {
		device, err := h.requestDeviceID(producer)
		if err != nil {
			return err
		}
		_ = h.options.Factory.CreateUser(storage.Object{"content": raw([]protocol.JSONObject{})})
		_ = h.options.Factory.CreateUser(storage.Object{"content": prompt})
		user := messages.OrderedUser(h.options.Factory.CreateUser(storage.Object{"content": prompt}))
		disabled := false
		definitions := []toolschema.Definition{}
		format := json.RawMessage(`{"type":"json_schema","schema":{"type":"object","properties":{"title":{"type":"string"},"branch":{"type":"string"}},"required":["title","branch"],"additionalProperties":false}}`)
		input := requestretry.Input{Context: requestcontext.Context{History: []protocol.JSONObject{user}, SystemPrompt: []json.RawMessage{}, Tools: []protocol.JSONObject{}, Thinking: requestcontext.Thinking{Type: "disabled"}, Options: requestcontext.Options{Model: model, QuerySource: "teleport_generate_title", IsNonInteractiveSession: false, OutputFormat: format, EnablePromptCaching: &disabled}, Host: requestcontext.Host{Profile: requestcontext.NativeDirectRelayProfile, Env: h.requestEnvironment, DeviceID: device, SessionID: h.options.Controller.SessionID(), NativeToolDefinitions: &definitions, SDKBetas: h.options.SDKBetas, Workload: h.options.Workload, TTLAllowlist: []string{}}}, Runtime: h.options.Runtime, ResolvedFastMode: &disabled}
		deps := h.requestDependencies(producer)
		deps.NativeRemoteTitleSource = h.nativeRemoteTitleModels
		var last json.RawMessage
		_, err = modelyield.RunQueued(ctx, input, modelyield.QueuedDependencies{OnNativeQuota: h.nativeQuotaConsumer(), Request: deps, Activity: h.options.Activity, Session: h.sessionAccounting, AgentContext: h.agentContext, StreamClock: h.streamClock, StreamPolicy: h.streamPolicy, SuccessDuration: &modelaccounting.SuccessDuration{NowMillis: h.options.NowMillis, GetPermission: h.options.Controller.SuccessPermissionOnOwner, Ignored: h.permissionRejections}, Start: func(first *cooperative.Scope, iterator *modelyield.QueryIterator, _ *modelyield.ResourceControl) (*ownerasync.OwnerFuture[struct{}], error) {
			done, err := ownerasync.NewOwnerFuture[struct{}](h.options.Owner)
			if err != nil {
				return nil, err
			}
			var next func(*cooperative.Scope) error
			next = func(current *cooperative.Scope) error {
				pending, err := iterator.Next(current)
				if err != nil {
					return done.Reject(current, err)
				}
				_, err = pending.Then(current, func(callback *cooperative.Scope, result modelyield.QueryResult, failure error) error {
					if failure != nil {
						return done.Reject(callback, failure)
					}
					if result.Done {
						return done.Resolve(callback, struct{}{})
					}
					if value := result.Value.Yield; value != nil && value.Update != nil && value.Update.Envelope != nil {
						last, err = value.Update.Envelope.MessageJSON()
						if err != nil {
							return done.Reject(callback, err)
						}
					}
					return next(callback)
				})
				return err
			}
			if err := next(first); err != nil {
				return nil, err
			}
			return done, nil
		}})
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		var message protocol.JSONObject
		var content []protocol.JSONObject
		if message.UnmarshalJSON(last) != nil || json.Unmarshal(message.Get("content"), &content) != nil || len(content) == 0 || content[0].String("type") != "text" {
			return errors.New("No title response text")
		}
		output = content[0].String("text")
		return producer.AwaitFulfilled()
	})
	return output, err
}
