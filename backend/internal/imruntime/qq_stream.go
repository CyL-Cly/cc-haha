package imruntime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type QQStream struct {
	client                       *QQClient
	ctx                          context.Context
	target                       QQTarget
	throttle                     time.Duration
	mu                           sync.Mutex
	messageID, lastSent, pending Text
	index                        int
	sequence                     *int32
	lastFlush                    int64
	timerStop                    func()
	inFlight                     chan struct{}
	completed                    bool
}

func (c *QQClient) OpenStream(ctx context.Context, target QQTarget, throttle time.Duration) (*QQStream, error) {
	if target.Scope != "c2c" {
		return nil, errors.New("Streaming is only supported for C2C targets")
	}
	if len(target.MessageID) == 0 {
		return nil, errors.New("Streaming requires target.msgId from the inbound message")
	}
	if throttle == 0 {
		throttle = 500 * time.Millisecond
	}
	return &QQStream{client: c, ctx: ctx, target: QQTarget{target.Scope, target.TargetID.Clone(), target.MessageID.Clone()}, throttle: max(throttle, 300*time.Millisecond)}, nil
}
func (s *QQStream) Update(text Text) error {
	s.mu.Lock()
	if s.completed {
		s.mu.Unlock()
		return nil
	}
	s.pending = text.Clone()
	elapsed := s.client.o.Now().UnixMilli() - s.lastFlush
	if s.inFlight != nil {
		s.mu.Unlock()
		return nil
	}
	if elapsed >= s.throttle.Milliseconds() {
		s.mu.Unlock()
		return s.flush(1)
	}
	if s.timerStop == nil {
		s.timerStop = s.client.o.Schedule(s.throttle-time.Duration(elapsed)*time.Millisecond, func() {
			s.mu.Lock()
			s.timerStop = nil
			completed := s.completed
			s.mu.Unlock()
			if !completed {
				if e := s.flush(1); e != nil {
					s.client.o.Log("[qqbot:stream] throttle flush error: " + e.Error())
				}
			}
		})
	}
	s.mu.Unlock()
	return nil
}
func (s *QQStream) Complete() error {
	s.mu.Lock()
	if s.completed {
		s.mu.Unlock()
		return nil
	}
	s.completed = true
	if s.timerStop != nil {
		s.timerStop()
		s.timerStop = nil
	}
	inFlight := s.inFlight
	s.mu.Unlock()
	if inFlight != nil {
		select {
		case <-inFlight:
		case <-s.ctx.Done():
			return context.Cause(s.ctx)
		}
	}
	return s.flush(10)
}
func (s *QQStream) Cancel() {
	s.mu.Lock()
	s.completed = true
	if s.timerStop != nil {
		s.timerStop()
		s.timerStop = nil
	}
	s.mu.Unlock()
}
func (s *QQStream) flush(state int) error {
	s.mu.Lock()
	if s.inFlight != nil {
		s.mu.Unlock()
		return nil
	}
	if qqRawKey(s.pending) == qqRawKey(s.lastSent) && state != 10 {
		s.mu.Unlock()
		return nil
	}
	done := make(chan struct{})
	s.inFlight = done
	text := s.pending.Clone()
	if s.sequence == nil {
		n := s.client.nextSequence()
		s.sequence = &n
	}
	index := s.index
	s.index++
	request := qqObject(qqField("input_mode", "replace"), qqField("input_state", state), qqField("content_type", "markdown"), qqField("content_raw", text), qqField("event_id", s.target.MessageID), qqField("msg_id", s.target.MessageID), qqField("msg_seq", *s.sequence), qqField("index", index))
	if len(s.messageID) > 0 {
		request.Set("stream_msg_id", raw(s.messageID))
	}
	s.mu.Unlock()
	var response protocol.JSONObject
	var e error
	for attempt := 0; attempt <= 3; attempt++ {
		result, err := s.client.SendStream(s.ctx, s.target, request)
		e = err
		if e == nil {
			response = object(result)
			break
		}
		// SDK's actual ApiError stores httpStatus/bizCode, whereas the stream
		// classifier reads code/err_code. Do not invent a429/50002 retry here.
		if !strings.Contains(e.Error(), "rate limit") || attempt == 3 {
			break
		}
		if e = s.client.sleep(s.ctx, time.Duration(1<<attempt)*time.Second); e != nil {
			break
		}
		s.mu.Lock()
		request.Set("index", raw(s.index))
		s.index++
		s.mu.Unlock()
	}
	s.mu.Lock()
	if e == nil {
		if truthy(response.Get("id")) && len(s.messageID) == 0 {
			s.messageID = TextJSON(response.Get("id"))
		}
		s.lastSent = text
		s.lastFlush = s.client.o.Now().UnixMilli()
	}
	s.inFlight = nil
	trailing := e == nil && !s.completed && qqRawKey(s.pending) != qqRawKey(s.lastSent) && s.timerStop == nil && state != 10
	close(done)
	s.mu.Unlock()
	if e != nil {
		s.client.o.Log("[qqbot:stream] flush failed: " + e.Error())
		return e
	}
	if trailing {
		return s.flush(1)
	}
	return nil
}

// QQResponse is the original adapter response, distinct from SDK sequencing.
// Its streamed offset advances after update returns, including a throttled
// update that has not reached the wire yet; that Source behavior is retained.
type QQResponse struct {
	mu          sync.Mutex
	session     *QQStream
	accumulated Text
	streamed    int
	failed      bool
	fallback    func(Text) error
	log         func(string)
}

func NewQQResponse(ctx context.Context, client *QQClient, target QQTarget, fallback func(Text) error, log func(string)) *QQResponse {
	if log == nil {
		log = func(string) {}
	}
	r := &QQResponse{fallback: fallback, log: log}
	if target.Scope == "c2c" && len(target.MessageID) > 0 {
		session, e := client.OpenStream(ctx, target, QQFlushInterval)
		if e == nil {
			r.session = session
		} else {
			log("[QQ] openStream failed: " + e.Error())
		}
	}
	return r
}
func (r *QQResponse) Append(delta Text) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(delta) == 0 {
		return nil
	}
	r.accumulated = r.accumulated.Join(delta)
	if r.session == nil || r.failed {
		return nil
	}
	if e := r.session.Update(r.accumulated); e != nil {
		r.failed = true
		r.log("[QQ] stream update failed: " + e.Error())
	} else {
		r.streamed = len(r.accumulated)
	}
	return nil
}
func (r *QQResponse) Finish() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	session := r.session
	r.session = nil
	if session != nil && !r.failed {
		if e := session.Complete(); e == nil {
			r.accumulated = Text{}
			return nil
		} else {
			r.log("[QQ] stream complete failed: " + e.Error())
		}
	}
	if session != nil {
		session.Cancel()
	}
	remainder := r.accumulated.Slice(r.streamed, len(r.accumulated)).Trim()
	r.accumulated = Text{}
	if len(remainder) > 0 {
		return r.fallback(remainder)
	}
	return nil
}

type QQPortOptions struct {
	Context   context.Context
	Client    *QQClient
	TargetFor func(string) QQTarget
	ClearChat func(string)
	Log       func(string)
}

func NewQQChatPort(o QQPortOptions) ChatPort {
	if o.Log == nil {
		o.Log = func(string) {}
	}
	return ChatPort{Platform: "qq", LogPrefix: "[QQ]", SendNotice: func(id, text string) error { return o.Client.SendNotice(o.Context, o.TargetFor(id), NewText(text)) }, CreateResponse: func(id string) ResponseStream {
		return NewQQResponse(o.Context, o.Client, o.TargetFor(id), func(t Text) error { return o.Client.SendNotice(o.Context, o.TargetFor(id), t) }, o.Log)
	}, SendImage: func(id string, image OutboundImage) error {
		return o.Client.SendImage(o.Context, o.TargetFor(id), image)
	}, SetBusy: func(id string, busy bool) {
		if busy {
			target := o.TargetFor(id)
			if target.Scope == "c2c" {
				go func() { _, _ = o.Client.SendTyping(o.Context, target) }()
			}
		}
	}, ClearChat: o.ClearChat}
}
