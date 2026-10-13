package queryhooks

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
	"time"
)

type CompactHookResult struct{ Instructions, Display string }

func (s *Service) ExecuteCompactHooks(ctx context.Context, event, trigger, custom, summary string, call Context) (CompactHookResult, error) {
	result := CompactHookResult{}
	fields := protocol.NewJSONObject(protocol.JSONField{Name: "trigger", Value: commandJSON(trigger)})
	if event == "PreCompact" {
		value := commandJSON(custom)
		if custom == "" {
			value = commandJSON(nil)
		}
		fields.Set("custom_instructions", value)
	} else if event == "PostCompact" {
		fields.Set("compact_summary", commandJSON(summary))
	} else {
		return result, unsupported("compact hook event " + event)
	}
	rows, err := s.ExecuteOutside(ctx, OutsideInput{Event: event, SessionID: s.mainSessionID(), MatchQuery: trigger, Fields: fields, Call: call, Timeout: 600 * time.Second})
	if err != nil {
		return result, err
	}
	successful, display := []string{}, []string{}
	for _, row := range rows {
		command := protocol.NewJSONObject(protocol.JSONField{Name: "command", Value: row.Command}).String("command")
		output := strings.TrimFunc(protocol.NewJSONObject(protocol.JSONField{Name: "output", Value: row.Output}).String("output"), javaScriptWhitespace)
		state := "failed"
		if row.Succeeded {
			state = "completed successfully"
			if output != "" {
				successful = append(successful, output)
			}
		}
		text := event + " [" + command + "] " + state
		if output != "" {
			text += ": " + output
		}
		display = append(display, text)
	}
	result.Display = strings.Join(display, "\n")
	if event == "PreCompact" {
		result.Instructions = strings.Join(successful, "\n\n")
	}
	return result, nil
}

func (s *Service) ApplyCompactWatchPaths(scope *cooperative.Scope, paths []json.RawMessage) error {
	if len(paths) == 0 {
		return nil
	}
	if s.options.UpdateWatchPaths == nil {
		return unsupported("actual SessionStart watchPaths consumer")
	}
	return s.options.UpdateWatchPaths(scope, paths)
}
