package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
)

// This is the original agent initialMessages stage, before the first prompt.
// It is unavailable to a main/Team session and uses the same serial Controller.
func (h *Host) InstallNativeAgentPrelude(scope *cooperative.Scope, rows []protocol.JSONObject) error {
	if e := h.checkInferenceOwner(scope); e != nil {
		return e
	}
	if !h.isAgent() || h.options.Print.AgentID == "" || len(h.serial.History()) != 0 {
		return cooperative.ErrRestricted
	}
	return h.options.Controller.InstallAgentPrelude(scope, rows)
}
func (h *Host) AppendNativeAgentMCPTools(scope *cooperative.Scope, names []string) error {
	if e := h.checkInferenceOwner(scope); e != nil {
		return e
	}
	if !h.isAgent() || h.options.Print.NativeMCP == nil || h.options.Print.OnlyToolNames == nil {
		return cooperative.ErrRestricted
	}
	_, actual, e := h.options.Print.NativeMCP.Catalog(scope)
	if e != nil {
		return e
	}
	allowed := map[string]bool{}
	for _, tool := range actual {
		allowed[tool.Name] = true
	}
	next := append([]string{}, (*h.options.Print.OnlyToolNames)...)
	for _, name := range names {
		if !strings.HasPrefix(name, "mcp__") || !allowed[name] {
			return cooperative.ErrRestricted
		}
		present := false
		for _, old := range next {
			present = present || old == name
		}
		if !present {
			next = append(next, name)
		}
	}
	*h.options.Print.OnlyToolNames = next
	return h.initializePrintCatalog(scope)
}
