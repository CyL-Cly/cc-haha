package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func retainedNativeBashProgress(record protocol.JSONObject) bool {
	return messages.IsNativeBashProgress(record)
}
