package builtintools

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func (s *Service) HasLSPBindings() bool {
	return s != nil && s.options.LSP != nil && s.options.LSP.Owns(s.options.Owner, s.options.NativeBinding)
}
func (s *Service) lspPermission(scope *cooperative.Scope, in *protocol.JSONObject) (queryloop.ToolPermission, error) {
	if err := s.check(scope); err != nil {
		return queryloop.ToolPermission{}, err
	}
	c, err := s.options.PermissionContext(scope)
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	p, err := s.path(*in, "filePath")
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	decision := queryloop.ToolPermission{Behavior: "allow", Input: in, UpdatedInput: in}
	reject := func(behavior, message string) queryloop.ToolPermission {
		if behavior == "ask" && c.String("mode") == "dontAsk" {
			behavior, message = "deny", messages.DontAskRejectMessage("LSP")
			decision.DecisionReason = raw(map[string]string{"type": "mode", "mode": "dontAsk"})
		}
		decision.Behavior = behavior
		decision.Message = quote(message)
		decision.UpdatedInput = nil
		return decision
	}
	if wholeDenied(c, "LSP") {
		decision.DecisionReason = resourceWholeRule(c, "alwaysDenyRules", "deny", "LSP")
		return reject("deny", "Permission to use LSP has been denied."), nil
	}
	bypass := c.String("mode") == "bypassPermissions" || c.String("mode") == "plan" && string(c.Get("isBypassPermissionsModeAvailable")) == "true"
	if !bypass && resourceWholeRule(c, "alwaysAskRules", "ask", "LSP") != nil {
		decision.DecisionReason = resourceWholeRule(c, "alwaysAskRules", "ask", "LSP")
		return reject("ask", "Claude requested permissions to use LSP, but you haven't granted it yet."), nil
	}
	// checkReadPermissionForTool applies Read-specific rules to LSP's getPath;
	// whole Read rules do not replace the actual tool's whole-tool rules.
	safety := s.readPathSafety(c, p)
	denied, err := s.pathRule(c, "alwaysDenyRules", "Read", p)
	if err != nil {
		return decision, err
	}
	if denied {
		return reject("deny", "Permission to read "+p+" has been denied."), nil
	}
	ask, err := s.pathRule(c, "alwaysAskRules", "Read", p)
	if err != nil {
		return decision, err
	}
	if ask && !bypass {
		return reject("ask", "Claude requested permissions to read from "+p+", but you haven't granted it yet."), nil
	}
	if bypass || wholeAllowed(c, "LSP") {
		return decision, nil
	}
	if safety != "" {
		suffix := ", which contains a suspicious Windows path pattern that requires manual approval."
		if safety == "unc" {
			suffix = ", which appears to be a UNC path that could access network resources."
		}
		return reject("ask", "Claude requested permissions to read from "+p+suffix), nil
	}
	if s.pathInAllowedWorkingPath(c, p) {
		return decision, nil
	}
	if s.options.InternalReadable != nil {
		allowed, _, err := s.options.InternalReadable(scope, p)
		if err != nil {
			return decision, err
		}
		if allowed {
			return decision, nil
		}
	}
	for _, name := range []string{"Read", "Edit"} {
		allowed, err := s.pathRule(c, "alwaysAllowRules", name, p)
		if err != nil {
			return decision, err
		}
		if allowed {
			return decision, nil
		}
	}
	return reject("ask", "Claude requested permissions to read from "+p+", but you haven't granted it yet."), nil
}
