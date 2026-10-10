package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
)

type nativeInProcessTurn struct {
	Identity                                   sessioncontroller.InProcessTeammateIdentity
	Definition                                 *agentcatalog.Definition
	Prompt, Description, Model, PermissionMode string
	StreamScopeID                              string
	History                                    []protocol.JSONObject
	Observe                                    func(*cooperative.Scope, protocol.JSONObject) error
}
type nativeInProcessTurnResult struct {
	AgentID, Transcript string
	Messages            []protocol.JSONObject
	NewMessages         []protocol.JSONObject
}
type nativeInProcessTurnPrepared struct {
	options  MainPrintOptions
	ref      *ownerasync.NativeReference
	streamID string
}

// Every prompt creates the same real independent runAgent graph used by
// foreground/background Agent. The outer teammate identity outlives that graph.
func (m *MainPrint) prepareNativeInProcessTurn(ctx context.Context, scope *cooperative.Scope, turn nativeInProcessTurn) (nativeInProcessTurnPrepared, error) {
	var out nativeInProcessTurnPrepared
	if !scope.BelongsTo(m.Owner) || m.Host == nil || m.prompt == nil || turn.Identity.ParentSessionID != m.Controller.SessionID() || turn.Identity.AgentID != turn.Identity.AgentName+"@"+turn.Identity.TeamName || turn.Identity.TeamName == "" {
		return out, cooperative.ErrRestricted
	}
	catalogue, err := m.Host.NativeToolSearchCatalogue(scope)
	if err != nil {
		return out, err
	}
	if catalogue == nil {
		return out, mainCap("in-process original parent tool pool")
	}
	names, schemas := []string{}, []protocol.JSONObject{}
	for _, tool := range catalogue.Tools {
		var name string
		_ = json.Unmarshal(tool.Name, &name)
		names = append(names, name)
		schemas = append(schemas, protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: tool.Name}))
	}
	parentModel, err := m.Host.InferenceModel(scope)
	if err != nil {
		return out, err
	}
	base, err := m.prompt.ResolveForModel(ctx, schemas, parentModel)
	if err != nil {
		return out, err
	}
	var custom json.RawMessage
	if turn.Definition != nil {
		body, err := m.nativeAgentPromptJSON(scope, *turn.Definition)
		if err != nil {
			return out, err
		}
		custom = body
	}
	system, err := teamspawn.SystemPrompt(base, custom, nil, "")
	if err != nil {
		return out, err
	}
	var body string
	if err = json.Unmarshal(system, &body); err != nil {
		return out, err
	}
	definition := teamspawn.InProcessDefinition(turn.Identity.AgentName, body, turn.Definition)
	mode := turn.PermissionMode
	if mode == "" {
		mode = "default"
	}
	definition.Fields.Set("permissionMode", quoteMain(mode))
	resolved := agentcatalog.ResolveInProcessTools(definition, names, m.options.Native.Environment["USER_TYPE"] == "ant")
	permission, err := m.nativeToolPermissionContext(scope)
	if err != nil {
		return out, err
	}
	model := turn.Model
	if model == "" {
		model = parentModel
	}
	model, err = m.nativeTeamParsedModel(model)
	if err != nil {
		return out, err
	}
	id, err := nativeAgentID()
	if err != nil {
		return out, err
	}
	out.streamID, err = nativeAgentID()
	if err != nil {
		return out, err
	}
	directory, err := m.nativeTaskOutputDirectory(scope)
	if err != nil {
		return out, err
	}
	environment, err := m.prompt.AgentEnvironmentAt(ctx, model, nil, m.workingDirectory(), func(context.Context) (bool, error) {
		return usercontext.GitRoot(mainSettingsFiles{n: m.options.Native}, m.workingDirectory()) != "", nil
	})
	if err != nil {
		return out, err
	}
	request := builtintools.AgentRequest{AgentType: definition.Name, Source: definition.Source, SelectedAgent: definition.Fields.Clone(), PermissionContext: permission, PermissionModeOverride: mode, Tools: resolved.ResolvedTools, Effort: definition.Effort}
	o, err := m.nativeIndependentAgentOptions(scope, request, id, m.workingDirectory(), model, directory, []json.RawMessage{system, environment}, nil)
	if err != nil {
		return out, err
	}
	identity := turn.Identity
	o.DetachedAgent.teammate = &identity
	o.DetachedAgent.preset, err = m.nativeAgentPreset(scope, definition)
	if err != nil {
		return out, err
	}
	copy, err := m.Controller.BorrowNativeCopyBinding(m.Owner)
	if err != nil {
		return out, err
	}
	out.ref, err = copy.Acquire(scope)
	if err != nil {
		return out, err
	}
	out.options = o
	return out, nil
}

func (m *MainPrint) runNativeInProcessTurn(ctx context.Context, turn nativeInProcessTurn) (out nativeInProcessTurnResult, returned error) {
	var prepared nativeInProcessTurnPrepared
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		var err error
		prepared, err = m.prepareNativeInProcessTurn(ctx, scope, turn)
		return err
	})
	if err == nil {
		err = receipt.Wait()
	}
	if err != nil {
		return out, err
	}
	defer prepared.ref.DeliveryFinished()
	o := prepared.options
	var child *MainPrint
	o.OnQueryYield = func(scope *cooperative.Scope, yield *queryloop.Yield) error {
		var data json.RawMessage
		var row protocol.JSONObject
		var err error
		if yield.Stream != nil {
			data, err = yield.Stream.EventJSON()
		} else if yield.Message != nil {
			data, err = yield.Message.MarshalJSON()
			if err == nil {
				err = row.UnmarshalJSON(data)
			}
		}
		if err != nil {
			return err
		}
		if data == nil {
			return nil
		}
		return bridgeAgentVoid(child, m, scope, func(parentScope *cooperative.Scope) error {
			route := yield.Stream != nil || row.String("type") == "assistant" || row.String("type") == "user" || row.String("type") == "system" && (row.String("subtype") == "api_retry" || row.String("subtype") == "streaming_fallback")
			if route {
				if err := m.emitNativeInProcessRun(parentScope, o.AgentID, prepared.streamID, turn.Identity.AgentID, turn.StreamScopeID, "message", data, nil); err != nil {
					return err
				}
			}
			if turn.Observe != nil && yield.Message != nil && !child.nativeTeammateShutdownPending() {
				return turn.Observe(parentScope, row.Clone())
			}
			return nil
		})
	}
	child, err = NewMainPrint(o)
	if err != nil {
		return out, err
	}
	defer func() {
		closeCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		failure := child.ShutdownSession(closeCtx)
		if returned == nil {
			returned = failure
		}
	}()
	defer func() {
		kind := "complete"
		failure := returned
		if errors.Is(failure, context.Canceled) || ctx.Err() != nil {
			kind = "cancelled"
			failure = nil
		} else if failure != nil {
			kind = "error"
		}
		_, emitErr := inProcessParentCall(context.Background(), m, func(scope *cooperative.Scope) (struct{}, error) {
			return struct{}{}, m.emitNativeInProcessRun(scope, o.AgentID, prepared.streamID, turn.Identity.AgentID, turn.StreamScopeID, kind, nil, failure)
		})
		if returned == nil {
			returned = emitErr
		}
	}()
	if err = child.StartSession(ctx); err != nil {
		return out, err
	}
	history, err := teamspawn.FilterIncompleteContext(turn.History)
	if err != nil {
		return out, err
	}
	if err = child.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		preset := child.options.DetachedAgent.preset
		if err := child.mcpTools.InstallAgentConnections(scope, preset.inherited, nil, preset.resources); err != nil {
			return err
		}
		if len(history) > 0 {
			if err := child.Controller.InstallAgentResumeHistory(scope, history); err != nil {
				return err
			}
		}
		var err error
		out.Transcript, err = child.Controller.Writer().ComputedTranscriptPath(scope)
		return err
	}); err != nil {
		return out, err
	}
	metadata := protocol.NewJSONObject(protocol.JSONField{Name: "agentType", Value: quoteMain(turn.Identity.AgentName)}, protocol.JSONField{Name: "model", Value: quoteMain(turn.Model)})
	if turn.Model == "" {
		metadata.Delete("model")
	}
	m.writeAgentWorktreeMetadata(out.Transcript, metadata)
	err = child.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "session_id", Value: quoteMain(o.Arguments.SessionID)}, protocol.JSONField{Name: "message", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoteMain("user")}, protocol.JSONField{Name: "content", Value: quoteMain(turn.Prompt)}))})
		return child.Host.RunOne(scope, ctx, input)
	})
	if err == nil && child.Host.ExitCode() != 0 {
		err = fmt.Errorf("Teammate %s query failed", turn.Identity.AgentID)
	}
	if failure := child.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		original, err := child.Host.OriginalHistoryOnOwner(scope)
		if err != nil {
			return err
		}
		out.Messages = make([]protocol.JSONObject, 0, len(original))
		for _, message := range original {
			data, err := message.MarshalJSON()
			if err != nil {
				return err
			}
			var value protocol.JSONObject
			if err = value.UnmarshalJSON(data); err != nil {
				return err
			}
			out.Messages = append(out.Messages, value)
		}
		if len(out.Messages) < len(history) {
			return mainCap("actual in-process fork history prefix")
		}
		out.NewMessages = append([]protocol.JSONObject{}, out.Messages[len(history):]...)
		return nil
	}); err == nil {
		err = failure
	}
	out.AgentID = o.AgentID
	if errors.Is(err, context.Canceled) {
		return out, err
	}
	return out, err
}
