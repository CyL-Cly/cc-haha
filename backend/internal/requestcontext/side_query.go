package requestcontext

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// SideQueryInput is the original sideQuery options, not prepared API params.
type SideQueryInput struct {
	System          []protocol.JSONObject
	Messages        []protocol.JSONObject
	Tools           []protocol.JSONObject
	ToolChoice      protocol.JSONObject
	MaxTokens       float64
	Temperature     *float64
	DisableThinking bool
	SkipPrefix      bool
}

func NativeSideQueryRequest(input Context, settings protocol.JSONObject, opts SideQueryInput) (protocol.JSONObject, error) {
	base, e := NativeModelValidationRequest(input, settings)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	history := []protocol.JSONObject{}
	for _, m := range opts.Messages {
		history = append(history, object(field("type", m.Get("role")), field("message", rawObject(m))))
	}
	system := []protocol.JSONObject{}
	if !envFalse(input.Host.Env["CLAUDE_CODE_ATTRIBUTION_HEADER"]) {
		entry, ok := input.Host.Env["CLAUDE_CODE_ENTRYPOINT"]
		if !ok {
			entry = "unknown"
		}
		workload := ""
		if input.Host.Workload != "" {
			workload = " cc_workload=" + input.Host.Workload + ";"
		}
		system = append(system, object(field("type", quoted("text")), field("text", quoted("x-anthropic-billing-header: cc_version="+compatibilityVersion+"."+fingerprint(history)+"; cc_entrypoint="+entry+"; cch=00000;"+workload))))
	}
	if !opts.SkipPrefix {
		system = append(system, object(field("type", quoted("text")), field("text", quoted(cliPrefix))))
	}
	system = append(system, opts.System...)
	max := opts.MaxTokens
	_, required, adaptive := nativeThinkingSupport(input)
	useAuto := required && adaptive && (opts.ToolChoice.String("type") == "tool" || opts.ToolChoice.String("type") == "any")
	if required && adaptive {
		_, upper := nativeModelLimits(input.Options.Model)
		max += 2048
		if max > upper {
			max = upper
		}
	}
	tools := opts.Tools
	choice := opts.ToolChoice
	if useAuto {
		if choice.String("type") == "tool" {
			tools = []protocol.JSONObject{}
			for _, t := range opts.Tools {
				if t.String("name") == choice.String("name") {
					tools = append(tools, t)
				}
			}
		}
		prompt := "Respond by calling one of the provided tools with the complete result. Do not substitute a text response for the tool call."
		if choice.String("type") == "tool" {
			prompt = "Respond by calling the " + choice.String("name") + " tool exactly once with the complete result. Do not substitute a text response for the tool call."
		}
		system = append(system, object(field("type", quoted("text")), field("text", quoted(prompt))))
		choice = object(field("type", quoted("auto")), field("disable_parallel_tool_use", json.RawMessage("true")))
	}
	p := object(field("model", base.Get("model")), field("max_tokens", rawNumber(max)), field("system", encodeSide(system)), field("messages", encodeSide(opts.Messages)))
	if opts.Tools != nil {
		p.Set("tools", encodeSide(tools))
	}
	if opts.ToolChoice.IsObject() {
		p.Set("tool_choice", rawObject(choice))
	}
	if opts.Temperature != nil && !(required && adaptive) {
		p.Set("temperature", rawNumber(*opts.Temperature))
	}
	if required && adaptive {
		p.Set("thinking", base.Get("thinking"))
	} else if opts.DisableThinking {
		p.Set("thinking", json.RawMessage(`{"type":"disabled"}`))
	}
	if beta := base.Get("betas"); beta != nil {
		p.Set("betas", beta)
	}
	p.Set("metadata", base.Get("metadata"))
	return p, nil
}
func encodeSide(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
