package sessioncontroller

import (
	"bytes"
	"encoding/json"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

// originalRecord and originalBlocks inspect the existing original envelope on
// its caller's event owner. They do not normalize a tool result or read a log.
func originalRecord(original LiveRecord) (recordOriginal, error) {
	if (original.Record == nil) == (original.Assistant == nil) {
		return recordOriginal{}, capability("record requires exactly one original envelope")
	}
	value := recordOriginal{record: original.Record, live: original.Assistant}
	if value.live != nil {
		live := value.live
		if !live.Record.IsObject() || live.Record.String("type") != "assistant" || !live.Message.IsObject() || live.Message.String("role") != "assistant" || live.UUID == "" || live.Record.String("uuid") != live.UUID || live.Content == nil {
			return recordOriginal{}, capability("record original provider envelope")
		}
		value.record = &live.Record
	}
	if !value.record.IsObject() {
		return recordOriginal{}, capability("record original object")
	}
	return value, nil
}

// LoggingTypeError is the actual failed null property access, not a migration
// refusal. Each read allocates its own Error object; Record preserves that same
// pointer through rejection. Its native Go type/stack differs from Bun.
type LoggingTypeError struct{ message string }

func (failure *LoggingTypeError) Error() string     { return failure.message }
func (failure *LoggingTypeError) ErrorName() string { return "TypeError" }
func nullLoggingBlock() error {
	return &LoggingTypeError{message: "null is not an object (evaluating 'b.type')"}
}
func originalBlocks(value recordOriginal) ([]protocol.JSONObject, bool, error) {
	if value.live != nil {
		blocks := make([]protocol.JSONObject, 0, len(value.live.Content))
		for _, block := range value.live.Content {
			if block == nil || !block.Object.IsObject() {
				return nil, true, nullLoggingBlock()
			}
			blocks = append(blocks, block.Object)
		}
		return blocks, true, nil
	}
	message, ok := value.record.Object("message")
	if !ok {
		return nil, false, nil
	}
	content, array := message.Array("content")
	if !array {
		return nil, false, nil
	}
	blocks := make([]protocol.JSONObject, 0, content.Len())
	for i := 0; i < content.Len(); i++ {
		block, ok := content.Object(i)
		if !ok {
			raw := bytes.TrimSpace(content.Get(i))
			if len(raw) == 0 {
				return nil, true, &LoggingTypeError{message: "undefined is not an object (evaluating 'b.type')"}
			}
			if bytes.Equal(raw, []byte("null")) {
				return nil, true, nullLoggingBlock()
			}
			// Ordinary primitive property reads yield undefined. Keep the original
			// array value when filtering, rather than inventing a null replacement.
		}
		blocks = append(blocks, block)
	}
	return blocks, true, nil
}

// Tool IDs normally are strings. Keep their JavaScript UTF-16 identity, so
// distinct lone surrogates do not collapse into Go's replacement rune.
func loggingToolKey(value json.RawMessage) (string, error) {
	data := bytes.TrimSpace(value)
	if len(data) == 0 {
		return "undefined", nil
	}
	if data[0] == '"' {
		return stringKey(value)
	}
	if data[0] == '{' || data[0] == '[' {
		return "", capability("object-valued original tool identity")
	}
	canonical, err := protocol.ParseAndStringifyJavaScriptJSON(data)
	return string(canonical), err
}
func collectLoggingREPLIDs(messages []LiveRecord) (map[string]bool, error) {
	ids := map[string]bool{}
	for _, original := range messages {
		value, err := originalRecord(original)
		if err != nil {
			return nil, err
		}
		if value.record.String("type") != "assistant" {
			continue
		}
		blocks, array, err := originalBlocks(value)
		if err != nil {
			return nil, err
		}
		if !array {
			continue
		}
		for _, block := range blocks {
			if block.String("type") == "tool_use" && block.String("name") == "REPL" {
				key, err := loggingToolKey(block.Get("id"))
				if err != nil {
					return nil, err
				}
				ids[key] = true
			}
		}
	}
	return ids, nil
}
func cleanRecord(messages []LiveRecord) ([]recordOriginal, error) {
	return cleanRecordWithAll(messages, nil)
}

// This is Source's external SDK transcript cleaning, before exclusions or any
// cache/file await. REPL IDs come from the full original session, even when the
// recorded slice contains only a later result. Ordinary tool results keep their
// order, fields, identity and duplicates; the existing UUID service dedups rows.
func cleanRecordWithAll(messages, allMessages []LiveRecord) ([]recordOriginal, error) {
	return cleanRecordForEnvironment(messages, allMessages, nil)
}

// transcriptEntrypoint follows Source's trimmed application override followed
// by its untrimmed legacy entrypoint. The same value stamps and filters records.
func transcriptEntrypoint(environment map[string]string) string {
	name := utf16Text(trimUnits(utf16.Encode([]rune(environment["CC_HAHA_TRANSCRIPT_ENTRYPOINT"]))))
	if name != "" {
		return name
	}
	return environment["CLAUDE_CODE_ENTRYPOINT"]
}

// External desktop transcripts keep explicit context so restarting the app
// restores the content originally sent to the model. Other attachment kinds
// retain the ordinary external filtering policy; ant/hook admission is separate.
func isLoggableRecord(record protocol.JSONObject, environment map[string]string) (bool, error) {
	kind := record.String("type")
	if kind == "progress" {
		return false, nil
	}
	if kind != "attachment" {
		return true, nil
	}
	attachment, object := record.Object("attachment")
	if !object {
		raw := bytes.TrimSpace(record.Get("attachment"))
		if len(raw) == 0 {
			return false, &LoggingTypeError{message: "undefined is not an object (evaluating 'm.attachment.type')"}
		}
		if bytes.Equal(raw, []byte("null")) {
			return false, &LoggingTypeError{message: "null is not an object (evaluating 'm.attachment.type')"}
		}
		return false, nil
	}
	if transcriptEntrypoint(environment) != "claude-desktop" {
		return false, nil
	}
	switch attachment.String("type") {
	case "file", "directory", "pdf_reference":
		return true, nil
	default:
		return false, nil
	}
}

func cleanRecordForEnvironment(messages, allMessages []LiveRecord, environment map[string]string) ([]recordOriginal, error) {
	if allMessages == nil {
		allMessages = messages
	}
	// Source filters before collecting full-session REPL IDs. In particular,
	// a malformed desktop attachment rejects before unrelated block reads.
	filteredMessages := make([]recordOriginal, 0, len(messages))
	for _, original := range messages {
		value, err := originalRecord(original)
		if err != nil {
			return nil, err
		}
		keep, err := isLoggableRecord(*value.record, environment)
		if err != nil {
			return nil, err
		}
		if keep {
			filteredMessages = append(filteredMessages, value)
		}
	}
	replIDs, err := collectLoggingREPLIDs(allMessages)
	if err != nil {
		return nil, err
	}
	values := make([]recordOriginal, 0, len(filteredMessages))
	for _, value := range filteredMessages {
		kind := value.record.String("type")
		if _, err := stringKey(value.record.Get("uuid")); err != nil {
			return nil, err
		}
		var filtered []protocol.JSONObject
		var keptLive []*streamhistory.Block
		var keptIndices []int
		changed := false
		array := false
		if kind == "assistant" || kind == "user" {
			blocks, isArray, err := originalBlocks(value)
			if err != nil {
				return nil, err
			}
			array = isArray
			if array {
				for index, block := range blocks {
					remove := kind == "assistant" && block.String("type") == "tool_use" && block.String("name") == "REPL"
					if kind == "user" && block.String("type") == "tool_result" {
						key, err := loggingToolKey(block.Get("tool_use_id"))
						if err != nil {
							return nil, err
						}
						remove = replIDs[key]
					}
					if remove {
						changed = true
						continue
					}
					filtered = append(filtered, block)
					keptIndices = append(keptIndices, index)
					if value.live != nil {
						keptLive = append(keptLive, value.live.Content[index])
					}
				}
				if len(filtered) == 0 {
					continue
				}
			}
		}
		virtual := truthy(value.record.Get("isVirtual"))
		if changed || virtual {
			copy := value.record.Clone()
			if virtual {
				copy.Delete("isVirtual")
			}
			if array {
				if value.live != nil {
					// Source shallow-copies the API message: subsequent property replacement
					// on the original is invisible, while retained block objects stay shared.
					live := *value.live
					live.Record = copy
					live.Message = value.live.Message.Clone()
					if changed {
						live.Content = keptLive
					}
					value.live = &live
				} else {
					originalMessage, _ := copy.Object("message")
					message := originalMessage.Clone()
					if changed {
						content, _ := originalMessage.Array("content")
						kept, err := content.Select(keptIndices)
						if err != nil {
							return nil, err
						}
						message.SetArray("content", kept)
					}
					copy.SetObject("message", message)
				}
			}
			value.record = &copy
			if value.live != nil {
				value.live.Record = copy
				value.record = &value.live.Record
			}
		}
		values = append(values, value)
	}
	return values, nil
}
