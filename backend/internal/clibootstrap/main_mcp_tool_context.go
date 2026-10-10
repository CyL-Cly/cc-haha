package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionplans"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolsearch"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

// NativeMCPToolContext owns the non-ASK tool lifetime from entrypoints/mcp.ts.
// Its cache is shared across requests, while permission/app context is created
// for each request. Auxiliary Web and Workflow requests use their original
// producers; this entrypoint creates no main ASK query loop.
type NativeMCPToolContext struct {
	hooks               *queryhooks.Service
	worktreeService     *worktree.Service
	modelTLS            *tlsconfig.State
	platform            string
	state               *bootstrapstate.State
	plans               *sessionplans.Service
	models              *sdkhost.NativeMCPModels
	modelAuth           *MainPrint
	background          *builtintools.BackgroundTaskBindings
	workflowDefinitions *workflow.Service
	command             *MainCommand
	refs                ownerasync.NativeReferences
	binding             *ownerasync.NativeBinding
	cache               *builtinwrite.FileStateCache
	tools               *builtintools.Service
	schemas             *toolschema.Session
	mu                  sync.Mutex
	closed              bool
	debug, verbose      bool
	model               string
	closeAuth           sync.Once
}

func emptyMCPPermissionContext() protocol.JSONObject {
	return protocol.NewJSONObject(protocol.JSONField{Name: "mode", Value: quoteMain("default")}, protocol.JSONField{Name: "additionalWorkingDirectories", Value: json.RawMessage(`{}`)}, protocol.JSONField{Name: "alwaysAllowRules", Value: json.RawMessage(`{}`)}, protocol.JSONField{Name: "alwaysDenyRules", Value: json.RawMessage(`{}`)}, protocol.JSONField{Name: "alwaysAskRules", Value: json.RawMessage(`{}`)}, protocol.JSONField{Name: "isBypassPermissionsModeAvailable", Value: json.RawMessage(`false`)})
}

func NewNativeMCPToolContext(command *MainCommand, platform string, debug, verbose bool) (*NativeMCPToolContext, error) {
	if command == nil || command.Owner == nil || command.Native == nil || !command.Native.NativeProcess {
		return nil, cooperative.ErrRestricted
	}

	m := &NativeMCPToolContext{platform: platform, command: command, schemas: toolschema.NewSession(), debug: debug, verbose: verbose}
	var err error
	m.binding, err = m.refs.Bind(command.Owner)
	if err != nil {
		return nil, err
	}
	m.cache, err = builtinwrite.NewFileStateCache(command.Owner, 100, 25*1024*1024)
	if err != nil {
		return nil, err
	}
	n := command.Native
	settings, err := command.Settings.InitialSettings()
	if err != nil {
		return nil, err
	}
	if settings == nil {
		value := protocol.NewJSONObject()
		settings = &value
	}
	// Bare mode makes Source subscriber auth inactive; selecting the model
	// for a prompt does not construct credentials or a model request.
	selector, err := modelselection.NewParsing(modelselection.Options{Environment: n.Environment, Settings: settings.Clone()})
	if err != nil {
		return nil, err
	}
	selected, err := selector.Select()
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(selected, &m.model)
	permission := func(scope *cooperative.Scope) (protocol.JSONObject, error) {
		if !scope.BelongsTo(command.Owner) {
			return protocol.JSONObject{}, cooperative.ErrRestricted
		}
		return emptyMCPPermissionContext(), nil
	}
	writer, err := builtinwrite.New(builtinwrite.Options{Owner: command.Owner, NativeBinding: m.binding, Cache: m.cache, Home: n.Home, CWD: n.CWD, PID: os.Getpid(), NowMillis: n.NowMillis, Environment: func(k string) string { return n.Environment[k] }, PermissionContext: permission, Host: builtinwrite.Host{Platform: platform}, HostState: func(scope *cooperative.Scope) (builtinwrite.Host, error) {
		if !scope.BelongsTo(command.Owner) {
			return builtinwrite.Host{}, cooperative.ErrRestricted
		}
		return builtinwrite.Host{Platform: platform, NonInteractive: true, AnalyticsEnabled: !nativeAnalyticsDisabled(n.Environment)}, nil
	}, Files: mainWriteFiles{mainSettingsFiles{n: n}}})
	if err != nil {
		return nil, err
	}
	options := builtintools.Options{Owner: command.Owner, NativeBinding: m.binding, Cache: m.cache, Write: writer, Home: n.Home, CWD: n.CWD, Platform: platform, NowMillis: n.NowMillis, Environment: n.Environment, PermissionContext: permission, NonInteractive: true}
	if !envTruthyNative(n.Environment["CLAUDE_CODE_SIMPLE"]) {
		options, err = m.ordinaryOptions(options)
		if err != nil {
			return nil, err
		}
	}
	m.tools, err = builtintools.New(options)
	if err != nil {
		return nil, err
	}
	if m.state != nil {
		if err = command.Run(context.Background(), func(scope *cooperative.Scope) error { return m.initializeOrdinaryModels(scope, m.modelTLS) }); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (m *NativeMCPToolContext) catalogOnOwner(scope *cooperative.Scope) ([]toolschema.Definition, []queryloop.RegisteredTool, error) {
	return m.tools.Catalog(scope)
}
func (m *NativeMCPToolContext) Catalog(ctx context.Context) ([]protocol.JSONObject, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, cooperative.ErrClosed
	}
	m.refs.Begin()
	m.mu.Unlock()
	defer m.refs.End()
	var result []protocol.JSONObject
	err := m.runRequest(ctx, func(scope *cooperative.Scope) error {
		defs, _, err := m.catalogOnOwner(scope)
		if err != nil {
			return err
		}
		result = make([]protocol.JSONObject, 0, len(defs))
		for _, def := range defs {
			input, err := toolschema.OriginalInputSchema(def)
			if err != nil {
				return err
			}
			prompt, err := m.schemas.OriginalPrompt(def, m.schemaHost(scope))
			if err != nil {
				return err
			}
			if def.Name == "Agent" {
				var schema protocol.JSONObject
				_ = schema.UnmarshalJSON(input)
				properties, _ := schema.Object("properties")
				mode, _ := properties.Object("mode")
				mode.Set("enum", rawMain([]string{"acceptEdits", "bypassPermissions", "default", "dontAsk", "plan"}))
				properties.Set("mode", rawMain(mode))
				schema.Set("properties", rawMain(properties))
				input = rawMain(schema)
			}
			value := nativeMCPToolMetadata(def)
			value.Set("description", prompt)
			value.Set("inputSchema", input)
			if output := nativeMCPOutputSchema(def.Name); output != nil {
				value.Set("outputSchema", output)
			}
			result = append(result, value)
		}
		return nil
	})
	return result, err
}
func (m *NativeMCPToolContext) Call(ctx context.Context, name string, args json.RawMessage) (protocol.JSONObject, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return protocol.JSONObject{}, cooperative.ErrClosed
	}
	m.refs.Begin()
	m.mu.Unlock()
	defer m.refs.End()
	var result protocol.JSONObject
	err := m.runRequest(ctx, func(scope *cooperative.Scope) error {
		defs, tools, err := m.catalogOnOwner(scope)
		if err != nil {
			return err
		}
		var selected *queryloop.RegisteredTool
		for i := range tools {
			if tools[i].Name == name {
				selected = &tools[i]
				break
			}
			for _, alias := range tools[i].Aliases {
				if alias == name {
					selected = &tools[i]
					break
				}
			}
			if selected != nil {
				break
			}
		}
		if selected == nil {
			return fmt.Errorf("Tool %s not found", name)
		}
		input := protocol.NewJSONObject()
		if len(args) > 0 && string(args) != "null" {
			if err = input.UnmarshalJSON(args); err != nil {
				return err
			}
		}
		call := &builtinwrite.CallContext{Scope: scope, PermissionContext: func(*cooperative.Scope) (protocol.JSONObject, error) { return emptyMCPPermissionContext(), nil }, Messages: []queryloop.Message{}, MainLoopModel: m.model}
		if m.state != nil {
			catalogue := &toolsearch.Catalogue{Tools: []toolsearch.Tool{}, PendingServers: []json.RawMessage{}, Gate: toolsearch.GateInputs{Environment: m.command.Native.Environment}}
			for i, d := range defs {
				definition := d
				registered := tools[i]
				aliases := []json.RawMessage{}
				for _, alias := range registered.Aliases {
					aliases = append(aliases, quoteMain(alias))
				}
				catalogue.Tools = append(catalogue.Tools, toolsearch.Tool{Name: quoteMain(registered.Name), Aliases: aliases, IsMCP: registered.IsMCP, AlwaysLoad: registered.AlwaysLoad, ShouldDefer: registered.ShouldDefer, SearchHint: registered.SearchHint, Prompt: func(context.Context) (json.RawMessage, error) {
					return m.schemas.OriginalPrompt(definition, m.schemaHost(scope))
				}, Schema: func(context.Context) (json.RawMessage, error) { return toolschema.OriginalInputSchema(definition) }})
			}
			call.ToolSearchCatalogue = catalogue
		}
		// Source entrypoint deliberately skips zod input parsing and directly
		// invokes validateInput/call with empty message context.
		if selected.ValidateInput != nil {
			if err = selected.ValidateInput(ctx, input, call); err != nil {
				result = mcpToolError(fmt.Sprintf("Tool %s input is invalid: %s", name, err.Error()))
				return nil
			}
		}
		value, err := selected.CallJSON(ctx, input, queryloop.JSONToolCallContext{Opaque: call})
		if err != nil {
			result = mcpToolError(err.Error())
			return nil
		}
		if selected.Name == "Bash" {
			value.Data, err = builtintools.OriginalBashCallData(value.Data)
			if err != nil {
				return err
			}
		}
		text, err := protocol.ParseAndStringifyJavaScriptJSON(value.Data)
		if err != nil {
			return err
		}
		result = protocol.NewJSONObject(protocol.JSONField{Name: "content", Value: rawMain([]protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("text")}, protocol.JSONField{Name: "text", Value: quoteMain(string(text))})})})
		return nil
	})
	return result, err
}
func mcpToolError(text string) protocol.JSONObject {
	text = strings.TrimFunc(text, protocol.IsJavaScriptWhitespace)
	if text == "" {
		text = "Error"
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "isError", Value: json.RawMessage(`true`)}, protocol.JSONField{Name: "content", Value: rawMain([]protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("text")}, protocol.JSONField{Name: "text", Value: quoteMain(text)})})})
}
func (m *NativeMCPToolContext) Close(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	_, idle := m.refs.Snapshot()
	if idle == nil {
		return nil
	}
	select {
	case <-idle:
		m.closeAuth.Do(func() {
			if m.modelAuth != nil {
				if m.modelAuth.apiKeyHelper != nil {
					_ = m.modelAuth.apiKeyHelper.Close(ctx)
				}
				m.modelAuth.closeNativeCloudCredentialCaches(ctx)
			}
		})
		return nil
	case <-ctx.Done():
		return errors.Join(context.Cause(ctx), errors.New("MCP native file operations have not settled"))
	}
}

func (m *NativeMCPToolContext) schemaHost(scope *cooperative.Scope) toolschema.Host {
	guideline, e := m.ordinaryWorkflowGuideline(scope)
	if e != nil {
		guideline = "medium"
	}
	now := m.command.Native.Now()
	if override := m.command.Native.Environment["CLAUDE_CODE_OVERRIDE_DATE"]; override != "" {
		if date, e := time.Parse("2006-01-02", override); e == nil {
			now = date
		}
	}
	return toolschema.Host{BaseURL: m.command.Native.Environment["ANTHROPIC_BASE_URL"], MainLoopModel: m.model, BashEnvironment: m.command.Native.Environment, AgentDefinitions: []toolschema.AgentPromptDefinition{}, QuestionPreviewFormat: "", PlanUserType: m.command.Native.Environment["USER_TYPE"], WorkflowConcurrency: min(16, max(2, runtime.NumCPU()-2)), WorkflowSizeGuideline: guideline, WebSearchMonthYear: now.Format("January 2006")}
}

// MCP handlers are independent JS async calls. The owner still serializes every
// continuation, while an IO wait admits another request, as the Source server.
func (m *NativeMCPToolContext) runRequest(ctx context.Context, work func(*cooperative.Scope) error) error {
	receipt, e := m.command.Owner.SubmitIndependent(ctx, cooperative.Read, work)
	if e == nil {
		e = receipt.Wait()
	}
	return e
}
