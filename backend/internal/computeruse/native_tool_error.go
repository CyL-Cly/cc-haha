package computeruse

import (
	"encoding/json"
	"errors"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// NativeToolError keeps the native JavaScript Error.message's original units.
// Error() remains readable for ordinary Go logs; tool payloads use MessageJSON.
type NativeToolError struct{ MessageJSON json.RawMessage }

func (e *NativeToolError) Error() string {
	units, _ := protocol.DecodeJavaScriptJSONString(e.MessageJSON)
	return string(utf16.Decode(units))
}
func nativeErrorMessageJSON(err error) json.RawMessage {
	var native *NativeToolError
	if errors.As(err, &native) {
		return append(json.RawMessage{}, native.MessageJSON...)
	}
	return pixelQuoted(err.Error())
}
