package builtintools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/nativepaths"
	"github.com/NanmiCoder/cc-haha/backend/internal/powershellpolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func (s *Service) powerShellPolicy(scope *cooperative.Scope) (*powershellpolicy.Parser, *powershellpolicy.Classifier, error) {
	if err := s.check(scope); err != nil {
		return nil, nil, err
	}
	environment, err := s.processEnvironment(scope)
	if err != nil {
		return nil, nil, err
	}
	if s.powerShellParser == nil {
		executable, err := s.PowerShellPath(scope)
		if err != nil {
			return nil, nil, err
		}
		s.powerShellParser = powershellpolicy.NewParser(powershellpolicy.ParserOptions{Platform: s.options.Platform, Executable: executable, CWD: s.options.CWD, Environment: environment, Run: func(ctx context.Context, path string, args []string, env map[string]string, cwd string) (powershellpolicy.ProcessResult, error) {
			return s.runPowerShellASTProcess(ctx, path, args, env, cwd)
		}})
	} else {
		s.powerShellParser.UpdateEnvironment(environment)
	}
	if s.powerShellClassifier == nil || s.powerShellClassifier.UserType != environment["USER_TYPE"] {
		var err error
		s.powerShellClassifier, err = powershellpolicy.NewClassifier(s.options.Platform, environment["USER_TYPE"])
		if err != nil {
			return nil, nil, err
		}
	}
	return s.powerShellParser, s.powerShellClassifier, nil
}

func (s *Service) runPowerShellASTProcess(ctx context.Context, path string, args []string, environment map[string]string, cwd string) (powershellpolicy.ProcessResult, error) {
	// Parser owns the Source configurable per-attempt deadline. Adding a second
	// fixed 5s limit here would silently truncate a larger configured timeout.
	timeout := 5 * time.Second
	if deadline, exists := ctx.Deadline(); exists {
		timeout = time.Until(deadline)
	}
	output, err := s.processOutputEnvironment(ctx, path, args, cwd, timeout, false, environment)
	timedOut := ctx.Err() == context.DeadlineExceeded || output.Interrupted && ctx.Err() == nil
	if timedOut {
		err = nil
	}
	return powershellpolicy.ProcessResult{Stdout: output.Out, Stderr: output.Err, ExitCode: output.Code, TimedOut: timedOut}, err
}
func (s *Service) powerShellRules(context protocol.JSONObject) ([]powershellpolicy.Rule, error) {
	result := []powershellpolicy.Rule{}
	for _, behavior := range []string{"deny", "ask", "allow"} {
		rules, err := orderedHookRules(context, behavior)
		if err != nil {
			return nil, err
		}
		for _, rule := range rules {
			if rule.value.String("toolName") != "PowerShell" || rule.value.Get("ruleContent") == nil {
				continue
			}
			result = append(result, powershellpolicy.Rule{Source: rule.source, RuleBehavior: rule.behavior, RuleValue: powershellpolicy.RuleValue{ToolName: "PowerShell", RuleContent: rule.value.String("ruleContent")}})
		}
	}
	return result, nil
}
func (s *Service) powerShellFileRule(scope *cooperative.Scope, c protocol.JSONObject, operation, behavior, path string) (*powershellpolicy.Rule, error) {
	name := "Edit"
	if operation == "read" {
		name = "Read"
	}
	matched, err := s.matchingHookFileRule(scope, c, name, behavior, path)
	if err != nil || matched == nil {
		return nil, err
	}
	return &powershellpolicy.Rule{Source: matched.source, RuleBehavior: matched.behavior, RuleValue: powershellpolicy.RuleValue{ToolName: matched.value.String("toolName"), RuleContent: matched.value.String("ruleContent")}}, nil
}
func (s *Service) powerShellPathContext(scope *cooperative.Scope, c protocol.JSONObject) (powershellpolicy.PathContext, error) {
	working := []string{s.options.CWD}
	var dirs protocol.JSONObject
	if dirs.UnmarshalJSON(c.Get("additionalWorkingDirectories")) == nil {
		var entries [][]json.RawMessage
		if json.Unmarshal(dirs.Get("map"), &entries) == nil {
			for _, entry := range entries {
				if len(entry) > 0 {
					var dir string
					if json.Unmarshal(entry[0], &dir) == nil {
						working = append(working, dir)
					}
				}
			}
		}
	}
	safety := HookPathSafetyContext{CWD: s.options.CWD, Platform: s.options.Platform}
	if s.options.HookPathSafety != nil {
		var err error
		safety, err = s.options.HookPathSafety(scope)
		if err != nil {
			return powershellpolicy.PathContext{}, err
		}
	}
	paths := powershellpolicy.PathContext{CWD: s.options.CWD, Home: s.options.Home, Platform: s.options.Platform, Mode: c.String("mode"), WorkingDirectories: working, Resolve: powershellpolicy.SafeResolvePath}
	paths.Directory = func(path string) string {
		if !nativepaths.IsUNC(path) {
			if info, err := os.Stat(path); err == nil && info.IsDir() {
				return path
			}
		}
		if s.options.Platform == "windows" {
			return nativepaths.DirWindows(path)
		}
		return filepath.Dir(path)
	}
	paths.Deny = func(path, operation string) (*powershellpolicy.Rule, error) {
		return s.powerShellFileRule(scope, c, operation, "deny", path)
	}
	paths.Check = func(path, operation string, canonical bool) (powershellpolicy.PathCheck, error) {
		if rule, err := paths.Deny(path, operation); err != nil || rule != nil {
			if err != nil {
				return powershellpolicy.PathCheck{}, err
			}
			return powershellpolicy.PathCheck{DecisionReason: map[string]any{"type": "rule", "rule": *rule}}, nil
		}
		if operation != "read" && s.options.HookInternalEditable != nil {
			allowed, err := s.options.HookInternalEditable(scope, path)
			if err != nil {
				return powershellpolicy.PathCheck{}, err
			}
			if allowed {
				return powershellpolicy.PathCheck{Allowed: true, DecisionReason: map[string]string{"type": "other", "reason": "Path is an editable internal directory"}}, nil
			}
		}
		inside := s.pathInAllowedWorkingPath(c, path)
		if operation != "read" {
			forms := []string{path}
			if !canonical {
				forms = append(forms, s.permissionResolved(path))
			}
			checked := CheckHookPathSafety(path, forms, safety, s.options.Platform == "windows" && inside)
			if !checked.Safe {
				return powershellpolicy.PathCheck{DecisionReason: map[string]any{"type": "safetyCheck", "reason": checked.Message, "classifierApprovable": checked.ClassifierApprovable}}, nil
			}
		}
		if inside && (operation == "read" || c.String("mode") == "acceptEdits") {
			return powershellpolicy.PathCheck{Allowed: true}, nil
		}
		if operation == "read" && s.options.InternalReadable != nil {
			allowed, reason, err := s.options.InternalReadable(scope, path)
			if err != nil {
				return powershellpolicy.PathCheck{}, err
			}
			if allowed {
				return powershellpolicy.PathCheck{Allowed: true, DecisionReason: map[string]string{"type": "other", "reason": reason}}, nil
			}
		}
		if operation != "read" && !inside {
			allowed, err := s.powerShellSandboxWriteAllowed(scope, path)
			if err != nil {
				return powershellpolicy.PathCheck{}, err
			}
			if allowed {
				return powershellpolicy.PathCheck{Allowed: true, DecisionReason: map[string]string{"type": "other", "reason": "Path is in sandbox write allowlist"}}, nil
			}
		}
		if rule, err := s.powerShellFileRule(scope, c, operation, "allow", path); err != nil || rule != nil {
			if err != nil {
				return powershellpolicy.PathCheck{}, err
			}
			return powershellpolicy.PathCheck{Allowed: true, DecisionReason: map[string]any{"type": "rule", "rule": *rule}}, nil
		}
		return powershellpolicy.PathCheck{}, nil
	}
	return paths, nil
}
func (s *Service) powerShellPermissionDecision(scope *cooperative.Scope, in *protocol.JSONObject) (queryloop.ToolPermission, error) {
	if err := s.check(scope); err != nil {
		return queryloop.ToolPermission{}, err
	}
	if in == nil || !in.IsObject() {
		return queryloop.ToolPermission{}, capability("actual parsed PowerShell permission input")
	}
	c, err := s.options.PermissionContext(scope)
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	if rule, err := powerShellWholeRule(c, "deny"); err != nil || rule != nil {
		if err != nil {
			return queryloop.ToolPermission{}, err
		}
		return queryloop.ToolPermission{Behavior: "deny", Input: in, Message: quote("Permission to use PowerShell has been denied."), DecisionReason: rule.reason()}, nil
	}
	if rule, err := powerShellWholeRule(c, "ask"); err != nil || rule != nil && !powerShellBypass(c) {
		if err != nil {
			return queryloop.ToolPermission{}, err
		}
		return queryloop.ToolPermission{Behavior: "ask", Input: in, Message: quote(powershellpolicy.RequestMessage(nil)), DecisionReason: rule.reason()}, nil
	}
	parser, classifier, err := s.powerShellPolicy(scope)
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	command, err := text(in.Get("command"))
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	command = protocol.TrimJavaScriptString(command)
	// Source parsePowerShellCommand has no AbortSignal. Its independent native
	// process gets the original bounded two-attempt timeout; the existing native
	// ledger joins it before session teardown, including a canceled owner await.
	future, err := native(s, scope, func() (powershellpolicy.Parsed, error) { return parser.Parse(context.Background(), command), nil })
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	parsed, err := future.Await(scope)
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	// Read-only callbacks in Source consult current process.env after awaiting
	// the AST. Settings may have changed while native parsing yielded the owner.
	_, classifier, err = s.powerShellPolicy(scope)
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	paths, err := s.powerShellPathContext(scope, c)
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	permissionRules, err := s.powerShellRules(c)
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	bare := powerShellBareRepository(s.options.CWD)
	input, err := in.MarshalJSON()
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	result, err := classifier.Evaluate(input, parsed, powershellpolicy.EvaluationContext{Mode: c.String("mode"), Rules: permissionRules, Paths: paths, BareRepository: bare})
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	// Source's outer hasPermissionsToUseToolInner preserves tool denies and
	// content-specific rule/safety asks before applying bypass/whole-tool allow.
	// In particular bypass may not skip protected removal or file deny rules.
	fresh, err := s.options.PermissionContext(scope)
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	result, err = powerShellNormalizePermission(result, input, fresh)
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	decision := queryloop.ToolPermission{Behavior: result.Behavior, Input: in}
	if result.Message != "" {
		decision.Message = quote(result.Message)
	}
	if result.DecisionReason != nil {
		decision.DecisionReason = raw(result.DecisionReason)
	}
	if result.Suggestions != nil {
		decision.Suggestions = raw(result.Suggestions)
	}
	if result.BlockedPath != "" {
		decision.Metadata = raw(protocol.NewJSONObject(field("blockedPath", result.BlockedPath)))
	}
	if len(result.UpdatedInput) > 0 {
		var input protocol.JSONObject
		if err := input.UnmarshalJSON(result.UpdatedInput); err != nil {
			return queryloop.ToolPermission{}, err
		}
		decision.UpdatedInput = &input
	}
	return decision, nil
}

func powerShellBypass(context protocol.JSONObject) bool {
	return context.String("mode") == "bypassPermissions" || context.String("mode") == "plan" && string(context.Get("isBypassPermissionsModeAvailable")) == "true"
}
func powerShellWholeRule(context protocol.JSONObject, behavior string) (*hookRule, error) {
	rows, err := orderedHookRules(context, behavior)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if hookToolRuleMatches("PowerShell", rows[i]) {
			return &rows[i], nil
		}
	}
	return nil, nil
}
func powerShellNormalizePermission(result powershellpolicy.PermissionResult, input json.RawMessage, context protocol.JSONObject) (powershellpolicy.PermissionResult, error) {
	if result.Behavior == "deny" {
		return result, nil
	}
	var reason protocol.JSONObject
	_ = reason.UnmarshalJSON(raw(result.DecisionReason))
	ruleReason, _ := reason.Object("rule")
	if !powerShellBypass(context) && result.Behavior == "ask" && (reason.String("type") == "safetyCheck" || reason.String("type") == "rule" && ruleReason.String("ruleBehavior") == "ask") {
		return result, nil
	}
	updated := input
	if len(result.UpdatedInput) > 0 {
		updated = result.UpdatedInput
	}
	if powerShellBypass(context) {
		return powershellpolicy.PermissionResult{Behavior: "allow", UpdatedInput: updated, DecisionReason: map[string]string{"type": "mode", "mode": context.String("mode")}}, nil
	}
	if rule, err := powerShellWholeRule(context, "allow"); err != nil || rule != nil {
		if err != nil {
			return powershellpolicy.PermissionResult{}, err
		}
		return powershellpolicy.PermissionResult{Behavior: "allow", UpdatedInput: updated, DecisionReason: json.RawMessage(rule.reason())}, nil
	}
	if result.Behavior == "passthrough" {
		result.Behavior = "ask"
		result.Message = powershellpolicy.RequestMessage(result.DecisionReason)
	}
	return result, nil
}

func powerShellBareRepository(cwd string) bool {
	git, err := os.Stat(filepath.Join(cwd, ".git"))
	if err == nil {
		if git.Mode().IsRegular() {
			return false
		}
		if git.IsDir() {
			if head, e := os.Stat(filepath.Join(cwd, ".git", "HEAD")); e == nil && head.Mode().IsRegular() {
				return false
			}
		}
	}
	for _, name := range []string{"HEAD", "objects", "refs"} {
		if info, err := os.Stat(filepath.Join(cwd, name)); err == nil && (name == "HEAD" && info.Mode().IsRegular() || name != "HEAD" && info.IsDir()) {
			return true
		}
	}
	return false
}
