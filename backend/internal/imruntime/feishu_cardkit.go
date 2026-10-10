package imruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"math"
	"time"
)

const FeishuStreamingElementID = "streaming_content"

type FeishuCardKitError struct {
	API     string
	Code    json.RawMessage
	Msg     Text
	Context string
}

func (e *FeishuCardKitError) Error() string {
	return fmt.Sprintf("cardkit %s FAILED: code=%s, msg=%s, %s", e.API, jsString(e.Code), e.Msg.String(), e.Context)
}
func feishuCardOK(response json.RawMessage, api, detail string) error {
	v := object(response)
	code := v.Get("code")
	if len(code) > 0 {
		n, number := wechatNumeric(code)
		if !number || n != 0 {
			msg := Text{}
			if x := v.Get("msg"); isString(x) {
				msg = TextJSON(x)
			}
			return &FeishuCardKitError{api, code, msg, detail}
		}
	}
	return nil
}
func (c *FeishuClient) cardRequest(ctx context.Context, api string, request func(context.Context) (json.RawMessage, error)) (json.RawMessage, error) {
	timeout := 15000.0
	if s := c.o.Environment["CC_HAHA_IM_CARD_REQUEST_TIMEOUT_MS"]; s != "" {
		n := qqNumber(raw(s))
		if !math.IsNaN(n) && !math.IsInf(n, 0) && n > 0 {
			timeout = n
		}
	}
	type result struct {
		body json.RawMessage
		err  error
	}
	done := make(chan result, 1)
	// Source Promise.race rejects its waiter but does not abort the submitted
	// SDK HTTP request. Its lifetime is still joined/cancelled by Client.Close.
	go func() { p, e := request(c.ctx); done <- result{p, e} }()
	timer := time.NewTimer(feishuTimer(timeout))
	defer timer.Stop()
	select {
	case r := <-done:
		return r.body, r.err
	case <-timer.C:
		return nil, fmt.Errorf("%s timed out after %gms", api, timeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.ctx.Done():
		return nil, c.ctx.Err()
	}
}
func (c *FeishuClient) CreateCardEntity(ctx context.Context, card json.RawMessage) (json.RawMessage, error) {
	compact := protocol.StringifyValidatedJavaScriptJSON(card)
	detail := fmt.Sprintf("cardLen=%d", len(NewText(string(compact))))
	response, e := c.cardRequest(ctx, "card.create", func(ctx context.Context) (json.RawMessage, error) {
		return c.Request(ctx, "POST", "/open-apis/cardkit/v1/cards", protocol.NewJSONObject(), raw(feishuObject(feishuField("type", "card_json"), feishuField("data", NewText(string(compact))))))
	})
	if e != nil {
		return nil, e
	}
	if e = feishuCardOK(response, "card.create", detail); e != nil {
		return nil, e
	}
	value := object(object(response).Get("data")).Get("card_id")
	if len(value) == 0 || string(value) == "null" {
		value = object(response).Get("card_id")
	}
	if !credentialTruthy(value) {
		code := object(response).Get("code")
		if len(code) == 0 || string(code) == "null" {
			code = raw(-1)
		}
		prefix := NewText(string(protocol.StringifyValidatedJavaScriptJSON(response)))
		return nil, &FeishuCardKitError{"card.create", code, NewText("response missing card_id"), "resp=" + prefix.Slice(0, 200).String()}
	}
	return value, nil
}
func (c *FeishuClient) SendCardAsMessage(ctx context.Context, chat Text, cardID json.RawMessage, reply Text, uuid json.RawMessage) (json.RawMessage, error) {
	content := NewText(string(raw(feishuObject(feishuField("type", "card"), feishuField("data", feishuObject(protocol.JSONField{Name: "card_id", Value: cardID}))))))
	api := "im.message.create"
	if len(reply) > 0 {
		api = "im.message.reply"
	}
	response, e := c.cardRequest(ctx, api, func(ctx context.Context) (json.RawMessage, error) {
		if len(reply) > 0 {
			return c.ReplyMessage(ctx, reply, "interactive", content, uuid)
		}
		return c.CreateMessage(ctx, chat, "interactive", content, uuid)
	})
	if e != nil {
		return nil, e
	}
	value := object(object(response).Get("data")).Get("message_id")
	if !credentialTruthy(value) {
		detail := "cardId=" + jsString(cardID)
		if len(reply) == 0 {
			detail = "chatId=" + chat.String() + " " + detail
		}
		return nil, &FeishuCardKitError{api, raw(-1), NewText("response missing message_id"), detail}
	}
	return value, nil
}
func (c *FeishuClient) StreamCardContent(ctx context.Context, cardID, elementID Text, content Text, sequence int64) error {
	detail := fmt.Sprintf("seq=%d len=%d", sequence, len(content))
	response, e := c.cardRequest(ctx, "cardElement.content", func(ctx context.Context) (json.RawMessage, error) {
		return c.Request(ctx, "PUT", "/open-apis/cardkit/v1/cards/"+cardID.String()+"/elements/"+elementID.String()+"/content", protocol.NewJSONObject(), raw(feishuObject(feishuField("content", content), feishuField("sequence", sequence))))
	})
	if e != nil {
		return e
	}
	return feishuCardOK(response, "cardElement.content", detail)
}
func (c *FeishuClient) SetCardStreamingMode(ctx context.Context, cardID Text, streaming bool, sequence int64) error {
	response, e := c.cardRequest(ctx, "card.settings", func(ctx context.Context) (json.RawMessage, error) {
		return c.Request(ctx, "PATCH", "/open-apis/cardkit/v1/cards/"+cardID.String()+"/settings", protocol.NewJSONObject(), raw(feishuObject(feishuField("settings", NewText(string(raw(feishuObject(feishuField("streaming_mode", streaming)))))), feishuField("sequence", sequence))))
	})
	if e != nil {
		return e
	}
	return feishuCardOK(response, "card.settings", fmt.Sprintf("seq=%d streaming_mode=%t", sequence, streaming))
}
func (c *FeishuClient) UpdateCardKitCard(ctx context.Context, cardID Text, card json.RawMessage, sequence int64) error {
	response, e := c.cardRequest(ctx, "card.update", func(ctx context.Context) (json.RawMessage, error) {
		return c.Request(ctx, "PUT", "/open-apis/cardkit/v1/cards/"+cardID.String(), protocol.NewJSONObject(), raw(feishuObject(feishuField("card", feishuObject(feishuField("type", "card_json"), feishuField("data", NewText(string(protocol.StringifyValidatedJavaScriptJSON(card)))))), feishuField("sequence", sequence))))
	})
	if e != nil {
		return e
	}
	return feishuCardOK(response, "card.update", fmt.Sprintf("seq=%d cardId=%s", sequence, cardID.String()))
}
