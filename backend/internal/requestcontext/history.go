package requestcontext

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/attachments"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

// IsSyntheticAPIErrorMessage is the release normalizer's strict predicate for
// valid original envelopes. Malformed message payloads remain an admission
// capability boundary; this is not a replacement for Source TypeError policy.
func IsSyntheticAPIErrorMessage(source protocol.JSONObject) bool {
	if source.String("type") != "assistant" || !bytes.Equal(bytes.TrimSpace(source.Get("isApiErrorMessage")), []byte("true")) {
		return false
	}
	api, err := parseObject(source.Get("message"))
	return err == nil && api.IsObject() && api.String("model") == "<synthetic>"
}

// IsOriginalNoResponseAssistant recognizes the existing conversation-recovery
// Factory sentinel. It is an ordinary assistant, not an API error: Source keeps
// it in model history. Admission remains narrower than arbitrary error flags.
func IsOriginalNoResponseAssistant(source protocol.JSONObject) bool {
	if source.String("type") != "assistant" || source.Get("isVirtual") != nil {
		return false
	}
	flag := bytes.TrimSpace(source.Get("isApiErrorMessage"))
	if len(flag) != 0 && !bytes.Equal(flag, []byte("false")) {
		return false
	}
	for _, key := range []string{"apiError", "error", "errorDetails", "businessErrorCode", "sourceModel"} {
		if source.Get(key) != nil {
			return false
		}
	}
	api, err := parseObject(source.Get("message"))
	if err != nil || !api.IsObject() || api.String("model") != "<synthetic>" {
		return false
	}
	var content []protocol.JSONObject
	return json.Unmarshal(api.Get("content"), &content) == nil && len(content) == 1 && content[0].String("type") == "text" && content[0].String("text") == "No response requested."
}

// Source's media-anchor maps are ordinary JavaScript objects. Prototype lookup
// names can throw during recovery even for textual history. That error policy
// and non-string lookup coercion are not migrated by this textual-history slice.
func prototypeAnchorLookup(raw json.RawMessage) bool {
	switch stringValue(raw) {
	case "__proto__", "constructor", "hasOwnProperty", "isPrototypeOf", "propertyIsEnumerable", "toLocaleString", "toString", "valueOf", "__defineGetter__", "__defineSetter__", "__lookupGetter__", "__lookupSetter__":
		return true
	}
	return false
}

func validateSyntheticAPIErrorAnchor(source protocol.JSONObject) error {
	if prototypeAnchorLookup(source.Get("businessErrorCode")) {
		return unsupported("unported synthetic API error anchor lookup")
	}
	api, _ := parseObject(source.Get("message"))
	var values []protocol.JSONObject
	if json.Unmarshal(api.Get("content"), &values) != nil || len(values) == 0 || values[0].String("type") != "text" {
		return nil
	}
	text := values[0].Get("text")
	if len(text) == 0 || bytes.Equal(bytes.TrimSpace(text), []byte("null")) {
		return nil
	}
	if _, err := protocol.DecodeJavaScriptJSONString(text); err != nil {
		return unsupported("unported synthetic API error anchor text")
	}
	if prototypeAnchorLookup(text) {
		return unsupported("unported synthetic API error anchor lookup")
	}
	return nil
}

func validateBlocks(raw json.RawMessage, assistant bool) error {
	if !assistant && len(raw) > 0 && raw[0] == '"' {
		return nil
	}
	var values []protocol.JSONObject
	if json.Unmarshal(raw, &values) != nil || values == nil {
		return unsupported("malformed history content")
	}
	for _, block := range values {
		switch block.String("type") {
		case "text":
			if _, err := protocol.DecodeJavaScriptJSONString(block.Get("text")); err != nil {
				return unsupported("non-string history text")
			}
		case "image":
			if assistant {
				return unsupported("user image in assistant history")
			}
			if err := ValidateProcessedImageBlock(block); err != nil {
				return err
			}
		case "document":
			if assistant {
				return unsupported("user document in assistant history")
			}
			if err := ValidateDocumentBlock(block); err != nil {
				return err
			}
		case "thinking", "redacted_thinking", "tool_use":
			if !assistant {
				return unsupported("assistant block in user history")
			}
		case "tool_result":
			if assistant {
				return unsupported("tool result in assistant history")
			}
			value := block.Get("content")
			if len(value) == 0 || string(value) == "null" || value[0] == '"' {
				continue
			}
			var nestedBlocks []protocol.JSONObject
			if json.Unmarshal(value, &nestedBlocks) != nil || nestedBlocks == nil {
				return unsupported("malformed nested tool result content")
			}
			for _, nested := range nestedBlocks {
				if nested.String("type") == "image" {
					if err := ValidateProcessedImageBlock(nested); err != nil {
						return unsupported("media/nested tool result normalization")
					}
					continue
				}
				if nested.String("type") == "document" {
					if err := ValidateDocumentBlock(nested); err != nil {
						return err
					}
					continue
				}
				if nested.String("type") != "text" && nested.String("type") != "tool_reference" {
					return unsupported("media/nested tool result normalization")
				}
				if nested.String("type") == "text" {
					if _, err := protocol.DecodeJavaScriptJSONString(nested.Get("text")); err != nil {
						return unsupported("non-string nested tool result text")
					}
				}
			}
		default:
			return unsupported("history block " + block.String("type"))
		}
	}
	return nil
}

func stripToolReferences(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || raw[0] != '[' {
		return raw
	}
	values := blocks(raw)
	hasReference := false
	for _, block := range values {
		if block.String("type") == "tool_result" {
			for _, part := range blocks(block.Get("content")) {
				hasReference = hasReference || part.String("type") == "tool_reference"
			}
		}
	}
	if !hasReference {
		return raw
	}
	for index, block := range values {
		if block.String("type") != "tool_result" {
			continue
		}
		nested := block.Get("content")
		if len(nested) == 0 || nested[0] != '[' {
			continue
		}
		kept := []protocol.JSONObject{}
		for _, part := range blocks(nested) {
			if part.String("type") != "tool_reference" {
				kept = append(kept, part)
			}
		}
		if len(kept) == 0 {
			kept = append(kept, object(field("type", quoted("text")), field("text", quoted("[Tool references removed - tool search not enabled]"))))
		}
		copy := block.Clone()
		value, _ := json.Marshal(kept)
		copy.Set("content", value)
		values[index] = copy
	}
	result, _ := json.Marshal(values)
	return result
}

func merge(a, b protocol.JSONObject, assistant bool) (protocol.JSONObject, error) {
	var merged storage.Object
	var err error
	if assistant {
		merged, err = messages.MergeAssistantMessages(toStorage(a), toStorage(b))
	} else {
		merged, err = messages.MergeUserMessages(toStorage(a), toStorage(b))
	}
	if err != nil {
		return protocol.JSONObject{}, err
	}
	api, _ := parseObject(merged["message"])
	result := replaceContent(a, api.Get("content"))
	if !assistant && truthy(a.Get("isMeta")) {
		result.Set("uuid", b.Get("uuid"))
	}
	return result, nil
}

// Pairing copies storage maps and replaces content. Recover the ordered record
// from all retained properties, rather than assuming a UUID uniquely identifies
// a legacy imported envelope. Incompatible order with indistinguishable retained
// properties is an unported boundary, never a reason to select an arbitrary one.
func sameRetainedFields(left, right storage.Object, omit string) bool {
	for name, value := range left {
		if name == omit {
			continue
		}
		other, exists := right[name]
		if !exists {
			return false
		}
		a, errA := stringify(value)
		b, errB := stringify(other)
		if errA != nil || errB != nil || string(a) != string(b) {
			return false
		}
	}
	for name := range right {
		if name != omit {
			if _, exists := left[name]; !exists {
				return false
			}
		}
	}
	return true
}

func retainedLayout(envelope protocol.JSONObject) string {
	copy := envelope.Clone()
	api, _ := parseObject(copy.Get("message"))
	api.Set("content", json.RawMessage("null"))
	copy.Set("message", rawObject(api))
	raw, _ := stringify(rawObject(copy))
	return string(raw)
}

func normalizeHistory(input []protocol.JSONObject, dependencies Dependencies, tools []toolschema.Definition) ([]protocol.JSONObject, error) {
	return normalizeHistoryForModel(input, dependencies, tools, "", false)
}

func normalizeHistoryForModel(input []protocol.JSONObject, dependencies Dependencies, tools []toolschema.Definition, model string, nonInteractive bool) ([]protocol.JSONObject, error) {
	return normalizeHistoryForModelWithPlan(input, dependencies, tools, model, nonInteractive, nil)
}
func normalizeHistoryForModelWithPlan(input []protocol.JSONObject, dependencies Dependencies, tools []toolschema.Definition, model string, nonInteractive bool, plan *attachments.PlanOptions) ([]protocol.JSONObject, error) {
	return normalizeHistoryForModelWithToolSearch(input, dependencies, tools, model, nonInteractive, plan, false)
}

func normalizeHistoryForModelWithToolSearch(input []protocol.JSONObject, dependencies Dependencies, tools []toolschema.Definition, model string, nonInteractive bool, plan *attachments.PlanOptions, toolSearchEnabled bool) ([]protocol.JSONObject, error) {
	factory, err := messages.NewFactory(dependencies.UUID, dependencies.Now)
	if err != nil {
		return nil, err
	}
	result := []protocol.JSONObject{}
	indices := map[string]int{}
	indexed := 0
	reordered := attachments.ReorderForAPI(input)
	visible := make([]protocol.JSONObject, 0, len(reordered))
	for _, source := range reordered {
		if (source.String("type") == "user" || source.String("type") == "assistant") && truthy(source.Get("isVirtual")) {
			continue
		}
		visible = append(visible, source)
	}
	mediaPlan, err := imageRecoveryPlan(visible, model, nonInteractive)
	if err != nil {
		return nil, err
	}
	// Source reorders retained attachments before filtering virtual users and
	// assistants. Those display-only envelopes still stop attachment bubbling.
	for sourceIndex, source := range visible {
		kind := source.String("type")
		if kind == "attachment" {
			projected, err := attachments.ProjectWithOptions(source.Get("attachment"), factory, plan)
			if err != nil {
				var malformed *attachments.MalformedAttachmentError
				if errors.As(err, &malformed) {
					continue
				}
				var capability *attachments.CapabilityError
				if errors.As(err, &capability) {
					return nil, unsupported(capability.Error())
				}
				return nil, err
			}
			for _, current := range projected {
				if sourceIndex < mediaPlan.through {
					current = replaceHistoryMedia(current)
				}
				if len(result) > 0 && result[len(result)-1].String("type") == "user" {
					combined, err := attachments.MergeIntoUser(result[len(result)-1], current)
					if err != nil {
						return nil, err
					}
					result[len(result)-1] = combined
				} else {
					result = append(result, current)
				}
			}
			continue
		}
		if kind == "progress" || kind == "system" {
			if kind == "system" && source.String("subtype") == "local_command" {
				if _, e := protocol.DecodeJavaScriptJSONString(source.Get("content")); e != nil || source.String("uuid") == "" || source.String("timestamp") == "" || source.Get("isVirtual") != nil || source.Get("isApiErrorMessage") != nil {
					return nil, unsupported("malformed local command history")
				}
				source = messages.OrderedUser(factory.CreateUser(storage.Object{"content": source.Get("content"), "uuid": source.Get("uuid"), "timestamp": source.Get("timestamp")}))
				kind = "user"
			} else {
				continue
			}
		}
		if kind != "user" && kind != "assistant" {
			return nil, unsupported("history envelope " + kind)
		}
		if truthy(source.Get("isVirtual")) {
			continue
		}
		if IsSyntheticAPIErrorMessage(source) {
			// Synthetic errors are display history; the prepass applies their
			// original model-scoped media recovery before this envelope is removed.
			continue
		}
		if truthy(source.Get("isApiErrorMessage")) {
			return nil, unsupported("synthetic API error anchor normalization")
		}
		current := source.Clone()
		if kind == "user" {
			if !toolSearchEnabled {
				current = replaceContent(current, stripToolReferences(content(current)))
			} else {
				names := []json.RawMessage{}
				for _, tool := range tools {
					names = append(names, quoted(tool.Name))
				}
				current, err = FilterUnavailableToolReferences(current, names)
				if err != nil {
					return nil, err
				}
			}
			if sourceIndex < mediaPlan.through {
				current = replaceHistoryMedia(current)
			}
			if types := mediaPlan.targets[historyMediaUUID(current)]; len(types) != 0 {
				var empty bool
				current, empty = stripHistoryMedia(current, types)
				if empty {
					continue
				}
			}
			current = EnsureToolReferenceTurnBoundary(current)
		}
		if err := validateBlocks(content(current), kind == "assistant"); err != nil {
			return nil, err
		}
		if kind == "user" {
			if len(result) > 0 && result[len(result)-1].String("type") == "user" {
				combined, err := merge(result[len(result)-1], current, false)
				if err != nil {
					return nil, err
				}
				result[len(result)-1] = combined
				continue
			}
		} else {
			parts := blocks(content(current))
			for i, block := range parts {
				if block.String("type") == "tool_use" {
					name, input := toolschema.NormalizeToolUse(tools, block.Get("name"), block.Get("input"))
					if toolSearchEnabled {
						parts[i] = block.Clone()
						parts[i].Set("name", name)
						parts[i].Set("input", input)
					} else {
						parts[i] = object(field("type", quoted("tool_use")), field("id", block.Get("id")), field("name", name), field("input", input))
					}
				}
			}
			value, _ := json.Marshal(parts)
			current = replaceContent(current, value)
			for ; indexed < len(result); indexed++ {
				indexedMessage := result[indexed]
				if indexedMessage.String("type") == "assistant" {
					api, _ := parseObject(indexedMessage.Get("message"))
					indices[string(api.Get("id"))] = indexed
				} else {
					toolResult := false
					for _, block := range blocks(content(indexedMessage)) {
						if block.String("type") == "tool_result" {
							toolResult = true
							break
						}
					}
					if !toolResult {
						clear(indices)
					}
				}
			}
			api, _ := parseObject(current.Get("message"))
			if existing, found := indices[string(api.Get("id"))]; found {
				combined, err := merge(result[existing], current, true)
				if err != nil {
					return nil, err
				}
				result[existing] = combined
				continue
			}
		}
		result = append(result, current)
	}
	// Orphan thinking is identified by the complete normalized message ID set.
	nonThinking := map[string]bool{}
	for _, envelope := range result {
		if envelope.String("type") == "assistant" {
			api, _ := parseObject(envelope.Get("message"))
			for _, block := range blocks(content(envelope)) {
				if !isThinking(block) && truthy(api.Get("id")) {
					nonThinking[string(api.Get("id"))] = true
				}
			}
		}
	}
	kept := []protocol.JSONObject{}
	for _, envelope := range result {
		parts := blocks(content(envelope))
		onlyThinking := envelope.String("type") == "assistant" && len(parts) > 0
		for _, block := range parts {
			onlyThinking = onlyThinking && isThinking(block)
		}
		api, _ := parseObject(envelope.Get("message"))
		if onlyThinking && !nonThinking[string(api.Get("id"))] {
			continue
		}
		if envelope.String("type") == "assistant" {
			parts = messages.ReorderAssistantToolUseBlocks(parts)
			raw, _ := json.Marshal(parts)
			envelope = replaceContent(envelope, raw)
		}
		kept = append(kept, envelope)
	}
	result = kept
	if len(result) > 0 && result[len(result)-1].String("type") == "assistant" {
		last := len(result) - 1
		parts := blocks(content(result[last]))
		originalLength := len(parts)
		for len(parts) > 0 && isThinking(parts[len(parts)-1]) {
			parts = parts[:len(parts)-1]
		}
		if originalLength != len(parts) {
			if len(parts) == 0 {
				parts = []protocol.JSONObject{object(field("type", quoted("text")), field("text", quoted("[No message content]")), field("citations", json.RawMessage(`[]`)))}
			}
			raw, _ := json.Marshal(parts)
			result[last] = replaceContent(result[last], raw)
		}
	}
	kept = nil
	removed := false
	for _, envelope := range result {
		parts := blocks(content(envelope))
		whitespace := envelope.String("type") == "assistant" && len(parts) > 0
		for _, block := range parts {
			units, _ := protocol.DecodeJavaScriptJSONString(block.Get("text"))
			whitespace = whitespace && block.String("type") == "text"
			for _, unit := range units {
				whitespace = whitespace && space(unit)
			}
		}
		if whitespace {
			removed = true
			continue
		}
		kept = append(kept, envelope)
	}
	if removed {
		result = nil
		for _, envelope := range kept {
			if envelope.String("type") == "user" && len(result) > 0 && result[len(result)-1].String("type") == "user" {
				combined, err := merge(result[len(result)-1], envelope, false)
				if err != nil {
					return nil, err
				}
				result[len(result)-1] = combined
			} else {
				result = append(result, envelope)
			}
		}
	}
	for index, envelope := range result {
		if envelope.String("type") == "assistant" && index < len(result)-1 && len(blocks(content(envelope))) == 0 {
			result[index] = replaceContent(envelope, json.RawMessage(`[{"type":"text","text":"(no content)","citations":[]}]`))
		}
	}
	storageHistory := make([]storage.Object, 0, len(result))
	for _, envelope := range result {
		storageHistory = append(storageHistory, toStorage(envelope))
	}
	paired, err := messages.EnsureToolResultPairing(storageHistory, factory, messages.PairingOptions{})
	if err != nil {
		return nil, err
	}
	output := make([]protocol.JSONObject, 0, len(paired.Messages))
	for _, entry := range paired.Messages {
		var original *protocol.JSONObject
		api, _ := parseObject(entry["message"])
		var pairedAPI storage.Object
		_ = json.Unmarshal(entry["message"], &pairedAPI)
		for index := range result {
			candidate := toStorage(result[index])
			var candidateAPI storage.Object
			_ = json.Unmarshal(candidate["message"], &candidateAPI)
			if sameRetainedFields(candidate, entry, "message") && sameRetainedFields(candidateAPI, pairedAPI, "content") {
				if original != nil && retainedLayout(*original) != retainedLayout(result[index]) {
					return nil, unsupported("ambiguous ordered history provenance")
				}
				original = &result[index]
			}
		}
		if original != nil {
			output = append(output, replaceContent(*original, api.Get("content")))
		} else {
			// The shared factory's map carries unknown fields. This known repair
			// shape restores Source insertion order for the API message only.
			entry["message"] = rawObject(object(field("role", api.Get("role")), field("content", api.Get("content"))))
			output = append(output, fromStorage(entry))
		}
	}
	if err := ValidateUserImagesForAPI(output); err != nil {
		return nil, err
	}
	return output, nil
}
