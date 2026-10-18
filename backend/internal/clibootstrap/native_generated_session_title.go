package clibootstrap

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessiontitle"
)

//go:embed native_generated_session_title_prompt.md
var nativeGeneratedSessionTitlePrompt string

// GenerateCLISessionTitle is the distinct generateSessionTitle queryHaiku
// consumer used after the first successful input to an empty CCR session.
func (m *MainPrint) GenerateCLISessionTitle(ctx context.Context, description json.RawMessage) (json.RawMessage, error) {
	prompt, err := sessiontitle.CleanRaw(description)
	if err != nil {
		return nil, err
	}
	if string(prompt) == `""` {
		return nil, nil
	}
	var text string
	err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if m.Host == nil || m.nativeModelSelector == nil {
			return cooperative.ErrRestricted
		}
		var failure error
		text, failure = m.Host.QueryGeneratedSessionTitle(ctx, scope, prompt, []json.RawMessage{quoteMain(nativeGeneratedSessionTitlePrompt)}, m.nativeModelSelector.SmallFastModel())
		return failure
	})
	if err != nil {
		return nil, err
	}
	var result protocol.JSONObject
	if result.UnmarshalJSON([]byte(text)) != nil || !result.IsObject() {
		return nil, errors.New("Invalid generated session title JSON")
	}
	title := result.Get("title")
	if _, err := protocol.DecodeJavaScriptJSONString(title); err != nil {
		return nil, errors.New("Invalid generated session title string")
	}
	normalized, valid, err := sessiontitle.NormalizeRaw(title)
	if err != nil || !valid {
		return nil, err
	}
	return normalized, nil
}
