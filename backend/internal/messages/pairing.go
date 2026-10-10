package messages

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

const SyntheticToolResultPlaceholder = "[Tool result missing due to internal error]"

type PairingOptions struct {
	Strict          bool
	UniversalSmoosh bool
}

type PairingRepair struct {
	MessageCount         int
	RepairedMessageCount int
	MessageTypes         json.RawMessage
	Diagnostic           json.RawMessage
}

type PairingResult struct {
	Messages []storage.Object
	Repair   *PairingRepair
}

// PairingError retains the original JS string for history/API error envelopes.
// Error() is the replacement-character representation used by terminal output.
type PairingError struct{ MessageJSON json.RawMessage }

func (err *PairingError) Error() string { return rawStringField(err.MessageJSON) }

func idKey(raw json.RawMessage) string {
	if units, err := protocol.DecodeJavaScriptJSONString(raw); err == nil {
		return string(protocol.QuoteJavaScriptUTF16(units))
	}
	// Normalized SDK tool IDs are strings. Retain missing/null distinctions for
	// old imported envelopes without introducing an extra rejection policy.
	if raw == nil {
		return "undefined"
	}
	if len(raw) > 0 && (raw[0] == '-' || raw[0] >= '0' && raw[0] <= '9') {
		number, _ := strconv.ParseFloat(string(raw), 64)
		if number == 0 {
			return "number:0"
		}
		return "number:" + strconv.FormatFloat(number, 'g', -1, 64)
	}
	canonical, err := protocol.ParseAndStringifyJavaScriptJSON(raw)
	if err != nil {
		return string(raw)
	}
	return string(canonical)
}

func setMessageContent(message, api storage.Object, content []ContentBlock) storage.Object {
	copy := cloneObject(message)
	copyAPI := cloneObject(api)
	copyAPI["content"], _ = json.Marshal(content)
	copy["message"] = objectJSON(copyAPI)
	return copy
}

func messageAPI(message storage.Object) (storage.Object, error) {
	api, valid, err := storage.NormalizeObject(message["message"])
	if err != nil || !valid {
		return nil, errors.New("tool pairing requires normalized message envelopes")
	}
	return api, nil
}

func smooshSystemReminderSiblings(message storage.Object, api storage.Object, content []ContentBlock) (storage.Object, error) {
	lastResult := -1
	kept, reminders := []ContentBlock{}, []ContentBlock{}
	for _, block := range content {
		if blockType(block) == "text" && strings.HasPrefix(rawStringField(block.Get("text")), "<system-reminder>") {
			reminders = append(reminders, block)
		} else {
			kept = append(kept, block)
			if blockType(block) == "tool_result" {
				lastResult = len(kept) - 1
			}
		}
	}
	if lastResult < 0 || len(reminders) == 0 {
		return message, nil
	}
	smooshed, possible, err := smooshIntoToolResult(kept[lastResult], reminders)
	if err != nil || !possible {
		return message, err
	}
	kept[lastResult] = smooshed
	return setMessageContent(message, api, kept), nil
}

func joinJSON(parts ...json.RawMessage) json.RawMessage {
	raw, _ := protocol.JoinJavaScriptValuesJSON(parts, "")
	return raw
}

func pairingStructure(input []storage.Object) (json.RawMessage, error) {
	rows := make([]json.RawMessage, 0, len(input))
	for index, message := range input {
		prefix := stringJSON(fmt.Sprintf("[%d] ", index))
		typeName := rawStringField(message["type"])
		if typeName != "assistant" && typeName != "user" {
			rows = append(rows, joinJSON(prefix, message["type"]))
			continue
		}
		api, err := messageAPI(message)
		if err != nil {
			return nil, err
		}
		if typeName == "user" && isString(api["content"]) {
			rows = append(rows, joinJSON(prefix, stringJSON("user")))
			continue
		}
		content, err := contentBlocks(api["content"])
		if err != nil {
			return nil, err
		}
		uses, servers, results := []json.RawMessage{}, []json.RawMessage{}, []json.RawMessage{}
		for _, block := range content {
			switch blockType(block) {
			case "tool_use":
				uses = append(uses, block.Get("id"))
			case "server_tool_use", "mcp_tool_use":
				servers = append(servers, block.Get("id"))
			case "tool_result":
				results = append(results, block.Get("tool_use_id"))
			}
		}
		if typeName == "assistant" {
			id := api["id"]
			if id == nil {
				id = stringJSON("undefined")
			} else if string(id) == "null" {
				id = stringJSON("null")
			}
			usesText, _ := protocol.JoinJavaScriptValuesJSON(uses, ",")
			row := joinJSON(prefix, stringJSON("assistant(id="), id, stringJSON(", tool_uses=["), usesText, stringJSON("]"))
			if len(servers) > 0 {
				serverText, _ := protocol.JoinJavaScriptValuesJSON(servers, ",")
				row = joinJSON(row, stringJSON(", server_tool_uses=["), serverText, stringJSON("]"))
			}
			rows = append(rows, joinJSON(row, stringJSON(")")))
		} else if len(results) > 0 {
			resultText, _ := protocol.JoinJavaScriptValuesJSON(results, ",")
			rows = append(rows, joinJSON(prefix, stringJSON("user(tool_results=["), resultText, stringJSON("])")))
		} else {
			rows = append(rows, joinJSON(prefix, stringJSON("user")))
		}
	}
	return protocol.JoinJavaScriptValuesJSON(rows, "; ")
}

// EnsureToolResultPairing ports the repair immediately before request mapping.
// Input is the current build's normalized user/assistant history. Callers must
// publish Repair's diagnostic/event through the shared Go session controller.
func EnsureToolResultPairing(input []storage.Object, factory *Factory, options PairingOptions) (PairingResult, error) {
	result := make([]storage.Object, 0, len(input))
	repaired := false
	allSeen := map[string]bool{}
	for index := 0; index < len(input); index++ {
		message := input[index]
		typeName := rawStringField(message["type"])
		if typeName != "assistant" {
			if typeName == "user" {
				api, err := messageAPI(message)
				if err != nil {
					return PairingResult{}, err
				}
				precededByAssistant := len(result) > 0 && rawStringField(result[len(result)-1]["type"]) == "assistant"
				if !isString(api["content"]) && !precededByAssistant {
					content, err := contentBlocks(api["content"])
					if err != nil {
						return PairingResult{}, err
					}
					stripped := make([]ContentBlock, 0, len(content))
					for _, block := range content {
						if blockType(block) != "tool_result" {
							stripped = append(stripped, block)
						}
					}
					if len(stripped) != len(content) {
						repaired = true
						if len(stripped) == 0 && len(result) == 0 {
							stripped = []ContentBlock{textBlock(stringJSON("[Orphaned tool result removed due to conversation resume]"))}
						}
						if len(stripped) > 0 {
							result = append(result, setMessageContent(message, api, stripped))
						}
						continue
					}
				}
			}
			result = append(result, message)
			continue
		}
		api, err := messageAPI(message)
		if err != nil {
			return PairingResult{}, err
		}
		content, err := contentBlocks(api["content"])
		if err != nil {
			return PairingResult{}, err
		}
		serverResults := map[string]bool{}
		for _, block := range content {
			if isString(block.Get("tool_use_id")) {
				serverResults[idKey(block.Get("tool_use_id"))] = true
			}
		}
		seen, useIDs := map[string]bool{}, []json.RawMessage{}
		finalContent := make([]ContentBlock, 0, len(content))
		for _, block := range content {
			typeName := blockType(block)
			if typeName == "tool_use" {
				key := idKey(block.Get("id"))
				if allSeen[key] {
					repaired = true
					continue
				}
				allSeen[key], seen[key] = true, true
				useIDs = append(useIDs, block.Get("id"))
			}
			if (typeName == "server_tool_use" || typeName == "mcp_tool_use") && !serverResults[idKey(block.Get("id"))] {
				repaired = true
				continue
			}
			finalContent = append(finalContent, block)
		}
		changed := len(finalContent) != len(content)
		if len(finalContent) == 0 {
			finalContent = append(finalContent, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: stringJSON("text")}, protocol.JSONField{Name: "text", Value: stringJSON("[Tool use interrupted]")}, protocol.JSONField{Name: "citations", Value: json.RawMessage(`[]`)}))
		}
		// For an originally empty assistant the source does not install its
		// local placeholder because changed=false. Preserve that exact behavior.
		if changed {
			message = setMessageContent(message, api, finalContent)
		}
		result = append(result, message)
		var next storage.Object
		if index+1 < len(input) {
			next = input[index+1]
		}
		existing, duplicate := map[string]bool{}, false
		existingIDs := []json.RawMessage{}
		var nextAPI storage.Object
		var nextContent []ContentBlock
		if rawStringField(next["type"]) == "user" {
			nextAPI, err = messageAPI(next)
			if err != nil {
				return PairingResult{}, err
			}
			nextContent, err = contentBlocks(nextAPI["content"])
			if err != nil {
				return PairingResult{}, err
			}
			if !isString(nextAPI["content"]) {
				for _, block := range nextContent {
					if blockType(block) != "tool_result" {
						continue
					}
					key := idKey(block.Get("tool_use_id"))
					if existing[key] {
						duplicate = true
					} else {
						existingIDs = append(existingIDs, block.Get("tool_use_id"))
					}
					existing[key] = true
				}
			}
		}
		missing, orphans := []json.RawMessage{}, map[string]bool{}
		for _, id := range useIDs {
			if !existing[idKey(id)] {
				missing = append(missing, id)
			}
		}
		for _, id := range existingIDs {
			if !seen[idKey(id)] {
				orphans[idKey(id)] = true
			}
		}
		if len(missing) == 0 && len(orphans) == 0 && !duplicate {
			continue
		}
		repaired = true
		synthetic := make([]ContentBlock, 0, len(missing))
		for _, id := range missing {
			block := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: stringJSON("tool_result")}, protocol.JSONField{Name: "tool_use_id", Value: id}, protocol.JSONField{Name: "content", Value: stringJSON(SyntheticToolResultPlaceholder)}, protocol.JSONField{Name: "is_error", Value: json.RawMessage(`true`)})
			synthetic = append(synthetic, block)
		}
		if nextAPI != nil {
			if len(orphans) > 0 || duplicate {
				kept, seenResults := []ContentBlock{}, map[string]bool{}
				for _, block := range nextContent {
					if blockType(block) == "tool_result" {
						key := idKey(block.Get("tool_use_id"))
						if orphans[key] || seenResults[key] {
							continue
						}
						seenResults[key] = true
					}
					kept = append(kept, block)
				}
				nextContent = kept
			}
			patched := append(append([]ContentBlock{}, synthetic...), nextContent...)
			index++
			if len(patched) > 0 {
				next = setMessageContent(next, nextAPI, patched)
				if options.UniversalSmoosh {
					next, err = smooshSystemReminderSiblings(next, nextAPI, patched)
					if err != nil {
						return PairingResult{}, err
					}
				}
				result = append(result, next)
			} else {
				if factory == nil {
					return PairingResult{}, errors.New("pairing repair requires the shared session message factory")
				}
				result = append(result, factory.CreateUser(storage.Object{"content": stringJSON(NoContent), "isMeta": json.RawMessage(`true`)}))
			}
		} else if len(synthetic) > 0 {
			if factory == nil {
				return PairingResult{}, errors.New("pairing repair requires the shared session message factory")
			}
			raw, _ := json.Marshal(synthetic)
			result = append(result, factory.CreateUser(storage.Object{"content": raw, "isMeta": json.RawMessage(`true`)}))
		}
	}
	if !repaired {
		return PairingResult{Messages: result}, nil
	}
	structure, err := pairingStructure(input)
	if err != nil {
		return PairingResult{}, err
	}
	if options.Strict {
		return PairingResult{}, &PairingError{MessageJSON: joinJSON(stringJSON("ensureToolResultPairing: tool_use/tool_result pairing mismatch detected (strict mode). Refusing to repair — would inject synthetic placeholders into model context. Message structure: "), structure, stringJSON(". See inc-4977."))}
	}
	diagnostic := joinJSON(stringJSON(fmt.Sprintf("ensureToolResultPairing: repaired missing tool_result blocks (%d -> %d messages). Message structure: ", len(input), len(result))), structure)
	return PairingResult{Messages: result, Repair: &PairingRepair{MessageCount: len(input), RepairedMessageCount: len(result), MessageTypes: structure, Diagnostic: diagnostic}}, nil
}
