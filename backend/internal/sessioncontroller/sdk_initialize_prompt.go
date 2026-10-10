package sessioncontroller

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// The SDK's mutable options replace the effective main prompt before the next
// ASK. Empty/null clears a prior custom prompt and restores the real default
// producer, rather than retaining a constructor-time prompt snapshot.
func (c *Controller) SetNativeSDKSystemPrompt(scope *cooperative.Scope, value json.RawMessage, fallback func(context.Context, []protocol.JSONObject, string) ([]json.RawMessage, error)) error {
	if c == nil || c.options.Bootstrap == nil || !scope.BelongsTo(c.options.Bootstrap.Owner()) || !c.active || !c.options.PublicPrint || !c.options.NativeTools || c.options.WriterOptions.AgentID != "" {
		return cooperative.ErrRestricted
	}
	if e := c.options.Bootstrap.CheckScope(scope); e != nil {
		return e
	}
	if string(value) == "null" || string(value) == `""` {
		if fallback == nil {
			return cooperative.ErrRestricted
		}
		c.options.CustomSystemPrompt = nil
		c.options.ResolveSystemPrompt = nil
		c.options.ResolveSystemPromptForModel = fallback
		return nil
	}
	if _, e := protocol.DecodeJavaScriptJSONString(value); e != nil {
		return e
	}
	c.options.CustomSystemPrompt = append(json.RawMessage(nil), value...)
	c.options.ResolveSystemPrompt = nil
	c.options.ResolveSystemPromptForModel = nil
	return nil
}
