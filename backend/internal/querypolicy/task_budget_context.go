package querypolicy

import (
	"encoding/json"
	"errors"
	"math"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func taskBudgetObject(raw json.RawMessage) protocol.JSONObject {
	var object protocol.JSONObject
	_ = object.UnmarshalJSON(raw)
	return object
}

func taskBudgetToken(raw json.RawMessage) float64 {
	var value any
	_ = json.Unmarshal(raw, &value)
	number, ok := value.(float64)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
		return 0
	}
	return number
}

// This is finalContextTokensFromLastResponse, not billed token spend. Cache
// tokens are excluded and the last server-side iteration is authoritative.
func FinalTaskBudgetContext(history []protocol.JSONObject) (float64, error) {
	for i := len(history) - 1; i >= 0; i-- {
		record := history[i]
		message := taskBudgetObject(record.Get("message"))
		if record.String("type") != "assistant" || message.String("model") == "<synthetic>" {
			continue
		}
		var content []protocol.JSONObject
		_ = json.Unmarshal(message.Get("content"), &content)
		if len(content) > 0 && content[0].String("type") == "text" {
			switch content[0].String("text") {
			case "[Request interrupted by user]", "[Request interrupted by user for tool use]", "The user doesn't want to take this action right now. STOP what you are doing and wait for the user to tell you how to proceed.", "The user doesn't want to proceed with this tool use. The tool use was rejected (eg. if it was a file edit, the new_string was NOT written to the file). STOP what you are doing and wait for the user to tell you how to proceed.", "No response requested.":
				continue
			}
		}
		raw := message.Get("usage")
		if len(raw) == 0 || string(raw) == "null" || string(raw) == "false" || string(raw) == "0" || string(raw) == `""` {
			continue
		}
		usage := taskBudgetObject(raw)
		iterations := usage.Get("iterations")
		var rows []json.RawMessage
		if json.Unmarshal(iterations, &rows) == nil && len(rows) > 0 {
			last := rows[len(rows)-1]
			if string(last) == "null" {
				return 0, errors.New("null is not an object (evaluating 'last.input_tokens')")
			}
			window := taskBudgetObject(last)
			return taskBudgetToken(window.Get("input_tokens")) + taskBudgetToken(window.Get("output_tokens")), nil
		}
		if len(iterations) > 0 && iterations[0] == '"' {
			var text string
			_ = json.Unmarshal(iterations, &text)
			if text != "" {
				return 0, nil
			}
		}
		if object := taskBudgetObject(iterations); object.IsObject() && taskBudgetToken(object.Get("length")) > 0 {
			return 0, errors.New("iterations.at is not a function. (In 'iterations.at(-1)', 'iterations.at' is undefined)")
		}
		return taskBudgetToken(usage.Get("input_tokens")) + taskBudgetToken(usage.Get("output_tokens")), nil
	}
	return 0, nil
}
