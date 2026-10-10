package sdkhost

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
)

func nativeHookIfMatcher(tool queryloop.RegisteredTool, input *protocol.JSONObject) func(json.RawMessage) (bool, error) {
	return nativeHookIfMatcherWithOptions(tool, input, nativeHookIfOptions{})
}

type nativeHookIfOptions struct {
	TreeSitterBash bool
	BashMatcher    func(protocol.JSONObject) (func(json.RawMessage) (bool, error), error)
}

func nativeHookIfMatcherWithOptions(tool queryloop.RegisteredTool, input *protocol.JSONObject, options nativeHookIfOptions) func(json.RawMessage) (bool, error) {
	parsed, parseError := tool.ParseInput(input.Clone())
	var bash func(json.RawMessage) (bool, error)
	var bashError error
	if parseError == nil && tool.Name == "Bash" {
		if options.TreeSitterBash && options.BashMatcher != nil {
			bash, bashError = options.BashMatcher(parsed)
		} else {
			bash, bashError = nativeBashHookIfMatcher(context.Background(), parsed, options.TreeSitterBash)
		}
	}
	return func(condition json.RawMessage) (bool, error) {
		value, err := sdkpermissions.ParsePermissionRuleValue(condition)
		if err != nil {
			return false, err
		}
		if value.String("toolName") != tool.Name {
			return false, nil
		}
		pattern := value.Get("ruleContent")
		if !hookRawTruthy(pattern) {
			return true, nil
		}
		if parseError != nil {
			return false, nil
		}
		switch tool.Name {
		case "Read", "Write", "Edit":
			return sdkpermissions.MatchWildcardRule(pattern, parsed.Get("file_path"))
		case "Glob", "Grep":
			return sdkpermissions.MatchWildcardRule(pattern, parsed.Get("pattern"))
		case "Bash":
			// The original external build has no TREE_SITTER_BASH/SHADOW.
			// Its prepared matcher is fail-safe on unavailable parses, while
			// parseForSecurity still short-circuits an empty command to [].
			if bashError != nil {
				return false, bashError
			}
			if bash == nil {
				return false, cap("actual enabled Bash Hook if security AST/argv producer")
			}
			return bash(pattern)
		default:
			return false, nil
		}
	}
}
