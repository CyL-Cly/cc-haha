package queryhooks

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Original src/types/hooks.ts hookJSONOutputSchema declarations, captured by
// native-hook-command.fixture.ts. Only generic types/constraints are embedded.
//
//go:embed data/hook-output.json
var hookOutputDeclaration []byte

//go:embed data/output-schema-hint.txt
var hookOutputHint string
var hookOutputOnce sync.Once
var hookOutputSchema *pluginschema.Node
var hookOutputSchemaError error

func HookOutputSchema() (*pluginschema.Node, error) {
	hookOutputOnce.Do(func() { hookOutputSchemaError = json.Unmarshal(hookOutputDeclaration, &hookOutputSchema) })
	return hookOutputSchema, hookOutputSchemaError
}

type CommandOutput struct {
	Backgrounded   bool
	AsyncProcess   AsyncProcess
	AsyncTimeout   float64
	AsyncRewake    bool
	AsyncProcessID string
	Stdout, Stderr string
	Output         string
	Code           int
	CodeAbsent     bool
	Aborted        bool
	DurationMillis float64
}
type CommandDecision struct {
	Async                                    bool
	Interpreted                              *JSONOutputResult
	PreventContinuation                      bool
	StopReason, BlockingError, SystemMessage string
	JSON                                     bool
	ValidationError                          string
	blockingJSON, stopReasonJSON, systemJSON json.RawMessage
}

// CommandStopDecision is shared by Stop/SubagentStop's actual process output
// consumer. Invalid JSON falls back to plain output, while valid JSON with a
// schema error is a non-blocking error exactly at the original parser stage.
func CommandStopDecision(command, event string, output CommandOutput) (CommandDecision, error) {
	decision := CommandDecision{}
	trimmed := strings.TrimFunc(output.Stdout, javaScriptWhitespace)
	if strings.HasPrefix(trimmed, "{") && json.Valid([]byte(trimmed)) {
		schema, e := HookOutputSchema()
		if e != nil {
			return decision, e
		}
		parsed, issues := pluginschema.Validate(schema, json.RawMessage(trimmed))
		if len(issues) > 0 {
			lines := []string{}
			for _, issue := range issues {
				parts := []string{}
				for _, part := range issue.Path {
					parts = append(parts, fmt.Sprint(part))
				}
				lines = append(lines, "  - "+strings.Join(parts, ".")+": "+issue.Message)
			}
			var pretty bytes.Buffer
			_ = json.Indent(&pretty, protocol.StringifyValidatedJavaScriptJSON(json.RawMessage(trimmed)), "", "  ")
			decision.ValidationError = "Hook JSON output validation failed:\n" + strings.Join(lines, "\n") + "\n\nThe hook's output was: " + pretty.String()
			decision.ValidationError += "\n\nExpected schema:\n" + hookOutputHint
			return decision, nil
		}
		var value protocol.JSONObject
		if e = value.UnmarshalJSON(parsed); e != nil {
			return decision, e
		}
		if string(value.Get("async")) == "true" {
			decision.Async = true
			return decision, nil
		}
		decision.JSON = true
		interpreted, e := InterpretJSONOutput(value, JSONOutputContext{Command: command, HookName: event, Event: event, ExpectedEvent: event, Stdout: &output.Stdout, Stderr: &output.Stderr, ExitCode: &output.Code, DurationMillis: &output.DurationMillis})
		if e != nil {
			return decision, e
		}
		fields := interpreted.Fields
		decision.Interpreted = &interpreted
		decision.PreventContinuation = string(fields.Get("preventContinuation")) == "true"
		decision.StopReason = fields.String("stopReason")
		decision.stopReasonJSON = fields.Get("stopReason")
		decision.SystemMessage = fields.String("systemMessage")
		decision.systemJSON = fields.Get("systemMessage")
		blocked, _ := fields.Object("blockingError")
		decision.BlockingError = blocked.String("blockingError")
		decision.blockingJSON = blocked.Get("blockingError")
		return decision, nil
	}
	if output.Code == 2 {
		decision.BlockingError = "[" + command + "]: " + output.Stderr
		if output.Stderr == "" {
			decision.BlockingError = "[" + command + "]: No stderr output"
		}
		decision.blockingJSON = commandJSON(decision.BlockingError)
	}
	return decision, nil
}
func prettyHookJSON(raw json.RawMessage) string {
	var pretty bytes.Buffer
	_ = json.Indent(&pretty, protocol.StringifyValidatedJavaScriptJSON(raw), "", "  ")
	return pretty.String()
}
