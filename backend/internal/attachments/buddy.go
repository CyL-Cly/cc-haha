package attachments

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/buddy"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func companionIntroContent(value protocol.JSONObject) json.RawMessage {
	return buddy.IntroText(value.Get("name"), value.Get("species"))
}
