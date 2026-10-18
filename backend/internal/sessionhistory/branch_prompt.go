package sessionhistory

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// DeriveBranchFirstPrompt is the same original deriveFirstPrompt used by both
// createSessionBranch's default title and the command's in-memory fork LogOption.
// JSON retains the original UTF-16 slice, including a split surrogate at 100.
func DeriveBranchFirstPrompt(firstUser *protocol.JSONObject) json.RawMessage {
	if firstUser == nil {
		return quote("Branched conversation")
	}
	return branchFirstPrompt(*firstUser)
}
