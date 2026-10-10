package sessionresume

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type Diagnostic struct {
	Kind, Name string
	Fields     json.RawMessage
}
type Options struct {
	Factory      *messages.Factory
	RelativePath func(json.RawMessage) (json.RawMessage, error)
	OnDiagnostic func(Diagnostic)
	OnError      func(error)
	// Release builds enable TRANSCRIPT_CLASSIFIER. Other internal features
	// remain off; this option freezes the declared Source build profile.
	TranscriptClassifier bool
}
type Reducer struct{ options Options }

func New(options Options) (*Reducer, error) {
	if options.Factory == nil || options.RelativePath == nil || options.OnDiagnostic == nil || options.OnError == nil {
		return nil, errors.New("session resume requires actual factory, path, diagnostics and error capabilities")
	}
	return &Reducer{options}, nil
}

type Interruption struct {
	Kind    string   `json:"kind"`
	Message *Message `json:"message,omitempty"`
}
type Result struct {
	Messages              []*Message   `json:"messages"`
	TurnInterruptionState Interruption `json:"turnInterruptionState"`
}
type LoadedResult struct {
	Result
	SessionID json.RawMessage `json:"sessionId,omitempty"`
	Input     []*Message      `json:"-"`
}

// LoadPath consumes independent raw persisted history. It deliberately does
// not pretend to restore plans, skills, snapshots or SessionStart hooks.
func (reducer *Reducer) LoadPath(loader *sessionhistory.Loader, path string) (LoadedResult, error) {
	if loader == nil {
		return LoadedResult{}, errors.New("resume path requires the real session history loader")
	}
	loaded, err := loader.LoadMessagesFromJSONLPath(path)
	if err != nil {
		return LoadedResult{}, err
	}
	history, err := ParseMessages(loaded.Messages)
	if err != nil {
		return LoadedResult{}, err
	}
	result, err := reducer.Deserialize(history)
	return LoadedResult{result, loaded.SessionID, history}, err
}
func kind(message *Message) string { return text(message.value.get("type")) }
func (reducer *Reducer) event(name string, fields protocol.JSONObject) {
	raw, _ := fields.MarshalJSON()
	reducer.options.OnDiagnostic(Diagnostic{"event", name, raw})
}

func stringsArray(item *value) bool {
	if item == nil || !item.isArray {
		return false
	}
	for _, child := range item.items {
		if !stringValue(child) {
			return false
		}
	}
	return true
}
func attachmentValid(item *value) bool {
	if item == nil || !item.object.IsObject() || !stringValue(item.get("type")) {
		return false
	}
	switch text(item.get("type")) {
	case "new_file", "file":
		return stringValue(item.get("filename"))
	case "new_directory", "directory":
		return stringValue(item.get("path"))
	case "selected_lines_in_ide", "hook_success", "skill_listing":
		return stringValue(item.get("content"))
	case "hook_additional_context":
		return stringsArray(item.get("content"))
	case "invoked_skills":
		skills := item.get("skills")
		if skills == nil || !skills.isArray {
			return false
		}
		for _, skill := range skills.items {
			if !skill.object.IsObject() && !skill.isArray {
				return false
			}
		}
		return true
	case "deferred_tools_delta":
		return stringsArray(item.get("addedNames")) && stringsArray(item.get("addedLines")) && stringsArray(item.get("removedNames"))
	case "mcp_instructions_delta":
		return stringsArray(item.get("addedNames")) && stringsArray(item.get("addedBlocks")) && stringsArray(item.get("removedNames"))
	case "agent_listing_delta":
		return stringsArray(item.get("addedTypes")) && stringsArray(item.get("addedLines")) && stringsArray(item.get("removedTypes"))
	default:
		return true
	}
}

// FilterMalformedAttachments is Source's outer resume filter. It deliberately
// does not migrate legacy attachments before restoring their persisted state.
func (reducer *Reducer) FilterMalformedAttachments(history []*Message) ([]*Message, error) {
	retained := make([]*Message, 0, len(history))
	for _, message := range history {
		if _, err := property(message.value, "type", "message.type"); err != nil {
			return nil, err
		}
		if kind(message) != "attachment" || attachmentValid(message.value.get("attachment")) {
			retained = append(retained, message)
		}
	}
	if dropped := len(history) - len(retained); dropped > 0 {
		reducer.options.OnDiagnostic(Diagnostic{"debug", fmt.Sprintf("resume: dropped %d attachment entries with a missing or malformed payload", dropped), json.RawMessage(`{"level":"warn"}`)})
	}
	return retained, nil
}

func (reducer *Reducer) migrate(history []*Message) ([]*Message, error) {
	retained, err := reducer.FilterMalformedAttachments(history)
	if err != nil {
		return nil, err
	}
	for index, message := range retained {
		if kind(message) != "attachment" {
			continue
		}
		attachment := message.value.get("attachment")
		var path *value
		newType := ""
		switch text(attachment.get("type")) {
		case "new_file":
			newType, path = "file", attachment.get("filename")
		case "new_directory":
			newType, path = "directory", attachment.get("path")
		default:
			if attachment.get("displayPath") != nil {
				continue
			}
			for _, name := range []string{"filename", "path", "skillDir"} {
				if candidate := attachment.get(name); candidate != nil {
					path = candidate
					break
				}
			}
		}
		if !stringValue(path) {
			continue
		}
		display, err := reducer.options.RelativePath(encoded(path))
		if err != nil {
			return nil, err
		}
		copy := clone(message.value)
		payload := copy.get("attachment")
		if newType != "" {
			payload.set("type", quoted(newType))
		}
		item, err := parse(display)
		if err != nil {
			return nil, err
		}
		payload.set("displayPath", item)
		retained[index] = &Message{copy}
	}
	return retained, nil
}
func (reducer *Reducer) permissions(history []*Message) {
	allowed := map[string]bool{"acceptEdits": true, "bypassPermissions": true, "default": true, "dontAsk": true, "plan": true}
	if reducer.options.TranscriptClassifier {
		allowed["auto"] = true
	}
	for _, message := range history {
		if kind(message) == "user" {
			field := message.value.get("permissionMode")
			if field != nil && (!stringValue(field) || !allowed[text(field)]) {
				message.value.set("permissionMode", nil)
			}
		}
	}
}
func unresolved(history []*Message) ([]*Message, error) {
	uses, results := map[string]bool{}, map[string]bool{}
	for _, message := range history {
		if kind(message) != "user" && kind(message) != "assistant" {
			continue
		}
		content, err := messageContent(message, "msg.message.content")
		if err != nil {
			return nil, err
		}
		if content == nil || !content.isArray {
			continue
		}
		for _, block := range content.items {
			typeName, err := blockKind(block, "block.type")
			if err != nil {
				return nil, err
			}
			if typeName == "tool_use" {
				uses[identity(block.get("id"))] = true
			}
			if typeName == "tool_result" {
				results[identity(block.get("tool_use_id"))] = true
			}
		}
	}
	for id := range results {
		delete(uses, id)
	}
	if len(uses) == 0 {
		return history, nil
	}
	retained := make([]*Message, 0, len(history))
	for _, message := range history {
		all, count := true, 0
		if kind(message) == "assistant" {
			content, _ := messageContent(message, "msg.message.content")
			if content != nil && content.isArray {
				for _, block := range content.items {
					if text(block.get("type")) == "tool_use" {
						count++
						all = all && uses[identity(block.get("id"))]
					}
				}
			}
		}
		if count == 0 || !all {
			retained = append(retained, message)
		}
	}
	return retained, nil
}
func (reducer *Reducer) thinking(history []*Message) ([]*Message, error) {
	siblings := map[string]bool{}
	for _, message := range history {
		if kind(message) != "assistant" {
			continue
		}
		content, err := messageContent(message, "msg.message.content")
		if err != nil {
			return nil, err
		}
		if content == nil || !content.isArray {
			continue
		}
		for _, block := range content.items {
			typ, err := blockKind(block, "block.type")
			if err != nil {
				return nil, err
			}
			if typ != "thinking" && typ != "redacted_thinking" {
				if id := message.value.get("message").get("id"); truthy(id) {
					siblings[identity(id)] = true
				}
				break
			}
		}
	}
	retained := make([]*Message, 0, len(history))
	for _, message := range history {
		all := false
		var content *value
		if kind(message) == "assistant" {
			content, _ = messageContent(message, "msg.message.content")
			if content != nil && content.isArray && len(content.items) > 0 {
				all = true
				for _, block := range content.items {
					typ := text(block.get("type"))
					all = all && (typ == "thinking" || typ == "redacted_thinking")
				}
			}
		}
		id := message.value.get("message").get("id")
		if all && !(truthy(id) && siblings[identity(id)]) {
			reducer.event("tengu_filtered_orphaned_thinking_message", protocol.NewJSONObject(protocol.JSONField{Name: "messageUUID", Value: message.UUID()}, protocol.JSONField{Name: "messageId", Value: encoded(id)}, protocol.JSONField{Name: "blockCount", Value: json.RawMessage(fmt.Sprint(len(content.items)))}))
		} else {
			retained = append(retained, message)
		}
	}
	return retained, nil
}
func whitespace(unit uint16) bool {
	return unit >= 9 && unit <= 13 || unit == 0x20 || unit == 0xa0 || unit == 0x1680 || unit >= 0x2000 && unit <= 0x200a || unit == 0x2028 || unit == 0x2029 || unit == 0x202f || unit == 0x205f || unit == 0x3000 || unit == 0xfeff
}
func (reducer *Reducer) whitespace(history []*Message) ([]*Message, error) {
	retained := make([]*Message, 0, len(history))
	changed := false
	for _, message := range history {
		remove := false
		if kind(message) == "assistant" {
			content, err := messageContent(message, "message.message.content")
			if err != nil {
				return nil, err
			}
			if content != nil && content.isArray && len(content.items) > 0 {
				remove = true
				for _, block := range content.items {
					typ, err := blockKind(block, "block.type")
					if err != nil {
						return nil, err
					}
					if typ != "text" {
						remove = false
						break
					}
					field := block.get("text")
					if field == nil {
						continue
					}
					if null(field) {
						return nil, typeError("null is not an object (evaluating 'block.text.trim')")
					}
					if !stringValue(field) {
						return nil, typeErrorValue("block.text.trim is not a function. (In 'block.text.trim()', 'block.text.trim' is ", field.get("trim"), ")")
					}
					units, _ := protocol.DecodeJavaScriptJSONString(encoded(field))
					for _, unit := range units {
						if !whitespace(unit) {
							remove = false
							break
						}
					}
					if !remove {
						break
					}
				}
			}
		}
		if remove {
			changed = true
			reducer.event("tengu_filtered_whitespace_only_assistant", protocol.NewJSONObject(protocol.JSONField{Name: "messageUUID", Value: message.UUID()}))
		} else {
			retained = append(retained, message)
		}
	}
	if !changed {
		return history, nil
	}
	merged := make([]*Message, 0, len(retained))
	for _, message := range retained {
		if len(merged) > 0 && kind(message) == "user" && kind(merged[len(merged)-1]) == "user" {
			combined, err := mergeUsers(merged[len(merged)-1], message)
			if err != nil {
				return nil, err
			}
			merged[len(merged)-1] = combined
		} else {
			merged = append(merged, message)
		}
	}
	return merged, nil
}
func userContent(message *Message, expression string) (*value, error) {
	content, err := messageContent(message, expression)
	if err != nil {
		return nil, err
	}
	if stringValue(content) {
		block := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: json.RawMessage(`"text"`)}, protocol.JSONField{Name: "text", Value: encoded(content)})
		raw, _ := block.MarshalJSON()
		item, _ := parse(raw)
		return &value{isArray: true, items: []*value{item}}, nil
	}
	return content, nil
}
func mergeUsers(left, right *Message) (*Message, error) {
	// Source evaluates both normalization arguments before calling a.at(-1).
	leftContent, err := userContent(left, "a.message.content")
	if err != nil {
		return nil, err
	}
	rightContent, err := userContent(right, "b.message.content")
	if err != nil {
		return nil, err
	}
	if leftContent == nil || !leftContent.isArray {
		if leftContent == nil || null(leftContent) {
			_, err := property(leftContent, "at", "a.at")
			return nil, err
		}
		return nil, typeErrorValue("a.at is not a function. (In 'a.at(-1)', 'a.at' is ", leftContent.get("at"), ")")
	}
	if rightContent == nil || null(rightContent) {
		_, err := property(rightContent, "0", "b[0]")
		return nil, err
	}
	if !rightContent.isArray {
		return nil, typeError("Spread syntax requires ...iterable[Symbol.iterator] to be a function")
	}
	a, b := leftContent.items, rightContent.items
	combined := append(append([]*value{}, a...), b...)
	if len(a) > 0 && len(b) > 0 && text(a[len(a)-1].get("type")) == "text" && text(b[0].get("type")) == "text" {
		seam := clone(a[len(a)-1])
		coerced, err := protocol.JavaScriptStringJSON(encoded(seam.get("text")))
		if err != nil {
			return nil, err
		}
		joined, err := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{coerced, json.RawMessage(`"\n"`)}, "")
		if err != nil {
			return nil, err
		}
		item, _ := parse(joined)
		seam.set("text", item)
		combined[len(a)-1] = seam
	}
	results, other := []*value{}, []*value{}
	for _, block := range combined {
		if _, err := property(block, "type", "block.type"); err != nil {
			return nil, err
		}
		if text(block.get("type")) == "tool_result" {
			results = append(results, block)
		} else {
			other = append(other, block)
		}
	}
	copy := clone(left.value)
	copy.get("message").set("content", &value{isArray: true, items: append(results, other...)})
	if truthy(left.value.get("isMeta")) {
		copy.set("uuid", right.value.get("uuid"))
	}
	return &Message{copy}, nil
}
func terminal(result *Message, history []*Message, index int) (bool, error) {
	content, err := messageContent(result, "result.message.content")
	if err != nil {
		return false, err
	}
	if content == nil || !content.isArray || len(content.items) == 0 || content.items[0] == nil || text(content.items[0].get("type")) != "tool_result" {
		return false, nil
	}
	id := content.items[0].get("tool_use_id")
	for cursor := index - 1; cursor >= 0; cursor-- {
		message := history[cursor]
		if kind(message) != "assistant" {
			continue
		}
		blocks, err := messageContent(message, "msg.message.content")
		if err != nil {
			return false, err
		}
		if blocks == nil || !blocks.isArray {
			if stringValue(blocks) {
				continue
			}
			if blocks == nil || null(blocks) {
				_, err := property(blocks, "iterator", "msg.message.content")
				return false, err
			}
			if blocks.object.IsObject() {
				return false, typeError("{} is not iterable")
			}
			if string(blocks.raw) == "true" || string(blocks.raw) == "false" {
				return false, typeError(string(blocks.raw) + " is not iterable")
			}
			return false, typeError("number is not iterable")
		}
		for _, block := range blocks.items {
			typ, err := blockKind(block, "b.type")
			if err != nil {
				return false, err
			}
			if typ == "tool_use" && same(block.get("id"), id) {
				return null(block.get("name")), nil
			}
		}
	}
	return false, nil
}
func interruption(history []*Message) (Interruption, error) {
	state := Interruption{Kind: "none"}
	for index := len(history) - 1; index >= 0; index-- {
		message := history[index]
		typ := kind(message)
		if typ == "system" || typ == "progress" || typ == "assistant" && truthy(message.value.get("isApiErrorMessage")) {
			continue
		}
		if typ == "assistant" {
			return state, nil
		}
		if typ == "user" {
			if truthy(message.value.get("isMeta")) || truthy(message.value.get("isCompactSummary")) {
				return state, nil
			}
			content, err := messageContent(message, "message.message.content")
			if err != nil {
				return state, err
			}
			toolResult := truthy(message.value.get("toolUseResult"))
			if content != nil && content.isArray && len(content.items) > 0 {
				toolResult = toolResult || text(content.items[0].get("type")) == "tool_result"
			}
			if toolResult {
				done, err := terminal(message, history, index)
				if err != nil {
					return state, err
				}
				if !done {
					return Interruption{Kind: "interrupted_turn"}, nil
				}
				return state, nil
			}
			return Interruption{Kind: "interrupted_prompt", Message: message}, nil
		}
		if typ == "attachment" {
			return Interruption{Kind: "interrupted_turn"}, nil
		}
		return state, nil
	}
	return state, nil
}

func orderedFactory(factory *messages.Factory, assistant bool) (*Message, error) {
	var generated storage.Object
	var fields, apiFields []string
	if assistant {
		generated = factory.CreateAssistant(storage.Object{"content": json.RawMessage(`"No response requested."`)})
		fields = []string{"type", "uuid", "timestamp", "message", "requestId", "apiError", "error", "errorDetails", "businessErrorCode", "sourceModel", "isApiErrorMessage", "isVirtual"}
		apiFields = []string{"id", "container", "model", "role", "stop_reason", "stop_sequence", "type", "usage", "content", "context_management"}
	} else {
		generated = factory.CreateUser(storage.Object{"content": json.RawMessage(`"Continue from where you left off."`), "isMeta": json.RawMessage("true")})
		fields = []string{"type", "message", "isMeta", "isVisibleInTranscriptOnly", "isVirtual", "isCompactSummary", "summarizeMetadata", "uuid", "timestamp", "toolUseResult", "mcpMeta", "imagePasteIds", "sourceToolAssistantUUID", "permissionMode", "origin"}
		apiFields = []string{"role", "content"}
	}
	var api storage.Object
	_ = json.Unmarshal(generated["message"], &api)
	orderedAPI := protocol.NewJSONObject()
	for _, name := range apiFields {
		if raw := api[name]; raw != nil {
			orderedAPI.Set(name, raw)
		}
	}
	if !assistant {
		orderedAPI.Set("content", json.RawMessage(`[{"type":"text","text":"Continue from where you left off."}]`))
	}
	if assistant {
		var usage storage.Object
		_ = json.Unmarshal(api["usage"], &usage)
		orderedUsage := protocol.NewJSONObject()
		for _, name := range []string{"input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "server_tool_use", "service_tier", "cache_creation", "inference_geo", "iterations", "speed"} {
			orderedUsage.Set(name, usage[name])
		}
		raw, _ := orderedUsage.MarshalJSON()
		orderedAPI.Set("usage", raw)
	}
	apiRaw, _ := orderedAPI.MarshalJSON()
	ordered := protocol.NewJSONObject()
	for _, name := range fields {
		raw := generated[name]
		if name == "message" {
			raw = apiRaw
		}
		if raw != nil {
			ordered.Set(name, raw)
		}
	}
	raw, _ := ordered.MarshalJSON()
	item, err := parse(raw)
	return &Message{item}, err
}
func (reducer *Reducer) Deserialize(history []*Message) (result Result, err error) {
	defer func() {
		if err != nil {
			reducer.options.OnError(err)
		}
	}()
	filtered, err := reducer.migrate(history)
	if err != nil {
		return result, err
	}
	reducer.permissions(filtered)
	filtered, err = unresolved(filtered)
	if err != nil {
		return result, err
	}
	filtered, err = reducer.thinking(filtered)
	if err != nil {
		return result, err
	}
	filtered, err = reducer.whitespace(filtered)
	if err != nil {
		return result, err
	}
	state, err := interruption(filtered)
	if err != nil {
		return result, err
	}
	if state.Kind == "interrupted_turn" {
		continuation, err := orderedFactory(reducer.options.Factory, false)
		if err != nil {
			return result, err
		}
		filtered = append(filtered, continuation)
		state = Interruption{Kind: "interrupted_prompt", Message: continuation}
	}
	for index := len(filtered) - 1; index >= 0; index-- {
		typ := kind(filtered[index])
		if typ == "system" || typ == "progress" {
			continue
		}
		if typ == "user" {
			sentinel, err := orderedFactory(reducer.options.Factory, true)
			if err != nil {
				return result, err
			}
			filtered = append(filtered, nil)
			copy(filtered[index+2:], filtered[index+1:])
			filtered[index+1] = sentinel
		}
		break
	}
	return Result{filtered, state}, nil
}
