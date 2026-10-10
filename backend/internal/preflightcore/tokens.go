package preflightcore

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/attachments"
	"math"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func roughString(raw json.RawMessage) (float64, error) {
	if len(raw) == 0 {
		return 0, typeError("undefined is not an object (evaluating 'content.length')")
	}
	if isNull(raw) {
		return 0, capability("unproved null string length error")
	}
	if !isString(raw) {
		return math.NaN(), nil
	}
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return 0, err
	}
	return math.Floor(float64(len(units))/4 + 0.5), nil
}
func roughContent(raw json.RawMessage) (float64, error) {
	if !truthy(raw) {
		return 0, nil
	}
	if isString(raw) {
		return roughString(raw)
	}
	if !isArray(raw) {
		if _, ok := float(raw); ok {
			return 0, typeError("number is not iterable")
		}
		return 0, capability("unproved non-iterable content")
	}
	blocks, err := array(raw)
	if err != nil {
		return 0, err
	}
	total := 0.0
	for _, block := range blocks {
		count, err := roughBlock(block)
		if err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}
func roughBlock(raw json.RawMessage) (float64, error) {
	if isString(raw) {
		return roughString(raw)
	}
	if isNull(raw) {
		return 0, typeError("null is not an object (evaluating 'block.type')")
	}
	block := object(raw)
	switch block.String("type") {
	case "text":
		return roughString(block.Get("text"))
	case "image", "document":
		return 2000, nil
	case "tool_result":
		return roughContent(block.Get("content"))
	case "tool_use":
		name := block.Get("name")
		if !isString(name) {
			return 0, capability("unproved tool name additive coercion")
		}
		input := block.Get("input")
		if len(input) == 0 || isNull(input) {
			input = []byte("{}")
		}
		serialized, err := stringify(input)
		if err != nil {
			return 0, err
		}
		nameUnits, _ := protocol.DecodeJavaScriptJSONString(name)
		serializedRaw, _ := json.Marshal(serialized)
		units, _ := protocol.DecodeJavaScriptJSONString(serializedRaw)
		return math.Floor(float64(len(nameUnits)+len(units))/4 + 0.5), nil
	case "thinking":
		return roughString(block.Get("thinking"))
	case "redacted_thinking":
		data := block.Get("data")
		if !isString(data) {
			return 0, capability("unproved redacted reasoning type error")
		}
		units, _ := protocol.DecodeJavaScriptJSONString(data)
		plain := text(data)
		const prefix = "cc-haha:openai-reasoning:v1:"
		if strings.HasPrefix(plain, prefix) {
			suffix := strings.TrimPrefix(plain, prefix)
			if !json.Valid([]byte(suffix)) {
				return 0, capability("unproved reasoning-envelope JSON parser/depth")
			}
			var envelope protocol.JSONObject
			if json.Unmarshal([]byte(suffix), &envelope) == nil && envelope.IsObject() {
				encrypted := envelope.Get("encrypted_content")
				id := envelope.Get("id")
				summary := envelope.Get("summary")
				if isString(encrypted) && truthy(encrypted) && (len(id) == 0 || isString(id)) && isArray(summary) {
					entries, _ := array(summary)
					valid := true
					length := 0
					for index, entry := range entries {
						part := object(entry)
						if !part.IsObject() || !isString(part.Get("type")) || !isString(part.Get("text")) {
							valid = false
							break
						}
						textUnits, _ := protocol.DecodeJavaScriptJSONString(part.Get("text"))
						length += len(textUnits)
						if index > 0 {
							length++
						}
					}
					if valid {
						return math.Floor(float64(length)/4 + 0.5), nil
					}
				}
			}
		}
		return math.Floor(float64(len(units))/4 + 0.5), nil
	default:
		serialized, err := stringify(raw)
		if err != nil {
			return 0, err
		}
		quoted, _ := json.Marshal(serialized)
		return roughString(quoted)
	}
}
func roughHistory(history []*record) (float64, error) {
	return roughHistoryWithAttachments(history, nil)
}
func roughHistoryWithAttachments(history []*record, options *AttachmentOptions) (float64, error) {
	total := 0.0
	for _, r := range history {
		switch r.Object.String("type") {
		case "attachment":
			if !truthy(r.Object.Get("attachment")) {
				continue
			}
			if attachments.Invisible(r.Object.Get("attachment")) {
				continue
			}
			if options == nil || options.Factory == nil {
				return 0, capability("raw retained attachment estimation/allocations/error propagation")
			}
			projected, err := attachments.ProjectWithOptions(r.Object.Get("attachment"), options.Factory, options.Plan)
			if err != nil {
				return 0, err
			}
			for _, user := range projected {
				count, err := roughContent(object(user.Get("message")).Get("content"))
				if err != nil {
					return 0, err
				}
				total += count
			}
		case "user", "assistant":
			raw := property(r.Object.Get("message"), "content")
			if truthy(raw) {
				count, err := roughContent(raw)
				if err != nil {
					return 0, err
				}
				total += count
			}
		}
	}
	return total, nil
}
func usage(r *record) json.RawMessage {
	if r.Object.String("type") != "assistant" {
		return nil
	}
	api := object(r.Object.Get("message"))
	if api.String("model") == messages.SyntheticModel {
		return nil
	}
	content := api.Get("content")
	if isArray(content) {
		blocks, _ := array(content)
		if len(blocks) > 0 && text(property(blocks[0], "type")) == "text" {
			value := text(property(blocks[0], "text"))
			switch value {
			case messages.Interrupt, messages.InterruptForToolUse, "No response requested.":
				return nil
			}
		}
	}
	return api.Get("usage")
}
func safeNumber(raw json.RawMessage) (float64, bool) {
	value, ok := float(raw)
	return value, ok && !math.IsInf(value, 0) && !math.IsNaN(value) && value >= 0
}
func validUsageShape(raw json.RawMessage) bool {
	u := object(raw)
	_, input := safeNumber(u.Get("input_tokens"))
	_, output := safeNumber(u.Get("output_tokens"))
	if !input || !output {
		return false
	}
	for _, key := range []string{"cache_creation_input_tokens", "cache_read_input_tokens"} {
		if value := u.Get(key); value != nil {
			if _, valid := safeNumber(value); !valid {
				return false
			}
		}
	}
	return true
}
func usable(raw json.RawMessage) (bool, error) {
	if isNull(raw) {
		return false, typeError("null is not an object (evaluating 'usage.input_tokens')")
	}
	u := object(raw)
	input, a := safeNumber(u.Get("input_tokens"))
	_, b := safeNumber(u.Get("output_tokens"))
	sum := input
	valid := a && b
	for _, key := range []string{"cache_creation_input_tokens", "cache_read_input_tokens"} {
		value := u.Get(key)
		if len(value) == 0 {
			continue
		}
		count, ok := safeNumber(value)
		valid = valid && ok
		sum += count
	}
	return valid && sum > 0, nil
}
func hasUsable(history []*record) (bool, error) {
	for _, r := range history {
		raw := usage(r)
		if len(raw) == 0 {
			continue
		}
		ok, err := usable(raw)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}
func usageTotal(raw json.RawMessage) float64 {
	sum := 0.0
	u := object(raw)
	for _, key := range []string{"input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "output_tokens"} {
		if value, ok := safeNumber(u.Get(key)); ok {
			sum += value
		}
	}
	return sum
}
func responseID(r *record) json.RawMessage {
	if r.Object.String("type") != "assistant" {
		return nil
	}
	api := object(r.Object.Get("message"))
	if api.String("model") == messages.SyntheticModel {
		return nil
	}
	return api.Get("id")
}
func sameID(a, b json.RawMessage) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	if isString(a) && isString(b) {
		left, _ := protocol.DecodeJavaScriptJSONString(a)
		right, _ := protocol.DecodeJavaScriptJSONString(b)
		if len(left) != len(right) {
			return false
		}
		for i := range left {
			if left[i] != right[i] {
				return false
			}
		}
		return true
	}
	return bytes.Equal(a, b)
}
func tokenCount(history []*record) (float64, error) { return tokenCountWithAttachments(history, nil) }
func tokenCountWithAttachments(history []*record, options *AttachmentOptions) (float64, error) {
	for index := len(history) - 1; index >= 0; index-- {
		r := history[index]
		raw := usage(r)
		if !truthy(raw) {
			continue
		}
		// Source's truthy usage guard above excludes the only error shape,
		// JSON null. hasUsable independently reaches and reports that error.
		ok, _ := usable(raw)
		if !ok {
			continue
		}
		anchor := index
		id := responseID(r)
		tail := history[index+1:]
		if truthy(id) {
			if !isString(id) {
				return 0, capability("unproved non-string response ID equality")
			}
			for j := index - 1; j >= 0; j-- {
				prior := responseID(history[j])
				if sameID(prior, id) {
					anchor = j
				} else if len(prior) > 0 {
					break
				}
			}
			tail = make([]*record, 0, len(history)-anchor-1)
			for _, candidate := range history[anchor+1:] {
				if !sameID(responseID(candidate), id) {
					tail = append(tail, candidate)
				}
			}
		}
		unreported := 0.0
		output, outputIsNumber := float(property(raw, "output_tokens"))
		if outputIsNumber && output == 0 {
			visible := []*record{r}
			if truthy(id) {
				visible = make([]*record, 0)
				for _, candidate := range history {
					if sameID(responseID(candidate), id) {
						visible = append(visible, candidate)
					}
				}
			}
			var err error
			unreported, err = roughHistoryWithAttachments(visible, options)
			if err != nil {
				return 0, err
			}
		}
		after, err := roughHistoryWithAttachments(tail, options)
		if err != nil {
			return 0, err
		}
		total := usageTotal(raw) + unreported + after
		if math.IsInf(total, 0) {
			return 0, capability("unproved infinite usage-sum estimate")
		}
		return total, nil
	}
	return roughHistoryWithAttachments(history, options)
}
