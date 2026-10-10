package contextusage

import (
	"encoding/json"
	"errors"
	"sort"

	"github.com/NanmiCoder/cc-haha/backend/internal/preflightcore"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type ToolDetail struct {
	Name   string  `json:"name"`
	Call   float64 `json:"callTokens"`
	Result float64 `json:"resultTokens"`
}
type Breakdown struct {
	ToolCalls       float64      `json:"toolCallTokens"`
	ToolResults     float64      `json:"toolResultTokens"`
	Attachments     float64      `json:"attachmentTokens"`
	Assistant       float64      `json:"assistantMessageTokens"`
	User            float64      `json:"userMessageTokens"`
	Tools           []ToolDetail `json:"toolCallsByType"`
	AttachmentTypes []Detail     `json:"attachmentsByType"`
}

func (in Input) messageBreakdown() (Breakdown, error) {
	result := Breakdown{Tools: []ToolDetail{}, AttachmentTypes: []Detail{}}
	ids := map[string]string{}
	for _, message := range in.History {
		if message.String("type") != "assistant" {
			continue
		}
		var blocks []protocol.JSONObject
		_ = json.Unmarshal(object(message.Get("message")).Get("content"), &blocks)
		for _, block := range blocks {
			if block.String("type") == "tool_use" && block.Get("id") != nil {
				ids[string(block.Get("id"))] = block.String("name")
			}
		}
	}
	calls, callOrder := map[string]float64{}, []string{}
	results, resultOrder := map[string]float64{}, []string{}
	attachmentCounts, attachmentOrder := map[string]float64{}, []string{}
	add := func(values map[string]float64, order *[]string, name string, tokens float64) {
		if name == "" {
			name = "unknown"
		}
		if _, exists := values[name]; !exists {
			*order = append(*order, name)
		}
		values[name] += tokens
	}
	for _, message := range in.History {
		kind := message.String("type")
		if kind == "attachment" {
			if in.AttachmentMessages == nil {
				return result, errors.New("context message breakdown requires original attachment projector")
			}
			projected, err := in.AttachmentMessages(message.Get("attachment"))
			if err != nil {
				return result, err
			}
			total := 0.0
			for _, entry := range projected {
				count, err := preflightcore.RoughContent(object(entry.Get("message")).Get("content"))
				if err != nil {
					return result, err
				}
				total += count
			}
			result.Attachments += total
			add(attachmentCounts, &attachmentOrder, object(message.Get("attachment")).String("type"), total)
			continue
		}
		if kind != "user" && kind != "assistant" {
			continue
		}
		content := object(message.Get("message")).Get("content")
		if kind == "user" && len(content) > 0 && content[0] == '"' {
			count, err := preflightcore.RoughContent(content)
			if err != nil {
				return result, err
			}
			result.User += count
			continue
		}
		var blocks []json.RawMessage
		if err := json.Unmarshal(content, &blocks); err != nil {
			return result, err
		}
		for _, raw := range blocks {
			row, _ := json.Marshal([]json.RawMessage{raw})
			count, err := preflightcore.RoughContent(row)
			if err != nil {
				return result, err
			}
			block := object(raw)
			if kind == "assistant" {
				if block.String("type") == "tool_use" {
					result.ToolCalls += count
					add(calls, &callOrder, block.String("name"), count)
				} else {
					result.Assistant += count
				}
			} else if block.String("type") == "tool_result" {
				result.ToolResults += count
				add(results, &resultOrder, ids[string(block.Get("tool_use_id"))], count)
			} else {
				result.User += count
			}
		}
	}
	seen := map[string]bool{}
	for _, name := range append(callOrder, resultOrder...) {
		if seen[name] {
			continue
		}
		seen[name] = true
		result.Tools = append(result.Tools, ToolDetail{name, calls[name], results[name]})
	}
	sort.SliceStable(result.Tools, func(i, j int) bool {
		return result.Tools[i].Call+result.Tools[i].Result > result.Tools[j].Call+result.Tools[j].Result
	})
	if len(result.Tools) > 5 {
		result.Tools = result.Tools[:5]
	}
	for _, name := range attachmentOrder {
		result.AttachmentTypes = append(result.AttachmentTypes, Detail{name, attachmentCounts[name]})
	}
	sort.SliceStable(result.AttachmentTypes, func(i, j int) bool { return result.AttachmentTypes[i].Tokens > result.AttachmentTypes[j].Tokens })
	if len(result.AttachmentTypes) > 5 {
		result.AttachmentTypes = result.AttachmentTypes[:5]
	}
	return result, nil
}
