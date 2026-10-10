package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolfeedback"
)

func run() error {
	var input struct {
		HomeDir string `json:"homeDir"`
		Steps   []struct {
			Operation   string          `json:"operation"`
			Raw         json.RawMessage `json:"raw"`
			InputSource string          `json:"inputSource"`
			ToolName    json.RawMessage `json:"toolName"`
			ToolID      json.RawMessage `json:"toolID"`
			SourceUUID  json.RawMessage `json:"sourceUUID"`
		} `json:"steps"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		return err
	}
	if input.HomeDir == "" || input.HomeDir != os.Getenv("HOME") || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(input.HomeDir, ".claude") {
		return errors.New("tool feedback fixture requires explicit disposable HOME/config")
	}
	calls := 0
	factory, err := messages.NewFactory(func() string { calls++; return fmt.Sprintf("fixture-id-%d", calls) }, func() time.Time { return time.UnixMilli(1700000000000) })
	if err != nil {
		return err
	}
	results := []any{}
	for _, step := range input.Steps {
		use := storage.Object{"type": json.RawMessage(`"tool_use"`), "name": step.ToolName, "id": step.ToolID, "input": json.RawMessage(step.InputSource)}
		if step.Raw != nil {
			use["input"], err = toolfeedback.MarkerFromJSONText(step.Raw)
			if err != nil {
				return err
			}
		}
		var value any
		switch step.Operation {
		case "create-marker":
			value = use["input"]
		case "is-marker":
			value = toolfeedback.IsUnparsedToolInput(use["input"])
		case "invalid":
			message, handled, failure := toolfeedback.InvalidInput(factory, use, step.SourceUUID)
			if failure != nil {
				return failure
			}
			if !handled {
				return errors.New("invalid feedback fixture requires a recognized marker")
			}
			value = map[string]any{"message": message}
		case "unknown":
			message, failure := toolfeedback.UnknownTool(factory, use, step.SourceUUID)
			if failure != nil {
				return failure
			}
			value = map[string]any{"message": message}
		default:
			return fmt.Errorf("unsupported tool feedback operation: %s", step.Operation)
		}
		results = append(results, value)
	}
	raw, err := json.Marshal(map[string]any{"results": results, "uuidCalls": calls})
	if err != nil {
		return err
	}
	serialized, err := protocol.ParseAndStringifyJavaScriptJSON(raw)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, string(serialized))
	return err
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
