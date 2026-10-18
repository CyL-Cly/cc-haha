package sdkhost

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func nativeAgentSummaryPrompt(previous json.RawMessage) (json.RawMessage, error) {
	line := quote("")
	var err error
	if previous != nil {
		line, err = protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quote("\nPrevious: \""), previous, quote("\" — say something NEW.\n")}, "")
		if err != nil {
			return nil, err
		}
	}
	return protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quote("Describe your most recent action in 3-5 words using present tense (-ing). Name the file or function, not the branch. Do not use tools.\n"), line, quote("\nGood: \"Reading runAgent.ts\"\nGood: \"Fixing null check in validate.ts\"\nGood: \"Running auth module tests\"\nGood: \"Adding retry logic to fetchUser\"\n\nBad (past tense): \"Analyzed the branch diff\"\nBad (too vague): \"Investigating the issue\"\nBad (too long): \"Reviewing full branch diff and AgentTool.tsx integration\"\nBad (branch name): \"Analyzed adam/background-summary branch diff\"")}, "")
}
