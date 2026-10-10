package sdkhost

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

func (h *Host) processNativeGoalInput(scope *cooperative.Scope, args string) (*sessioncontroller.ProcessedInput, error) {
	if h.options.Print.NativeGoalCommand == nil {
		return nil, cap("actual /goal local JSX executor")
	}
	start := h.options.NowMillis()
	text, shouldQuery, err := h.options.Print.NativeGoalCommand(context.Background(), scope, args)
	if err != nil {
		return nil, err
	}
	first := h.options.Factory.CreateLocalCommandInput("<command-name>/goal</command-name>\n            <command-message>goal</command-message>\n            <command-args>" + args + "</command-args>")
	second := h.options.Factory.CreateLocalCommandInput("<local-command-stdout>" + text + "</local-command-stdout>")
	return &sessioncontroller.ProcessedInput{Messages: []protocol.JSONObject{first, second}, NoQuery: !shouldQuery, StartMillis: start, AttachmentsHandled: true}, nil
}
