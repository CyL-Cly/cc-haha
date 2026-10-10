package requestcontext

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func prependNativeDeferredTools(history []protocol.JSONObject, names []json.RawMessage, dependencies Dependencies) ([]protocol.JSONObject, error) {
	if len(names) == 0 {
		return history, nil
	}
	ordered := append([]json.RawMessage{}, names...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, _ := protocol.DecodeJavaScriptJSONString(ordered[i])
		b, _ := protocol.DecodeJavaScriptJSONString(ordered[j])
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return len(a) < len(b)
	})
	joined, e := protocol.JoinJavaScriptValuesJSON(ordered, "\n")
	if e != nil {
		return nil, e
	}
	text, e := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quoted("<available-deferred-tools>\n"), joined, quoted("\n</available-deferred-tools>")}, "")
	if e != nil {
		return nil, e
	}
	factory, e := messages.NewFactory(dependencies.UUID, dependencies.Now)
	if e != nil {
		return nil, e
	}
	header := messages.OrderedUser(factory.CreateUser(storage.Object{"content": text, "isMeta": json.RawMessage("true")}))
	return append([]protocol.JSONObject{header}, history...), nil
}

// FilterUnavailableToolReferences is the optimistic normalization stage. It
// keeps malformed/falsy names as Source does and removes references only when
// their truthy name is absent from the actual catalogue.
func FilterUnavailableToolReferences(record protocol.JSONObject, names []json.RawMessage) (protocol.JSONObject, error) {
	available := map[string]bool{}
	for _, name := range names {
		available[string(protocol.StringifyValidatedJavaScriptJSON(name))] = true
	}
	api, e := parseObject(record.Get("message"))
	if e != nil {
		return protocol.JSONObject{}, e
	}
	content := api.Get("content")
	if len(content) == 0 || content[0] != '[' {
		return record, nil
	}
	blocks := blocks(content)
	unavailable := func(part protocol.JSONObject) bool {
		if part.String("type") != "tool_reference" || !truthy(part.Get("tool_name")) {
			return false
		}
		name := part.Get("tool_name")
		for legacy, canonical := range map[string]string{"Task": "Agent", "AgentOutputTool": "TaskOutput", "BashOutputTool": "TaskOutput", "KillShell": "TaskStop"} {
			if stringValue(name) == legacy {
				name = quoted(canonical)
				break
			}
		}
		return !available[string(protocol.StringifyValidatedJavaScriptJSON(name))]
	}
	found := false
	for _, block := range blocks {
		if block.String("type") != "tool_result" {
			continue
		}
		for _, part := range blocksFromRaw(block.Get("content")) {
			found = found || unavailable(part)
		}
	}
	if !found {
		return record, nil
	}
	for i, block := range blocks {
		value := block.Get("content")
		if block.String("type") != "tool_result" || len(value) == 0 || value[0] != '[' {
			continue
		}
		kept := []protocol.JSONObject{}
		for _, part := range blocksFromRaw(value) {
			if !unavailable(part) {
				kept = append(kept, part)
			}
		}
		if len(kept) == 0 {
			kept = append(kept, object(field("type", quoted("text")), field("text", quoted("[Tool references removed - tools no longer available]"))))
		}
		copy := block.Clone()
		data, _ := json.Marshal(kept)
		copy.Set("content", data)
		blocks[i] = copy
	}
	value, _ := json.Marshal(blocks)
	api = api.Clone()
	api.Set("content", value)
	copy := record.Clone()
	copy.Set("message", rawObject(api))
	return copy, nil
}
func blocksFromRaw(raw json.RawMessage) []protocol.JSONObject { return blocks(raw) }

// EnsureToolReferenceTurnBoundary is Source's ordinary API normalization
// fallback when the tool-reference relocation experiment is inactive. It runs
// after reference filtering and is idempotent across SDK and request passes.
func EnsureToolReferenceTurnBoundary(record protocol.JSONObject) protocol.JSONObject {
	if record.String("type") != "user" {
		return record
	}
	parts := blocks(content(record))
	found := false
	for _, part := range parts {
		if part.String("type") == "text" && strings.HasPrefix(part.String("text"), "Tool loaded.") {
			return record
		}
		if part.String("type") == "tool_result" {
			for _, nested := range blocks(part.Get("content")) {
				found = found || nested.String("type") == "tool_reference"
			}
		}
	}
	if !found {
		return record
	}
	parts = append(parts, object(field("type", quoted("text")), field("text", quoted("Tool loaded."))))
	raw, _ := json.Marshal(parts)
	return replaceContent(record, raw)
}

func stripSearchFieldsAfterModelGate(history []protocol.JSONObject) []protocol.JSONObject {
	out := make([]protocol.JSONObject, len(history))
	for i, record := range history {
		out[i] = record
		if record.String("type") == "user" {
			out[i] = replaceContent(record, stripToolReferences(content(record)))
			continue
		}
		if record.String("type") != "assistant" {
			continue
		}
		parts := blocks(content(record))
		found := false
		for _, part := range parts {
			found = found || part.String("type") == "tool_use" && part.Get("caller") != nil && string(part.Get("caller")) != "null"
		}
		if !found {
			continue
		}
		for j, part := range parts {
			if part.String("type") == "tool_use" {
				parts[j] = object(field("type", quoted("tool_use")), field("id", part.Get("id")), field("name", part.Get("name")), field("input", part.Get("input")))
			}
		}
		value, _ := json.Marshal(parts)
		out[i] = replaceContent(record, value)
	}
	return out
}
