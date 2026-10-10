package authhelper

import (
	"encoding/json"
	"slices"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// ConfiguredInSource is the original strict JS string equality after the
// settings schema. Comparing decoded Go strings would merge a lone UTF-16
// surrogate with a real replacement character and lose the trust boundary.
func ConfiguredInSource(configured, source json.RawMessage) bool {
	command, err := protocol.DecodeJavaScriptJSONString(configured)
	if err != nil || len(command) == 0 {
		return false
	}
	value, err := protocol.DecodeJavaScriptJSONString(source)
	return err == nil && slices.Equal(command, value)
}
