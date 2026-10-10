package sdkhost

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func originalInputAttachmentText(initial *protocol.JSONObject) (string, bool, error) {
	if initial == nil {
		return "", false, cap("missing original user for input attachments")
	}
	api, present := initial.Object("message")
	if !present {
		return "", false, cap("original input message for attachments")
	}
	content := api.Get("content")
	var text string
	if e := json.Unmarshal(content, &text); e == nil {
		return text, true, nil
	}
	var blocks []protocol.JSONObject
	if e := json.Unmarshal(content, &blocks); e != nil {
		return "", false, cap("original input content for attachments")
	}
	// processUserInput extracts only the last text block as inputString;
	// preceding blocks remain in the user message, never a discovery signal.
	if len(blocks) > 0 && blocks[len(blocks)-1].String("type") == "text" {
		if e := json.Unmarshal(blocks[len(blocks)-1].Get("text"), &text); e != nil {
			return "", false, e
		}
		return text, true, nil
	}
	return "", false, nil
}
