package imruntime

import (
	"context"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const WecomStreamByteLimit = 19000
const WecomTextByteLimit = 4000

type WecomReplyClient interface {
	HasPendingReplyAck(protocol.JSONObject) bool
	ReplyStream(context.Context, protocol.JSONObject, Text, Text, bool) (protocol.JSONObject, error)
	SendMessage(context.Context, Text, protocol.JSONObject) (protocol.JSONObject, error)
}

// A response captures the inbound frame once. A later message from the same
// chat must not move an already-open turn into another reply bubble.
type WecomResponse struct {
	mu                 sync.Mutex
	client             WecomReplyClient
	ctx                context.Context
	chat               Text
	frame              *protocol.JSONObject
	streamID           Text
	streamed, overflow Text
	started, closed    bool
	log                func(string)
}

func NewWecomResponse(ctx context.Context, client WecomReplyClient, chat Text, frame *protocol.JSONObject, uuid string, log func(string)) *WecomResponse {
	var captured *protocol.JSONObject
	if frame != nil {
		value := object(raw(*frame))
		captured = &value
	}
	if log == nil {
		log = func(string) {}
	}
	return &WecomResponse{client: client, ctx: ctx, chat: chat.Clone(), frame: captured, streamID: NewText(strings.ReplaceAll(uuid, "-", "")), log: log}
}
func SendWecomMarkdown(ctx context.Context, client WecomReplyClient, chat Text, text Text) error {
	for _, chunk := range SplitWecomMessageByBytes(text, WecomTextByteLimit) {
		_, err := client.SendMessage(ctx, chat, wecomJSONObject(wecomField("msgtype", "markdown"), wecomField("markdown", wecomJSONObject(wecomField("content", chunk)))))
		if err != nil {
			return err
		}
	}
	return nil
}
func (r *WecomResponse) Append(text string) error { return r.AppendText(NewText(text)) }
func (r *WecomResponse) AppendText(delta Text) error {
	r.mu.Lock()
	if len(delta) == 0 {
		r.mu.Unlock()
		return nil
	}
	if r.closed {
		r.overflow = r.overflow.Join(delta)
		r.mu.Unlock()
		return nil
	}
	next := r.streamed.Join(delta)
	if WecomUTF8Length(next) > WecomStreamByteLimit {
		r.mu.Unlock()
		if err := r.flush(true); err != nil {
			return err
		}
		r.mu.Lock()
		r.closed = true
		r.overflow = r.overflow.Join(delta)
		r.mu.Unlock()
		return nil
	}
	r.streamed = next
	frame := r.frame
	r.mu.Unlock()
	if frame != nil && r.client.HasPendingReplyAck(*frame) {
		return nil
	}
	return r.flush(false)
}
func (r *WecomResponse) Finish() error {
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if !closed {
		if err := r.flush(true); err != nil {
			return err
		}
		r.mu.Lock()
		r.closed = true
		r.mu.Unlock()
	}
	r.mu.Lock()
	tail := r.overflow.Trim()
	r.overflow = Text{}
	r.mu.Unlock()
	if len(tail) > 0 {
		return SendWecomMarkdown(r.ctx, r.client, r.chat, tail)
	}
	return nil
}
func (r *WecomResponse) flush(finished bool) error {
	r.mu.Lock()
	text := r.streamed.Clone()
	started := r.started
	frame := r.frame
	r.mu.Unlock()
	if !started && len(text.Trim()) == 0 {
		return nil
	}
	if frame == nil {
		if finished && len(text.Trim()) > 0 {
			return SendWecomMarkdown(r.ctx, r.client, r.chat, text)
		}
		return nil
	}
	_, err := r.client.ReplyStream(r.ctx, *frame, r.streamID, text, finished)
	if err == nil {
		r.mu.Lock()
		r.started = true
		r.mu.Unlock()
		return nil
	}
	r.log("[WeCom] replyStream failed: " + err.Error())
	r.mu.Lock()
	text = r.streamed.Clone()
	r.mu.Unlock()
	if finished && len(text.Trim()) > 0 {
		if err := SendWecomMarkdown(r.ctx, r.client, r.chat, text); err != nil {
			r.log("[WeCom] stream fallback send failed: " + err.Error())
		}
	}
	return nil
}
