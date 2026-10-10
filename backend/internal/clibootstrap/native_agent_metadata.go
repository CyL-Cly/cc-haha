package clibootstrap

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Both foreground and detached Agent writers share runAgent's metadata
// projection. Resume's UI description is distinct from its persisted value.
func nativeAgentMetadata(request builtintools.AgentRequest, tree *nativeAgentWorktree, owner string, restarting bool) protocol.JSONObject {
	agentType := protocol.TrimJavaScriptString(request.PersistedAgentType)
	if agentType == "" {
		agentType = request.AgentType
	}
	metadata := protocol.NewJSONObject(protocol.JSONField{Name: "agentType", Value: quoteMain(agentType)})
	if request.Model != "" {
		metadata.Set("model", quoteMain(request.Model))
	}
	if tree.Info != nil {
		metadata.Set("worktreePath", quoteMain(tree.CWD))
	}
	if request.ResumeAgentID != "" && !restarting {
		if backgroundUsageTruthy(request.PersistedDescription) {
			metadata.Set("description", request.PersistedDescription)
		}
	} else if request.Description != "" {
		metadata.Set("description", quoteMain(request.Description))
	}
	if backgroundUsageTruthy(request.ToolUseID) {
		metadata.Set("toolUseId", request.ToolUseID)
	}
	if request.OwnerAgentID != "" {
		owner = request.OwnerAgentID
	}
	if owner != "" {
		metadata.Set("ownerAgentId", quoteMain(owner))
	}
	return metadata
}
