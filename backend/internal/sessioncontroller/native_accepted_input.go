package sessioncontroller

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

// The accepted command is not a prepared message or an alternate queue. It is
// consumed once by the actual NativeTools ASK's input processor on the owner.
type acceptedInputPreparation struct {
	input  command
	batch  []command
	target *protocol.JSONObject
}
