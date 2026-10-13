package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolerrors"
	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
	"strings"
	"unicode/utf16"
)

type TerminalBashResult struct {
	Stdout, Stderr string
	Interrupted    bool
	Data           json.RawMessage
}

func terminalBashXML(value string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(value)
}

// RunTerminalBash is a genuine local input to the existing Controller. It writes
// the original caveat, bash-input and bash-output history, with no model query.
func (m *MainPrint) RunTerminalBash(ctx context.Context, command string, progress func(json.RawMessage, protocol.JSONObject) error) (TerminalBashResult, error) {
	var result TerminalBashResult
	if !m.initialized || m.closed {
		return result, cooperative.ErrClosed
	}
	err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "session_id", Value: quoteMain(m.Controller.SessionID())}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": command})})
		return m.Host.RunTerminalBash(scope, ctx, input, func(scope *cooperative.Scope, modelContext context.Context, content json.RawMessage) (*sessioncontroller.ProcessedInput, error) {
			units, e := protocol.DecodeJavaScriptJSONString(content)
			if e != nil {
				return nil, e
			}
			processed, returned, e := m.nativeProcessTerminalBash(modelContext, scope, string(utf16.Decode(units)), progress)
			result = returned
			return processed, e
		})
	})
	return result, err
}
func (m *MainPrint) nativeProcessTerminalBash(ctx context.Context, scope *cooperative.Scope, command string, progress func(json.RawMessage, protocol.JSONObject) error) (*sessioncontroller.ProcessedInput, TerminalBashResult, error) {
	var result TerminalBashResult
	if !scope.BelongsTo(m.Owner) || m.nativeTools == nil || m.options.AgentID != "" {
		return nil, result, cooperative.ErrRestricted
	}
	settings, e := m.Startup.GetInitialSettings()
	if e != nil {
		return nil, result, e
	}
	shellTool := "Bash"
	available, e := m.nativeShellAvailability(scope)
	if e != nil {
		return nil, result, e
	}
	configured := ""
	if settings != nil {
		configured = settings.String("defaultShell")
	}
	if windowshell.DefaultShell(configured, available) == "powershell" {
		shellTool = "PowerShell"
	}
	start := m.options.Native.NowMillis()
	first := messages.OrderedUser(m.factory.CreateUser(storage.Object{"content": quoteMain("<bash-input>" + command + "</bash-input>")}))
	var data json.RawMessage
	var callError error
	if shellTool == "PowerShell" {
		data, callError = m.nativeTools.CallTerminalPowerShell(ctx, scope, command, progress)
	} else {
		data, callError = m.nativeTools.CallTerminalBash(ctx, scope, command, progress)
	}
	result.Data = append(json.RawMessage(nil), data...)
	body := ""
	if callError == nil {
		var value protocol.JSONObject
		if e := value.UnmarshalJSON(data); e != nil {
			return nil, result, e
		}
		result.Stderr = value.String("stderr")
		result.Interrupted = string(value.Get("interrupted")) == "true"
		value.Set("stderr", quoteMain(""))
		var mapped protocol.JSONObject
		var mapError error
		if shellTool == "PowerShell" {
			mapped, mapError = m.nativeTools.MapTerminalPowerShellResult(scope, rawMain(value), quoteMain(uuidMain()))
		} else {
			mapped, mapError = m.nativeTools.MapTerminalBashResult(scope, rawMain(value), quoteMain(uuidMain()))
		}
		if mapError != nil {
			return nil, result, mapError
		}
		mapped, mapError = queryloop.ProcessSmallTextToolResult(mapped, shellTool, 30000)
		if mapError != nil {
			return nil, result, mapError
		}
		if mapped.Get("content") != nil && len(mapped.Get("content")) > 0 && mapped.Get("content")[0] == '"' {
			_ = json.Unmarshal(mapped.Get("content"), &result.Stdout)
		} else {
			result.Stdout = terminalBashXML(value.String("stdout"))
		}
		body = "<bash-stdout>" + result.Stdout + "</bash-stdout><bash-stderr>" + terminalBashXML(result.Stderr) + "</bash-stderr>"
	} else {
		var capability *builtintools.CapabilityError
		if errors.As(callError, &capability) || errors.Is(callError, cooperative.ErrRestricted) || errors.Is(callError, cooperative.ErrClosed) {
			return nil, result, callError
		}
		var shell *toolerrors.ShellError
		if errors.As(callError, &shell) {
			result.Stdout, result.Stderr, result.Interrupted = shell.Stdout, shell.Stderr, shell.Interrupted
			if shell.Interrupted {
				caveat := m.factory.CreateLocalCaveat()
				interrupted := messages.OrderedUser(m.factory.CreateUserInterruption(false))
				return &sessioncontroller.ProcessedInput{Messages: []protocol.JSONObject{caveat, first, interrupted}, NoQuery: true, StartMillis: start, AttachmentsHandled: true}, result, nil
			}
			body = "<bash-stdout>" + terminalBashXML(shell.Stdout) + "</bash-stdout><bash-stderr>" + terminalBashXML(shell.Stderr) + "</bash-stderr>"
		} else {
			result.Stderr = "Command failed: " + callError.Error()
			body = "<bash-stderr>" + terminalBashXML(result.Stderr) + "</bash-stderr>"
		}
	}
	caveat := m.factory.CreateLocalCaveat()
	output := messages.OrderedUser(m.factory.CreateUser(storage.Object{"content": quoteMain(body)}))
	return &sessioncontroller.ProcessedInput{Messages: []protocol.JSONObject{caveat, first, output}, NoQuery: true, StartMillis: start, AttachmentsHandled: true}, result, nil
}
