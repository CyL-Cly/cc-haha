package imruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"math"
	"regexp"
	"sync"
	"time"
)

func FeishuInitialStreamingCard() json.RawMessage {
	return raw(feishuObject(feishuField("schema", "2.0"), feishuField("config", feishuObject(feishuField("streaming_mode", true), feishuField("update_multi", true))), feishuField("body", feishuObject(feishuField("elements", []any{feishuObject(feishuField("tag", "markdown"), feishuField("content", "☁️ *正在思考中...*"), feishuField("text_align", "left"), feishuField("element_id", FeishuStreamingElementID))})))))
}
func FeishuRenderedCard(text Text) json.RawMessage {
	if len(text) == 0 {
		text = NewText(" ")
	}
	return raw(feishuObject(feishuField("schema", "2.0"), feishuField("config", feishuObject(feishuField("update_multi", true))), feishuField("body", feishuObject(feishuField("elements", []any{feishuObject(feishuField("tag", "markdown"), feishuField("content", text), feishuField("text_align", "left"))})))))
}
func FeishuErrorCard(text Text) json.RawMessage {
	if len(text) == 0 {
		text = NewText("未知错误")
	}
	return raw(feishuObject(feishuField("schema", "2.0"), feishuField("config", feishuObject(feishuField("update_multi", true))), feishuField("header", feishuObject(feishuField("title", feishuObject(feishuField("tag", "plain_text"), feishuField("content", "❌ 出错了"))), feishuField("template", "red"))), feishuField("body", feishuObject(feishuField("elements", []any{feishuObject(feishuField("tag", "markdown"), feishuField("content", text))})))))
}

type FeishuStreamingOptions struct {
	Client          *FeishuClient
	ChatID, ReplyTo Text
	UUID            func() string
	Now             func() time.Time
	Schedule        func(time.Duration, func()) func()
	Log             func(string)
}
type FeishuToolStep struct {
	ID, Name Text
	Status   string
}
type FeishuStreamingSnapshot struct {
	Phase             string
	CardID, MessageID json.RawMessage
	Sequence          int64
	StreamActive      bool
	Text, Reasoning   Text
	Tools             []FeishuToolStep
	Failures          int
}
type FeishuStreamingCard struct {
	o                                                    FeishuStreamingOptions
	ctx                                                  context.Context
	cancel                                               context.CancelFunc
	mu                                                   sync.Mutex
	state                                                FeishuStreamingSnapshot
	last                                                 Text
	messageUUID                                          json.RawMessage
	flushMu                                              sync.Mutex
	ready, completed, flushing, reflush, pending, closed bool
	lastUpdate                                           int64
	flushDone                                            chan struct{}
	cancelPending                                        func()
	timerGeneration                                      uint64
	wg                                                   sync.WaitGroup
}

func NewFeishuStreamingCard(ctx context.Context, o FeishuStreamingOptions) *FeishuStreamingCard {
	if o.UUID == nil {
		o.UUID = uuid.NewString
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = func(string) {}
	}
	if o.Schedule == nil {
		o.Schedule = func(d time.Duration, f func()) func() { t := time.AfterFunc(d, f); return func() { t.Stop() } }
	}
	ctx, cancel := context.WithCancel(ctx)
	return &FeishuStreamingCard{o: o, ctx: ctx, cancel: cancel, state: FeishuStreamingSnapshot{Phase: "idle", Text: Text{}, Reasoning: Text{}, Tools: []FeishuToolStep{}}, messageUUID: raw(o.UUID())}
}
func (c *FeishuStreamingCard) Snapshot() FeishuStreamingSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.state
	s.CardID = append(json.RawMessage(nil), s.CardID...)
	s.MessageID = append(json.RawMessage(nil), s.MessageID...)
	s.Text = s.Text.Clone()
	s.Reasoning = s.Reasoning.Clone()
	s.Tools = append([]FeishuToolStep{}, s.Tools...)
	return s
}
func (c *FeishuStreamingCard) Close() {
	c.flushMu.Lock()
	c.closed = true
	c.completed = true
	c.cancelPendingLocked()
	c.cancel()
	c.flushMu.Unlock()
	c.wg.Wait()
}
func (c *FeishuStreamingCard) cancelPendingLocked() {
	c.timerGeneration++
	if c.cancelPending != nil {
		c.cancelPending()
	}
	c.cancelPending = nil
	c.pending = false
}
func (c *FeishuStreamingCard) scheduleLocked(ms int64) {
	c.timerGeneration++
	generation := c.timerGeneration
	c.pending = true
	c.cancelPending = c.o.Schedule(time.Duration(ms)*time.Millisecond, func() {
		c.flushMu.Lock()
		if c.closed || generation != c.timerGeneration {
			c.flushMu.Unlock()
			return
		}
		c.pending = false
		c.cancelPending = nil
		c.flushMu.Unlock()
		_ = c.Flush(c.ctx)
	})
}
func (c *FeishuStreamingCard) setReady() {
	c.flushMu.Lock()
	c.ready = true
	c.lastUpdate = c.o.Now().UnixMilli()
	c.flushMu.Unlock()
}
func (c *FeishuStreamingCard) throttle() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.StreamActive {
		return 100
	}
	return 1500
}
func (c *FeishuStreamingCard) ThrottledUpdate(ctx context.Context, ms int64) error {
	return c.throttledUpdate(ctx, ms, true)
}
func (c *FeishuStreamingCard) queueThrottle(ctx context.Context, ms int64) {
	_ = c.throttledUpdate(ctx, ms, false)
}
func (c *FeishuStreamingCard) throttledUpdate(ctx context.Context, ms int64, wait bool) error {
	c.flushMu.Lock()
	if !c.ready || c.closed {
		c.flushMu.Unlock()
		return nil
	}
	now := c.o.Now().UnixMilli()
	elapsed := now - c.lastUpdate
	if elapsed >= ms {
		c.cancelPendingLocked()
		if elapsed > 2000 {
			c.lastUpdate = now
			c.scheduleLocked(300)
			c.flushMu.Unlock()
			return nil
		}
		c.flushMu.Unlock()
		return c.flush(ctx, wait)
	}
	if !c.pending {
		c.scheduleLocked(ms - elapsed)
	}
	c.flushMu.Unlock()
	return nil
}
func (c *FeishuStreamingCard) Flush(ctx context.Context) error { return c.flush(ctx, true) }
func (c *FeishuStreamingCard) flush(ctx context.Context, wait bool) error {
	c.flushMu.Lock()
	if !c.ready || c.flushing || c.completed || c.closed {
		if c.flushing && !c.completed {
			c.reflush = true
		}
		c.flushMu.Unlock()
		return nil
	}
	c.flushing = true
	c.reflush = false
	c.lastUpdate = c.o.Now().UnixMilli()
	done := make(chan struct{})
	c.flushDone = done
	c.wg.Add(1)
	c.flushMu.Unlock()
	if wait {
		return c.finishFlush(ctx, done)
	}
	go func() { _ = c.finishFlush(ctx, done) }()
	return nil
}
func (c *FeishuStreamingCard) finishFlush(ctx context.Context, done chan struct{}) error {
	defer c.wg.Done()
	e := c.performFlush(ctx)
	c.flushMu.Lock()
	if e == nil {
		c.lastUpdate = c.o.Now().UnixMilli()
	}
	c.flushing = false
	close(done)
	if c.reflush && !c.completed && !c.pending && !c.closed {
		c.reflush = false
		c.scheduleLocked(0)
	}
	c.flushMu.Unlock()
	return e
}
func (c *FeishuStreamingCard) WaitForFlush(ctx context.Context) error {
	c.flushMu.Lock()
	if !c.flushing {
		c.flushMu.Unlock()
		return nil
	}
	done := c.flushDone
	c.flushMu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (c *FeishuStreamingCard) EnsureCreated(ctx context.Context) error {
	c.mu.Lock()
	if c.state.Phase != "idle" {
		c.mu.Unlock()
		return nil
	}
	c.state.Phase = "creating"
	c.mu.Unlock()
	card, e := c.o.Client.CreateCardEntity(ctx, FeishuInitialStreamingCard())
	var message json.RawMessage
	if e == nil {
		message, e = c.o.Client.SendCardAsMessage(ctx, c.o.ChatID, card, c.o.ReplyTo, c.messageUUID)
	}
	if e == nil {
		c.mu.Lock()
		c.state.CardID = card
		c.state.MessageID = message
		c.state.StreamActive = true
		c.state.Sequence = 1
		c.state.Phase = "streaming"
		c.mu.Unlock()
	} else {
		c.o.Log("[Feishu StreamingCard] CardKit create/send failed, falling back to im.message.patch: " + e.Error())
		response, fallback := c.o.Client.cardRequest(ctx, "im.message.create", func(ctx context.Context) (json.RawMessage, error) {
			return c.o.Client.CreateMessage(ctx, c.o.ChatID, "interactive", NewText(string(FeishuRenderedCard(NewText(" ")))), c.messageUUID)
		})
		if fallback == nil {
			message = object(object(response).Get("data")).Get("message_id")
			if !credentialTruthy(message) {
				fallback = errors.New("fallback im.message.create returned no message_id")
			}
		}
		if fallback != nil {
			c.mu.Lock()
			c.state.Phase = "aborted"
			c.mu.Unlock()
			return fallback
		}
		c.mu.Lock()
		c.state.CardID = nil
		c.state.MessageID = message
		c.state.StreamActive = false
		c.state.Phase = "streaming"
		c.mu.Unlock()
	}
	c.setReady()
	c.mu.Lock()
	has := len(c.state.Text) > 0 || len(c.state.Reasoning) > 0 || len(c.state.Tools) > 0
	c.mu.Unlock()
	if has {
		c.queueThrottle(ctx, c.throttle())
	}
	return nil
}
func (c *FeishuStreamingCard) AppendText(t Text)      { c.append(t, false) }
func (c *FeishuStreamingCard) AppendReasoning(t Text) { c.append(t, true) }
func (c *FeishuStreamingCard) append(t Text, reason bool) {
	if len(t) == 0 {
		return
	}
	c.mu.Lock()
	if c.state.Phase == "completed" || c.state.Phase == "aborted" {
		c.mu.Unlock()
		return
	}
	if reason {
		c.state.Reasoning = c.state.Reasoning.Join(t)
	} else {
		c.state.Text = c.state.Text.Join(t)
	}
	c.mu.Unlock()
	c.queueThrottle(c.ctx, c.throttle())
}
func (c *FeishuStreamingCard) StartTool(id, name Text) {
	c.mu.Lock()
	if c.state.Phase == "completed" || c.state.Phase == "aborted" || len(name) == 0 {
		c.mu.Unlock()
		return
	}
	if len(id) == 0 {
		id = name.Join(NewText(fmt.Sprintf("#%d", len(c.state.Tools))))
	}
	for _, s := range c.state.Tools {
		if string(raw(s.ID)) == string(raw(id)) {
			c.mu.Unlock()
			return
		}
	}
	c.state.Tools = append(c.state.Tools, FeishuToolStep{id.Clone(), name.Clone(), "running"})
	c.mu.Unlock()
	c.queueThrottle(c.ctx, c.throttle())
}
func (c *FeishuStreamingCard) CompleteTool(id, name Text) {
	c.mu.Lock()
	if c.state.Phase == "completed" || c.state.Phase == "aborted" {
		c.mu.Unlock()
		return
	}
	at := -1
	if len(id) > 0 {
		for i, s := range c.state.Tools {
			if string(raw(s.ID)) == string(raw(id)) {
				at = i
				break
			}
		}
	}
	if at < 0 && len(name) > 0 {
		for i := len(c.state.Tools) - 1; i >= 0; i-- {
			if string(raw(c.state.Tools[i].Name)) == string(raw(name)) && c.state.Tools[i].Status == "running" {
				at = i
				break
			}
		}
	}
	if at < 0 || c.state.Tools[at].Status == "done" {
		c.mu.Unlock()
		return
	}
	c.state.Tools[at].Status = "done"
	c.mu.Unlock()
	c.queueThrottle(c.ctx, c.throttle())
}
func feishuRendered(s FeishuStreamingSnapshot) Text {
	sections := []Text{}
	if len(s.Tools) > 0 {
		parts := []Text{}
		for _, step := range s.Tools {
			prefix := "⚙️ "
			if step.Status == "done" {
				prefix = "✅ "
			}
			parts = append(parts, NewText(prefix).Join(step.Name))
		}
		sections = append(sections, NewText("🛠️ ").Join(JoinText(parts, NewText(" · "))))
	}
	if len(s.Reasoning) > 0 {
		preview := s.Reasoning
		if len(preview) > 600 {
			preview = NewText("...").Join(preview.Slice(len(preview)-597, len(preview)))
		}
		sections = append(sections, NewText("💭 **思考中**\n\n").Join(preview))
	}
	if len(s.Text) > 0 {
		sections = append(sections, s.Text)
	}
	if len(sections) == 0 {
		return NewText("☁️ *正在思考中...*")
	}
	return FeishuOptimizeMarkdown(FeishuSanitizeCardText(JoinText(sections, NewText("\n\n---\n\n")), 3), 2)
}
func (c *FeishuStreamingCard) RenderedText() Text { return feishuRendered(c.Snapshot()) }
func (c *FeishuStreamingCard) TerminalText() Text {
	s := c.Snapshot()
	if len(s.Text) > 0 {
		return FeishuOptimizeMarkdown(FeishuSanitizeCardText(s.Text, 3), 2)
	}
	return feishuRendered(s)
}
func feishuCardCode(e error) (float64, Text, bool) {
	var card *FeishuCardKitError
	var httpErr *FeishuHTTPError
	var value json.RawMessage
	msg := Text{}
	if errors.As(e, &card) {
		value = card.Code
		msg = card.Msg
	} else if errors.As(e, &httpErr) {
		value = object(httpErr.Body).Get("code")
		if v := object(httpErr.Body).Get("msg"); isString(v) {
			msg = TextJSON(v)
		} else {
			msg = NewText(e.Error())
		}
	} else {
		return 0, nil, false
	}
	if len(value) == 0 || (!isString(value) && value[0] != '-' && (value[0] < '0' || value[0] > '9')) {
		return 0, nil, false
	}
	n := qqNumber(value)
	return n, msg, !math.IsNaN(n) && !math.IsInf(n, 0)
}
func FeishuCardRateLimited(e error) bool {
	code, _, ok := feishuCardCode(e)
	return ok && code == 230020
}
func FeishuCardTableLimited(e error) bool {
	code, msg, ok := feishuCardCode(e)
	if !ok || code != 230099 {
		return false
	}
	s := msg.String()
	match := regexp.MustCompile(`ErrCode:\s*(\d+)`).FindStringSubmatch(s)
	return len(match) > 1 && qqNumber(raw(match[1])) == 11310 && regexp.MustCompile(`(?i)table number over limit`).MatchString(s)
}
func (c *FeishuStreamingCard) performFlush(ctx context.Context) error {
	s := c.Snapshot()
	if s.Phase != "streaming" || !credentialTruthy(s.MessageID) || credentialTruthy(s.CardID) && !s.StreamActive {
		return nil
	}
	text := feishuRendered(s)
	c.mu.Lock()
	same := string(raw(text)) == string(raw(c.last))
	c.mu.Unlock()
	if same {
		return nil
	}
	if s.StreamActive && credentialTruthy(s.CardID) {
		c.mu.Lock()
		c.state.Sequence++
		seq := c.state.Sequence
		c.mu.Unlock()
		e := c.o.Client.StreamCardContent(ctx, TextJSON(s.CardID), NewText(FeishuStreamingElementID), text, seq)
		if e == nil {
			c.mu.Lock()
			c.last = text
			c.state.Failures = 0
			c.mu.Unlock()
			return nil
		}
		if FeishuCardRateLimited(e) {
			return nil
		}
		c.mu.Lock()
		if FeishuCardTableLimited(e) {
			c.state.StreamActive = false
		} else {
			c.state.Failures++
			if c.state.Failures >= 3 {
				c.state.StreamActive = false
			}
		}
		c.mu.Unlock()
		return nil
	}
	_, e := c.o.Client.cardRequest(ctx, "im.message.patch", func(ctx context.Context) (json.RawMessage, error) {
		return c.o.Client.PatchMessage(ctx, TextJSON(s.MessageID), FeishuRenderedCard(text))
	})
	if e == nil {
		c.mu.Lock()
		c.last = text
		c.mu.Unlock()
	}
	return nil
}
func (c *FeishuStreamingCard) complete() { c.flushMu.Lock(); c.completed = true; c.flushMu.Unlock() }
func (c *FeishuStreamingCard) Finalize(ctx context.Context) error {
	c.mu.Lock()
	if c.state.Phase == "completed" || c.state.Phase == "aborted" {
		c.mu.Unlock()
		return nil
	}
	if c.state.Phase == "idle" {
		c.state.Phase = "completed"
		c.mu.Unlock()
		c.complete()
		return nil
	}
	c.state.Phase = "finalizing"
	c.mu.Unlock()
	c.flushMu.Lock()
	c.cancelPendingLocked()
	c.flushMu.Unlock()
	if e := c.WaitForFlush(ctx); e != nil {
		return e
	}
	text := c.TerminalText()
	s := c.Snapshot()
	var e error
	if credentialTruthy(s.CardID) {
		c.mu.Lock()
		c.state.Sequence++
		seq := c.state.Sequence
		c.mu.Unlock()
		e = c.o.Client.SetCardStreamingMode(ctx, TextJSON(s.CardID), false, seq)
		if e == nil {
			c.mu.Lock()
			c.state.Sequence++
			seq = c.state.Sequence
			c.mu.Unlock()
			e = c.o.Client.UpdateCardKitCard(ctx, TextJSON(s.CardID), FeishuRenderedCard(text), seq)
		}
	} else if credentialTruthy(s.MessageID) {
		_, e = c.o.Client.cardRequest(ctx, "im.message.patch", func(ctx context.Context) (json.RawMessage, error) {
			return c.o.Client.PatchMessage(ctx, TextJSON(s.MessageID), FeishuRenderedCard(text))
		})
	}
	if e != nil && credentialTruthy(s.MessageID) {
		_, _ = c.o.Client.cardRequest(ctx, "im.message.patch", func(ctx context.Context) (json.RawMessage, error) {
			return c.o.Client.PatchMessage(ctx, TextJSON(s.MessageID), FeishuRenderedCard(text))
		})
	}
	c.mu.Lock()
	c.state.Phase = "completed"
	c.last = text
	c.mu.Unlock()
	c.complete()
	return nil
}
func (c *FeishuStreamingCard) Abort(ctx context.Context, cause error) error {
	c.mu.Lock()
	if c.state.Phase == "completed" || c.state.Phase == "aborted" {
		c.mu.Unlock()
		return nil
	}
	idle := c.state.Phase == "idle"
	c.state.Phase = "aborted"
	c.mu.Unlock()
	c.flushMu.Lock()
	c.cancelPendingLocked()
	c.flushMu.Unlock()
	_ = c.WaitForFlush(ctx)
	s := c.Snapshot()
	if idle || !credentialTruthy(s.MessageID) {
		c.complete()
		return nil
	}
	text := NewText(cause.Error())
	if len(s.Text) > 0 {
		text = text.Join(NewText("\n\n——\n\n")).Join(s.Text)
	}
	card := FeishuErrorCard(text)
	if credentialTruthy(s.CardID) {
		c.mu.Lock()
		c.state.Sequence++
		seq := c.state.Sequence
		c.mu.Unlock()
		_ = c.o.Client.SetCardStreamingMode(ctx, TextJSON(s.CardID), false, seq)
		c.mu.Lock()
		c.state.Sequence++
		seq = c.state.Sequence
		c.mu.Unlock()
		_ = c.o.Client.UpdateCardKitCard(ctx, TextJSON(s.CardID), card, seq)
	} else {
		_, _ = c.o.Client.cardRequest(ctx, "im.message.patch", func(ctx context.Context) (json.RawMessage, error) {
			return c.o.Client.PatchMessage(ctx, TextJSON(s.MessageID), card)
		})
	}
	c.complete()
	return nil
}
