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
	ctx := context.Background()
	if name == "keybindings" && h.inputModelContext != nil {
		ctx = h.inputModelContext
	}
	text, err := h.options.Print.NativeLocalCommand(ctx, scope, name, args)
	resultText := text
	tag := "stdout"
	if err != nil {
		if name != "keybindings" {
			return nil, err
		}
		text, tag = storage.JavaScriptErrorText(err), "stderr"
		resultText = ""
	}
	if err = scope.AwaitFulfilled(); err != nil {
		return nil, err
	}
	system := h.options.Factory.CreateLocalCommandInput("<local-command-" + tag + ">" + text + "</local-command-" + tag + ">")
	if err = scope.AwaitFulfilled(); err != nil {
		return nil, err
	}
	caveat := h.options.Factory.CreateLocalCaveat()
	return &sessioncontroller.ProcessedInput{Messages: []protocol.JSONObject{caveat, user, system}, LocalResult: &sessioncontroller.LocalInputResult{Text: text, ResultText: &resultText, StartMillis: start}}, nil
}
