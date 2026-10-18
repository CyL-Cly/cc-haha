package sdkhost

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (h *Host) captureSkillCallOverrides(opaque any) error {
	call, ok := opaque.(*builtinwrite.CallContext)
	if !ok || call == nil {
		return cap("actual Skill modifier call context")
	}
	if call.SkillOverrides == nil {
		h.askSkillOverrides = nil
		return nil
	}
	copy := call.SkillOverrides.Clone()
	h.askSkillOverrides = &copy
	if copy.Get("model") != nil {
		h.askFallbackModel = ""
	}
	model, e := h.skillMainModel(call.MainLoopModel)
	if e != nil {
		return e
	}
	call.MainLoopModel = model
	return nil
}
func (h *Host) skillMainModel(fallback string) (string, error) {
	if h.askFallbackModel != "" {
		return h.askFallbackModel, nil
	}
	if h.askSkillOverrides == nil || h.askSkillOverrides.Get("model") == nil {
		return fallback, nil
	}
	raw := h.askSkillOverrides.Get("model")
	var model string
	if e := json.Unmarshal(raw, &model); e != nil {
		return "", e
	}
	if model == "" {
		return "", cap("empty Skill model override")
	}
	return model, nil
}
func (h *Host) overlaySkillPermission(context protocol.JSONObject) (protocol.JSONObject, error) {
	if h.askSkillOverrides == nil || h.askSkillOverrides.Get("commandRules") == nil {
		return context, nil
	}
	var rules []json.RawMessage
	if e := json.Unmarshal(h.askSkillOverrides.Get("commandRules"), &rules); e != nil {
		return protocol.JSONObject{}, e
	}
	return mergeNativeSkillCommandRules(context, rules)
}

// Source Set insertion identity, shared by inline and fork-only permission views.
func mergeNativeSkillCommandRules(context protocol.JSONObject, rules []json.RawMessage) (protocol.JSONObject, error) {
	out := context.Clone()
	var allow protocol.JSONObject
	if e := allow.UnmarshalJSON(out.Get("alwaysAllowRules")); e != nil {
		return protocol.JSONObject{}, e
	}
	var existing []json.RawMessage
	if raw := allow.Get("command"); raw != nil {
		if e := json.Unmarshal(raw, &existing); e != nil {
			return protocol.JSONObject{}, e
		}
	}
	merged := []json.RawMessage{}
	seen := map[string]bool{}
	for _, rule := range append(existing, rules...) {
		units, e := protocol.DecodeJavaScriptJSONString(rule)
		if e != nil {
			return protocol.JSONObject{}, e
		}
		canonical := protocol.QuoteJavaScriptUTF16(units)
		if !seen[string(canonical)] {
			seen[string(canonical)] = true
			merged = append(merged, canonical)
		}
	}
	raw, e := json.Marshal(merged)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	allow.Set("command", raw)
	raw, e = allow.MarshalJSON()
	if e != nil {
		return protocol.JSONObject{}, e
	}
	out.Set("alwaysAllowRules", raw)
	return out, nil
}
