package modelaccounting

import (
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// AgentContextBinding represents the original async-chain store, not
// options.agentId or a Source-observed boolean. The host creates a main binding
// only for an absent store; active chains bind their original context object.
// It creates no Owner and implements no mutable telemetry or provider policy.
type AgentContextBinding struct {
	owner    *cooperative.Owner
	original *protocol.JSONObject
}

func NewMainContext(owner *cooperative.Owner) (*AgentContextBinding, error) {
	if owner == nil {
		return nil, cooperative.ErrRestricted
	}
	return &AgentContextBinding{owner: owner}, nil
}
func NewAgentContext(owner *cooperative.Owner, original protocol.JSONObject) (*AgentContextBinding, error) {
	if owner == nil {
		return nil, cooperative.ErrRestricted
	}
	if !original.IsObject() {
		return nil, unsupported("original agent context object")
	}
	copy := original.Clone()
	return &AgentContextBinding{owner: owner, original: &copy}, nil
}
func (b *AgentContextBinding) Owns(owner *cooperative.Owner) bool {
	return b != nil && owner != nil && b.owner == owner
}

// Source checks !getAgentContext(), not an agent ID field or agent type. All
// represented original store objects are truthy, including an empty object.
func (b *AgentContextBinding) isMainChain(querySource string) bool {
	return b.original == nil && (querySource == "sdk" || strings.HasPrefix(querySource, "repl_main_thread"))
}
