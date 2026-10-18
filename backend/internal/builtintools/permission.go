package builtintools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func rules(c protocol.JSONObject, key string) map[string][]string {
	v := map[string][]string{}
	_ = json.Unmarshal(c.Get(key), &v)
	return v
}
func wholeDenied(c protocol.JSONObject, name string) bool {
	for _, list := range rules(c, "alwaysDenyRules") {
		for _, r := range list {
			if r == name || r == name+"(*)" || name == "Agent" && (r == "Task" || r == "Task(*)") {
				return true
			}
		}
	}
	return false
}
func wholeAllowed(c protocol.JSONObject, name string) bool {
	for _, list := range rules(c, "alwaysAllowRules") {
		for _, r := range list {
			if r == name || r == name+"(*)" || name == "Agent" && (r == "Task" || r == "Task(*)") {
				return true
			}
		}
	}
	return false
}
func under(root, p string) bool {
	rel, e := filepath.Rel(root, p)
	return e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func resolved(p string) string {
	for ancestor := p; ; ancestor = filepath.Dir(ancestor) {
		if r, e := filepath.EvalSymlinks(ancestor); e == nil {
			suffix, _ := filepath.Rel(ancestor, p)
			return filepath.Join(r, suffix)
		}
		if filepath.Dir(ancestor) == ancestor {
			return p
		}
	}
}
func (s *Service) deniedPath(c protocol.JSONObject, name, p string) (bool, error) {
	return s.pathRule(c, "alwaysDenyRules", name, p)
}
func (s *Service) toolPath(name string, in protocol.JSONObject) (string, error) {
	if name == "Read" {
		return s.readPath(in)
	}
	if name == "NotebookEdit" {
		return s.notebookPath(in)
	}
	key := "file_path"
	if name == "Glob" || name == "Grep" {
		key = "path"
	}
	return s.path(in, key)
}
func (s *Service) pathRule(c protocol.JSONObject, key, name, p string) (bool, error) {
	return s.matchPathRule(c, key, name, p, true)
}
func (s *Service) matchPathRule(c protocol.JSONObject, key, name, p string, canonical bool) (bool, error) {
	if s.options.Platform == "windows" {
		return s.matchWindowsPathRule(c, key, name, p)
	}
	read := name == "Read" || name == "Glob" || name == "Grep"
	ruleName := "Edit"
	if read {
		ruleName = "Read"
	}
	for source, list := range rules(c, key) {
		for _, rule := range list {
			if !strings.HasPrefix(rule, ruleName+"(") {
				continue
			}
			if !strings.HasSuffix(rule, ")") {
				return false, capability("malformed filesystem permission rule")
			}
			pattern := strings.TrimSuffix(strings.TrimPrefix(rule, ruleName+"("), ")")
			root := s.options.CWD
			switch {
			case strings.HasPrefix(pattern, "//"):
				root = "/"
				pattern = pattern[2:]
			case strings.HasPrefix(pattern, "~/"):
				root = s.options.Home
				pattern = pattern[2:]
			case strings.HasPrefix(pattern, "/"):
				root = s.options.PermissionRoots[source]
				pattern = pattern[1:]
				if root == "" {
					return false, capability("permission source root " + source)
				}
			}
			if strings.HasPrefix(pattern, "!") || strings.Contains(pattern, "\\") {
				return false, capability("negated/escaped filesystem permission matcher")
			}
			if matchPermissionPath(pattern, root, p) || canonical && matchPermissionPath(pattern, root, resolved(p)) {
				return true, nil
			}
		}
	}
	return false, nil
}
func matchPermissionPath(pattern, root, p string) bool {
	rel, e := filepath.Rel(root, p)
	if e != nil || !under(root, p) {
		return false
	}
	rel = filepath.ToSlash(rel)
	pattern = strings.TrimPrefix(pattern, "./")
	if strings.HasSuffix(pattern, "/**") {
		base := strings.TrimSuffix(pattern, "/**")
		if rel == base || strings.HasPrefix(rel, base+"/") {
			return true
		}
	}
	if !strings.Contains(pattern, "/") {
		for _, part := range strings.Split(rel, "/") {
			if globMatch(pattern, part) {
				return true
			}
		}
	}
	return globMatch(pattern, rel)
}
func globMatch(pattern, path string) bool {
	var out strings.Builder
	out.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					out.WriteString("(?:.*/)?")
				} else {
					out.WriteString(".*")
				}
			} else {
				out.WriteString("[^/]*")
			}
		case '?':
			out.WriteString("[^/]")
		case '[':
			j := strings.IndexByte(pattern[i+1:], ']')
			if j < 0 {
				out.WriteString(`\[`)
			} else {
				j += i + 1
				class := pattern[i : j+1]
				if strings.HasPrefix(class, "[!") {
					class = "[^" + class[2:]
				}
				out.WriteString(class)
				i = j
			}
		default:
			// The grammar indexes ASCII metacharacters by byte. Preserve the
			// original UTF-8 bytes for literal filenames rather than converting
			// each byte into a separate Unicode code point.
			out.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
		}
	}
	out.WriteString("$")
	r, e := regexp.Compile(out.String())
	return e == nil && r.MatchString(path)
}
func (s *Service) Permission(scope *cooperative.Scope, name string, in protocol.JSONObject) (string, error) {
	if name == "LSP" {
		d, e := s.lspPermission(scope, &in)
		return d.Behavior, e
	}
	if name == "PowerShell" {
		decision, err := s.powerShellPermissionDecision(scope, &in)
		return decision.Behavior, err
	}
	if e := s.check(scope); e != nil {
		return "", e
	}
	if imageTool(name) {
		return "allow", nil
	}
	if backgroundTaskTool(name) {
		c, e := s.options.PermissionContext(scope)
		if e != nil {
			return "", e
		}
		if wholeDenied(c, name) {
			return "deny", nil
		}
		mode := c.String("mode")
		bypass := mode == "bypassPermissions" || mode == "plan" && string(c.Get("isBypassPermissionsModeAvailable")) == "true"
		if !bypass {
			for _, list := range rules(c, "alwaysAskRules") {
				for _, rule := range list {
					if rule == name || rule == name+"(*)" {
						return "ask", nil
					}
				}
			}
		}
		return "allow", nil
	}
	if name == "Skill" {
		decision, e := s.skillPermission(scope, &in)
		return decision.Behavior, e
	}
	if mcpResourceTool(name) {
		return s.mcpResourcePermission(scope, name)
	}
	if name == "ToolSearch" {
		decision, e := s.mcpResourcePermissionDecision(scope, name, &in)
		return decision.Behavior, e
	}
	if isWebTool(name) {
		decision, e := s.webPermissionDecision(scope, name, &in)
		return decision.Behavior, e
	}
	c, e := s.options.PermissionContext(scope)
	if e != nil {
		return "", e
	}
	if wholeDenied(c, name) {
		return "deny", nil
	}
	if name == "AskUserQuestion" {
		return "ask", nil
	}
	if name == "ExitPlanMode" {
		if p := s.options.PlanMode; p != nil && p.Teammate != nil {
			teammate, _, err := p.Teammate(scope)
			if err != nil {
				return "", err
			}
			if teammate {
				return "allow", nil
			}
		}
		return "ask", nil
	}
	bashRuleAllow := false
	if name == "Bash" {
		hasSpecific := false
		for _, key := range []string{"alwaysDenyRules", "alwaysAskRules", "alwaysAllowRules"} {
			for _, list := range rules(c, key) {
				for _, rule := range list {
					if strings.HasPrefix(rule, "Bash(") {
						hasSpecific = true
					}
				}
			}
		}
		if hasSpecific {
			decision, err := s.bashGitRuleDecision(c, in)
			if err != nil {
				return "", err
			}
			if decision == "deny" || decision == "ask" {
				return decision, nil
			}
			bashRuleAllow = decision == "allow"
		}
	}
	if name != "Bash" && name != "Agent" && name != "TodoWrite" && name != "SendMessage" && !taskTool(name) && !planTool(name) && !worktreeTool(name) && !teamTool(name) {
		p, e := s.toolPath(name, in)
		if e != nil {
			return "", e
		}
		denied, e := s.deniedPath(c, name, p)
		if e != nil {
			return "", e
		}
		if denied {
			return "deny", nil
		}
	}
	mode := c.String("mode")
	bypass := mode == "bypassPermissions" || mode == "plan" && string(c.Get("isBypassPermissionsModeAvailable")) == "true"
	for _, list := range rules(c, "alwaysAskRules") {
		for _, rule := range list {
			if !bypass && (rule == name || rule == name+"(*)" || name == "Agent" && (rule == "Task" || rule == "Task(*)")) {
				return "ask", nil
			}
		}
	}
	if !bypass && name != "Bash" && name != "Agent" && name != "TodoWrite" && name != "SendMessage" && !taskTool(name) && !planTool(name) && !worktreeTool(name) && !teamTool(name) {
		p, e := s.toolPath(name, in)
		if e != nil {
			return "", e
		}
		ask, e := s.pathRule(c, "alwaysAskRules", name, p)
		if e != nil {
			return "", e
		}
		if ask {
			return "ask", nil
		}
	}
	if !bypass && !wholeAllowed(c, name) && (name == "Read" || name == "Glob" || name == "Grep") {
		p, e := s.toolPath(name, in)
		if e != nil {
			return "", e
		}
		if s.readPathSafety(c, p) != "" {
			return "ask", nil
		}
	}
	if s.HasPlanBindings() && (name == "Read" || name == "Glob" || name == "Grep" || name == "Edit" || name == "Write" || name == "NotebookEdit") {
		path, e := s.toolPath(name, in)
		if e != nil {
			return "", e
		}
		allowed, e := s.options.PlanMode.Plans.IsSessionFile(scope, path)
		if e != nil {
			return "", e
		}
		if allowed {
			return "allow", nil
		}
	}
	if bypass || wholeAllowed(c, name) || bashRuleAllow {
		return "allow", nil
	}
	if name == "Agent" || name == "SendMessage" || name == "TodoWrite" || taskTool(name) || name == "EnterPlanMode" || worktreeTool(name) || teamTool(name) {
		return "allow", nil
	}
	if name == "Read" || name == "Glob" || name == "Grep" {
		key := "file_path"
		if name == "Glob" || name == "Grep" {
			key = "path"
		}
		p, e := s.path(in, key)
		if name == "Read" {
			p, e = s.readPath(in)
		}
		if e != nil {
			return "", e
		}
		if s.pathInAllowedWorkingPath(c, p) {
			return "allow", nil
		}
		if s.options.InternalReadable != nil {
			allowed, _, err := s.options.InternalReadable(scope, p)
			if err != nil {
				return "", err
			}
			if allowed {
				return "allow", nil
			}
		}
		allowed, e := s.pathRule(c, "alwaysAllowRules", name, p)
		if e != nil {
			return "", e
		}
		if allowed {
			return "allow", nil
		}
		return "ask", nil
	}
	if name == "Edit" || name == "Write" || name == "NotebookEdit" {
		p, e := s.toolPath(name, in)
		if e != nil {
			return "", e
		}
		allowed, e := s.pathRule(c, "alwaysAllowRules", name, p)
		if e != nil {
			return "", e
		}
		if allowed {
			return "allow", nil
		}
	}
	if mode == "acceptEdits" && (name == "Edit" || name == "Write" || name == "NotebookEdit") {
		p, e := s.toolPath(name, in)
		if e == nil && s.pathInAllowedWorkingPath(c, p) {
			return "allow", nil
		}
	}
	return "ask", nil
}

// PermissionDecision retains the Source resolver's model-visible denial text.
// The behavior-only seam remains for the original finite Write profile.
func (s *Service) PermissionDecision(scope *cooperative.Scope, name string, in *protocol.JSONObject) (queryloop.ToolPermission, error) {
	if name == "LSP" {
		return s.lspPermission(scope, in)
	}
	if name == "PowerShell" {
		return s.powerShellPermissionDecision(scope, in)
	}
	if name == "Read" {
		if err := s.check(scope); err != nil {
			return queryloop.ToolPermission{}, err
		}
		original, err := s.path(*in, "file_path")
		if err != nil {
			return queryloop.ToolPermission{}, err
		}
		path, err := s.readPath(*in)
		if err != nil {
			return queryloop.ToolPermission{}, err
		}
		if in.Get("file_path") != nil && path != original {
			in.Set("file_path", quote(path))
		}
	}
	if name == "Bash" {
		decision, e := s.sandboxAutoPermission(scope, in)
		if e != nil {
			return queryloop.ToolPermission{}, e
		}
		if decision != nil {
			return *decision, nil
		}
	}
	if imageTool(name) {
		if err := s.check(scope); err != nil {
			return queryloop.ToolPermission{}, err
		}
		return imagePermission(in), nil
	}
	if name == "Workflow" {
		return s.workflowPermission(scope, in)
	}
	if sessionTool(name) {
		return s.sessionPermission(scope, name, in)
	}
	if mcpResourceTool(name) {
		return s.mcpResourcePermissionDecision(scope, name, in)
	}
	if name == "ToolSearch" {
		return s.mcpResourcePermissionDecision(scope, name, in)
	}
	if name == "Skill" {
		return s.skillPermission(scope, in)
	}
	if isWebTool(name) {
		return s.webPermissionDecision(scope, name, in)
	}
	behavior, err := s.Permission(scope, name, *in)
	decision := queryloop.ToolPermission{Behavior: behavior, Input: in}
	if err == nil && name == "AskUserQuestion" && behavior == "ask" {
		decision.Message = quote("Answer questions?")
	}
	if err == nil && name == "ExitPlanMode" && behavior == "ask" {
		decision.Message = quote("Exit plan mode?")
	}
	if err == nil && behavior == "ask" && (name == "Read" || name == "Glob" || name == "Grep") {
		c, e := s.options.PermissionContext(scope)
		if e != nil {
			return queryloop.ToolPermission{}, e
		}
		wholeAsk := false
		for _, list := range rules(c, "alwaysAskRules") {
			for _, rule := range list {
				if rule == name || rule == name+"(*)" {
					wholeAsk = true
				}
			}
		}
		if wholeAsk {
			decision.Message = quote("Claude requested permissions to use " + name + ", but you haven't granted it yet.")
		} else {
			key, action := "file_path", "read from"
			if name == "Glob" || name == "Grep" {
				key = "path"
			}
			// Source tool.getPath uses the caller's original input string;
			// permission messages must not replace it with an expanded path.
			suffix := ", but you haven't granted it yet."
			if name == "Read" || name == "Glob" || name == "Grep" {
				path, e := s.toolPath(name, *in)
				if e != nil {
					return queryloop.ToolPermission{}, e
				}
				switch s.readPathSafety(c, path) {
				case "unc":
					suffix = ", which appears to be a UNC path that could access network resources."
				case "windows":
					suffix = ", which contains a suspicious Windows path pattern that requires manual approval."
				}
			}
			decision.Message, e = protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quote("Claude requested permissions to " + action + " "), in.Get(key), quote(suffix)}, "")
			if e != nil {
				return queryloop.ToolPermission{}, e
			}
		}
	}
	if err != nil || behavior != "deny" {
		return decision, err
	}
	c, err := s.options.PermissionContext(scope)
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	detail := "Permission to use " + name + " has been denied."
	if !wholeDenied(c, name) && name != "Bash" && name != "Agent" && name != "TodoWrite" && name != "SendMessage" && name != "AskUserQuestion" && !taskTool(name) {
		action := "edit"
		if name == "Read" || name == "Glob" || name == "Grep" {
			action = "read"
		}
		p, err := s.toolPath(name, *in)
		if err != nil {
			return queryloop.ToolPermission{}, err
		}
		detail = "Permission to " + action + " " + p + " has been denied."
	}
	decision.Message = raw(detail)
	return decision, nil
}
func (s *Service) Validate(ctx context.Context, name string, in protocol.JSONObject, scope *cooperative.Scope) error {
	if e := s.check(scope); e != nil {
		return e
	}
	if e := context.Cause(ctx); e != nil {
		return e
	}
	if imageTool(name) {
		return nil
	}
	if teamTool(name) {
		if !s.HasTeamBindings() {
			return capability("actual Team lifecycle")
		}
		if name == "TeamCreate" && protocol.TrimJavaScriptString(in.String("team_name")) == "" {
			return &builtinwrite.ValidationError{Message: "team_name is required for TeamCreate", ErrorCode: 9}
		}
		return nil
	}
	if name == "SendMessage" {
		return s.validateSendMessage(in)
	}
	if backgroundTaskTool(name) {
		return s.validateBackgroundTask(scope, name, in)
	}
	if name == "WebFetch" {
		return validateWebFetch(in)
	}
	if name == "WebSearch" {
		return validateWebSearch(in)
	}
	if name == "AskUserQuestion" {
		return validateQuestionHTML(in, s.options.QuestionPreviewFormat)
	}
	if worktreeTool(name) {
		return s.validateWorktree(ctx, name, in, scope)
	}
	if planTool(name) {
		return s.validatePlanMode(scope, name)
	}
	if name == "Skill" {
		return s.validateSkill(scope, in)
	}
	if mcpResourceTool(name) {
		if !s.HasMCPResourceBindings() {
			return capability("actual connected MCP resources")
		}
		return nil
	}
	if name == "ToolSearch" {
		if !s.HasToolSearchBindings() {
			return capability("actual shared tool search binding")
		}
		return nil
	}
	if sessionTool(name) {
		if !s.HasSessionCollaborationBindings() {
			return capability("actual authenticated desktop collaboration bridge")
		}
		return nil
	}
	if name == "Bash" {
		command, e := text(in.Get("command"))
		if e != nil {
			return e
		}
		if strings.TrimSpace(command) == "" {
			return errors.New("Command must not be empty")
		}
		return nil
	}
	if name == "Agent" || name == "TodoWrite" || taskTool(name) {
		return nil
	}
	if name == "NotebookEdit" {
		return s.validateNotebook(in, scope)
	}
	key := "file_path"
	if name == "Glob" || name == "Grep" {
		key = "path"
	}
	p, e := s.path(in, key)
	if e != nil {
		return e
	}
	if name == "Edit" {
		return s.validateEdit(in, scope, p)
	}
	if name == "Read" {
		return s.validateRead(in, scope, p)
	}
	if (name == "Glob" || name == "Grep") && in.Get("path") == nil {
		return nil
	}
	st, e := os.Stat(p)
	if e != nil {
		if os.IsNotExist(e) {
			kind := "File"
			if name == "Glob" {
				kind = "Directory"
			}
			return fmt.Errorf("%s does not exist: %s. Note: your current working directory is %s.", kind, p, s.options.CWD)
		}
		return e
	}
	if name == "Glob" && !st.IsDir() {
		return fmt.Errorf("Path is not a directory: %s", p)
	}
	return nil
}
