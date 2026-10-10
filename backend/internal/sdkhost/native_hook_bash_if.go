package sdkhost

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/bashsyntax"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
)

func nativeBashHookIfMatcher(ctx context.Context, input protocol.JSONObject, enabled bool) (func(json.RawMessage) (bool, error), error) {
	parsed, err := bashsyntax.ParseForSecurity(ctx, input.Get("command"), bashsyntax.SecurityOptions{Enabled: enabled})
	if err != nil {
		return nil, err
	}
	if parsed.Kind != "simple" {
		return func(json.RawMessage) (bool, error) { return true, nil }, nil
	}
	commands := make([]json.RawMessage, 0, len(parsed.Commands))
	for _, cmd := range parsed.Commands {
		args := make([]json.RawMessage, len(cmd.Argv))
		for i, arg := range cmd.Argv {
			args[i] = arg.JSON()
		}
		joined, err := protocol.JoinJavaScriptValuesJSON(args, " ")
		if err != nil {
			return nil, err
		}
		commands = append(commands, joined)
	}
	return func(pattern json.RawMessage) (bool, error) {
		units, err := protocol.DecodeJavaScriptJSONString(pattern)
		if err != nil {
			return false, err
		}
		var prefix []uint16
		if len(units) > 2 && units[len(units)-2] == ':' && units[len(units)-1] == '*' {
			prefix = units[:len(units)-2]
			for _, c := range prefix {
				if c == '\n' || c == '\r' || c == 0x2028 || c == 0x2029 {
					prefix = nil
					break
				}
			}
		}
		for _, command := range commands {
			if prefix != nil {
				value, err := protocol.DecodeJavaScriptJSONString(command)
				if err != nil {
					return false, err
				}
				if nativeHookUnitsPrefix(value, prefix) && (len(value) == len(prefix) || len(value) > len(prefix) && value[len(prefix)] == ' ') {
					return true, nil
				}
				continue
			}
			matched, err := sdkpermissions.MatchWildcardRule(pattern, command)
			if err != nil {
				return false, err
			}
			if matched {
				return true, nil
			}
		}
		return false, nil
	}, nil
}
func nativeHookUnitsPrefix(value, prefix []uint16) bool {
	if len(value) < len(prefix) {
		return false
	}
	for i, c := range prefix {
		if value[i] != c {
			return false
		}
	}
	return true
}
