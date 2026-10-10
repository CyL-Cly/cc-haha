package sdkhost

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func (h *Host) processNativeLocalInput(scope *cooperative.Scope, name, args string) (*sessioncontroller.ProcessedInput, error) {
	if h.options.Print.NativeLocalCommand == nil {
		return nil, cap("native local command /" + name)
	}
	start := h.options.NowMillis()
	tagged := "<command-name>/" + name + "</command-name>\n            <command-message>" + name + "</command-message>\n            <command-args>" + args + "</command-args>"
	user := messages.OrderedUser(h.options.Factory.CreateUser(storage.Object{"content": quote(tagged)}))
	_ = h.options.Factory.CreateLocalCaveat()
	if err := scope.AwaitFulfilled(); err != nil {
		return nil, err
	}
	text, err := h.options.Print.NativeLocalCommand(context.Background(), scope, name, args)
	if err != nil {
		return nil, err
	}
	if err = scope.AwaitFulfilled(); err != nil {
		return nil, err
	}
	system := h.options.Factory.CreateLocalCommandInput("<local-command-stdout>" + text + "</local-command-stdout>")
	if err = scope.AwaitFulfilled(); err != nil {
		return nil, err
	}
	caveat := h.options.Factory.CreateLocalCaveat()
	return &sessioncontroller.ProcessedInput{Messages: []protocol.JSONObject{caveat, user, system}, LocalResult: &sessioncontroller.LocalInputResult{Text: text, StartMillis: start}}, nil
}
