package builtintools

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionrewind"
)

func (s *Service) WithAutoMemoryFork(scope *cooperative.Scope) (*Service, error) {
	if !scope.IsNativeAutoDream(s.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	cache, err := s.options.Cache.CloneOnOwner(scope)
	if err != nil {
		return nil, err
	}
	writer, err := s.options.Write.WithForkCache(scope, cache)
	if err != nil {
		return nil, err
	}
	clone := *s
	clone.options.Cache, clone.options.Write = cache, writer
	clone.agentSnapshot = append(clone.agentSnapshot[:0:0], s.agentSnapshot...)
	return &clone, nil
}

// AutoMemoryPermission is createAutoMemCanUseTool's override. It retains the
// parent's advertised tool set, and enforces the fork's authority at execution.
func (s *Service) AutoMemoryPermission(_ context.Context, scope *cooperative.Scope, memoryRoot string, tool queryloop.RegisteredTool, input *protocol.JSONObject) (queryloop.ToolPermission, error) {
	if !scope.IsNativeAutoDream(s.options.Owner) || input == nil {
		return queryloop.ToolPermission{}, cooperative.ErrRestricted
	}
	allow := func() (queryloop.ToolPermission, error) {
		return queryloop.ToolPermission{Behavior: "allow", Input: input, UpdatedInput: input}, nil
	}
	deny := func(reason string) (queryloop.ToolPermission, error) {
		return queryloop.ToolPermission{Behavior: "deny", Message: quote(reason), DecisionReason: raw(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("other")}, protocol.JSONField{Name: "reason", Value: quote(reason)}))}, nil
	}
	switch tool.Name {
	case "REPL", "Read", "Grep", "Glob":
		return allow()
	case "Bash":
		parsed, err := tool.ParseInput(*input)
		if err == nil {
			// BashTool.isReadOnly's all-commands predicate accepts an empty
			// successfully parsed command list; the recorded-rewind classifier
			// intentionally excludes that no-op from its replay decision.
			if protocol.TrimJavaScriptString(parsed.String("command")) == "" {
				return allow()
			}
			classifier, err := sessionrewind.NewRecordedCommandClassifier(s.options.Platform, s.options.Environment["USER_TYPE"])
			if err != nil {
				return queryloop.ToolPermission{}, err
			}
			allowed, err := classifier.Classify(parsed.String("command"))
			if err != nil {
				return queryloop.ToolPermission{}, err
			}
			if allowed {
				return allow()
			}
		}
		return deny("Only read-only shell commands are permitted in this context (ls, find, grep, cat, stat, wc, head, tail, and similar)")
	case "Edit", "Write":
		if value := input.Get("file_path"); value != nil && value[0] == '"' && strings.HasPrefix(filepath.Clean(input.String("file_path")), memoryRoot) {
			return allow()
		}
	}
	return deny("only Read, Grep, Glob, read-only Bash, and Edit/Write within " + memoryRoot + " are allowed")
}
