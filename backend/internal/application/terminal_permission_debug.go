package application

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (s *Session) prepareTerminalPermissionDebug(scope *cooperative.Scope, request *tui.PermissionRequest, decision *queryloop.ToolPermission) error {
	if !s.terminalInteractive || s.native == nil || !s.native.NativeProcess || (request.ToolName != "Bash" && request.ToolName != "PowerShell") {
		return nil
	}
	permissions, hint, sandbox, err := s.process.TerminalPermissionDebugSnapshot(scope)
	if err != nil {
		return err
	}
	result := protocol.NewJSONObject(protocol.JSONField{Name: "behavior", Value: raw("ask")})
	if decision != nil {
		result.Set("behavior", raw(decision.Behavior))
		if decision.Message != nil {
			result.Set("message", decision.Message)
		}
		if decision.DecisionReason != nil {
			result.Set("decisionReason", decision.DecisionReason)
		}
		if decision.Suggestions != nil {
			result.Set("suggestions", decision.Suggestions)
		}
	}
	request.Debug = &tui.PermissionDebugData{Result: raw(result), Context: raw(permissions), DebugHint: hint, SandboxAutoAllow: sandbox}
	return nil
}
