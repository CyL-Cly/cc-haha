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
)

func run() error {
	var input struct {
		HomeDir string `json:"homeDir"`
		Steps   []struct {
			Operation string                  `json:"operation"`
			Options   storage.Object          `json:"options"`
			ToolUse   bool                    `json:"toolUse"`
			Input     json.RawMessage         `json:"input"`
			Preceding []json.RawMessage       `json:"preceding"`
			A         storage.Object          `json:"a"`
			B         storage.Object          `json:"b"`
			BlocksA   []messages.ContentBlock `json:"blocksA"`
			BlocksB   []messages.ContentBlock `json:"blocksB"`
			Universal bool                    `json:"universal"`
			History   []storage.Object        `json:"history"`
			Strict    bool                    `json:"strict"`
		} `json:"steps"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		return err
	}
	if input.HomeDir == "" || input.HomeDir != os.Getenv("HOME") || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(input.HomeDir, ".claude") {
		return errors.New("message fixture requires explicit disposable HOME/config")
	}
	calls := 0
	factory, err := messages.NewFactory(func() string { calls++; return fmt.Sprintf("fixture-id-%d", calls) }, func() time.Time { return time.UnixMilli(1700000000000) })
	if err != nil {
		return err
	}
	results := []any{}
	contentBytes := []any{}
	for _, step := range input.Steps {
		var value any
		switch step.Operation {
		case "user":
			value = factory.CreateUser(step.Options)
		case "assistant":
			value = factory.CreateAssistant(step.Options)
		case "api-error":
			value = factory.CreateAssistantAPIError(step.Options)
		case "interrupt":
			value = factory.CreateUserInterruption(step.ToolUse)
		case "prepare":
			value = messages.PrepareUserContent(step.Input, step.Preceding)
		case "merge-content":
			merged, failure := messages.MergeUserContentBlocks(step.BlocksA, step.BlocksB, step.Universal)
			if failure != nil {
				return failure
			}
			value = map[string]any{"merged": merged, "blocksAAfter": step.BlocksA, "blocksBAfter": step.BlocksB}
		case "reorder-tools":
			value = map[string]any{"merged": messages.ReorderAssistantToolUseBlocks(step.BlocksA), "blocksAAfter": step.BlocksA}
		case "merge-user", "merge-user-tools", "merge-assistant":
			var merged storage.Object
			var failure error
			switch step.Operation {
			case "merge-user":
				merged, failure = messages.MergeUserMessages(step.A, step.B)
			case "merge-user-tools":
				merged, failure = messages.MergeUserMessagesAndToolResults(step.A, step.B, step.Universal)
			case "merge-assistant":
				merged, failure = messages.MergeAssistantMessages(step.A, step.B)
			}
			if failure != nil {
				return failure
			}
			value = map[string]any{"merged": merged, "aAfter": step.A, "bAfter": step.B}
		case "pairing":
			paired, failure := messages.EnsureToolResultPairing(step.History, factory, messages.PairingOptions{Strict: step.Strict, UniversalSmoosh: step.Universal})
			projection := map[string]any{"historyAfter": step.History}
			if failure != nil {
				var strict *messages.PairingError
				if !errors.As(failure, &strict) {
					return failure
				}
				projection["error"] = strict.MessageJSON
			} else {
				projection["messages"] = paired.Messages
				if paired.Repair != nil {
					projection["repairEvent"] = map[string]any{"messageCount": paired.Repair.MessageCount, "repairedMessageCount": paired.Repair.RepairedMessageCount, "messageTypes": paired.Repair.MessageTypes}
					projection["diagnostic"] = paired.Repair.Diagnostic
				}
			}
			value = projection
		default:
			return fmt.Errorf("unsupported message operation: %s", step.Operation)
		}
		results = append(results, value)
		var content json.RawMessage
		switch step.Operation {
		case "prepare":
			content = value.(json.RawMessage)
		case "merge-content", "reorder-tools":
			content, _ = json.Marshal(value.(map[string]any)["merged"])
		case "pairing":
			if history, exists := value.(map[string]any)["messages"].([]storage.Object); exists {
				contents := []json.RawMessage{}
				for _, message := range history {
					var api storage.Object
					if err := json.Unmarshal(message["message"], &api); err != nil {
						return err
					}
					contents = append(contents, api["content"])
				}
				content, _ = json.Marshal(contents)
			}
		default:
			message, ok := value.(storage.Object)
			if !ok {
				message = value.(map[string]any)["merged"].(storage.Object)
			}
			var api storage.Object
			if err := json.Unmarshal(message["message"], &api); err != nil {
				return err
			}
			content = api["content"]
		}
		if content == nil {
			contentBytes = append(contentBytes, nil)
		} else {
			normalized, err := protocol.ParseAndStringifyJavaScriptJSON(content)
			if err != nil {
				return err
			}
			contentBytes = append(contentBytes, string(normalized))
		}
	}
	raw, err := json.Marshal(map[string]any{"results": results, "uuidCalls": calls, "contentBytes": contentBytes})
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
