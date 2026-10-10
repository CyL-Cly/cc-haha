package modelyield

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/apipresentation"
)

func originalThrow(s string) error {
	raw, _ := json.Marshal(s)
	e, _ := apipresentation.NewOriginalError(raw)
	return e
}
func originalPolicyForTest() *StreamErrorPolicy {
	p, _ := StreamErrorPolicyFromOriginalEnvironment(map[string]string{"CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK": "1", "CLAUDE_STREAM_TRANSIENT_RETRY_MAX": "0", "CLAUDE_ENABLE_STREAM_WATCHDOG": "0"})
	return p
}
