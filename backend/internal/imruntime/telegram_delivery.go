package imruntime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

type TelegramDeliveryAPI interface {
	SendMessage(context.Context, string, Text) (int64, error)
	EditMessage(context.Context, string, int64, Text) error
}
type TelegramDeliveryOptions struct {
	Interval               time.Duration
	Threshold, MaxAttempts int
	Sleep                  func(context.Context, time.Duration) error
	Warn                   func(string)
}
type deliveryState struct {
	mu          sync.Mutex
	full        Text
	immutable   int
	placeholder *int64
	delivered   Text
	plan        *finalPlan
	buffer      *MessageBuffer
}
type finalPlan struct {
	chunks []Text
	mode   string
	next   int
}
type DeliverySnapshot struct {
	Desired     Text `json:"desiredText"`
	Immutable   int  `json:"immutableOffset"`
	Delivered   Text `json:"placeholderDeliveredText"`
	FinalChunks int  `json:"finalChunksDelivered"`
}
type TelegramDelivery struct {
	mu      sync.Mutex
	states  map[string]*deliveryState
	api     TelegramDeliveryAPI
	options TelegramDeliveryOptions
	ctx     context.Context
	closed  bool
	retired []*MessageBuffer
}

func NewTelegramDelivery(ctx context.Context, api TelegramDeliveryAPI, options TelegramDeliveryOptions) *TelegramDelivery {
	if options.MaxAttempts < 1 {
		options.MaxAttempts = 3
	}
	if options.Sleep == nil {
		options.Sleep = func(ctx context.Context, delay time.Duration) error {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		}
	}
	if options.Warn == nil {
		options.Warn = func(string) {}
	}
	return &TelegramDelivery{states: map[string]*deliveryState{}, api: api, options: options, ctx: ctx}
}
func (d *TelegramDelivery) state(chat string) *deliveryState {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.states[chat]
}
func (d *TelegramDelivery) HasState(chat string) bool { return d.state(chat) != nil }
func (d *TelegramDelivery) Snapshot(chat string) *DeliverySnapshot {
	state := d.state(chat)
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	n := 0
	if state.plan != nil {
		n = state.plan.next
	}
	return &DeliverySnapshot{state.full.Clone(), state.immutable, state.delivered.Clone(), n}
}
func (d *TelegramDelivery) Placeholder(chat string) *int64 {
	state := d.state(chat)
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.placeholder == nil {
		return nil
	}
	id := *state.placeholder
	return &id
}
func (d *TelegramDelivery) EnsurePlaceholder(chat string, text Text) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return 0, context.Canceled
	}
	if state := d.states[chat]; state != nil {
		state.mu.Lock()
		if state.placeholder != nil {
			id := *state.placeholder
			state.mu.Unlock()
			return id, nil
		}
		state.mu.Unlock()
	}
	id, err := d.api.SendMessage(d.ctx, chat, text)
	if err != nil {
		return 0, err
	}
	state := &deliveryState{full: Text{}, placeholder: &id, delivered: Text{}}
	if previous := d.states[chat]; previous != nil {
		state.buffer = previous.buffer
	} else {
		state.buffer = NewMessageBuffer(func(text Text, complete bool) error {
			state := d.state(chat)
			if state == nil {
				return nil
			}
			state.mu.Lock()
			defer state.mu.Unlock()
			state.full = state.full.Join(text)
			if complete {
				return nil
			}
			if err := d.stream(chat, state); err != nil {
				d.options.Warn("[Telegram] streaming update failed for " + chat + "; retaining buffered text: " + err.Error())
			}
			return nil
		}, d.options.Interval, d.options.Threshold, func(err error) { d.options.Warn("[MessageBuffer] Flush error: " + err.Error()) })
	}
	d.states[chat] = state
	return id, nil
}
func (d *TelegramDelivery) Append(chat string, text Text) error {
	state := d.state(chat)
	if state == nil {
		if _, err := d.EnsurePlaceholder(chat, NewText("▍")); err != nil {
			return err
		}
		state = d.state(chat)
	}
	if state != nil {
		state.buffer.Append(text)
	}
	return nil
}
func (d *TelegramDelivery) Handle(chat, kind, block string, text Text) error {
	switch kind {
	case "content_start":
		if block == "text" {
			_, err := d.EnsurePlaceholder(chat, NewText("▍"))
			return err
		}
	case "content_delta":
		if len(text) > 0 {
			return d.Append(chat, text)
		}
	case "message_complete":
		return d.Complete(chat)
	}
	return nil
}
func (d *TelegramDelivery) Complete(chat string) error {
	state := d.state(chat)
	if state == nil {
		return nil
	}
	state.buffer.Complete()
	state.mu.Lock()
	if len(state.full.Trim()) == 0 {
		state.mu.Unlock()
		d.Clear(chat)
		return nil
	}
	err := d.final(chat, state)
	state.mu.Unlock()
	if err == nil {
		d.Clear(chat)
	}
	return err
}
func (d *TelegramDelivery) Clear(chat string) {
	d.mu.Lock()
	state := d.states[chat]
	delete(d.states, chat)
	if state != nil {
		d.retired = append(d.retired, state.buffer)
	}
	d.mu.Unlock()
	if state != nil {
		state.buffer.Reset()
	}
}
func (d *TelegramDelivery) Close() {
	d.mu.Lock()
	d.closed = true
	states := d.states
	retired := d.retired
	d.retired = nil
	d.states = map[string]*deliveryState{}
	d.mu.Unlock()
	for _, state := range states {
		state.buffer.Close()
	}
	for _, buffer := range retired {
		buffer.Close()
	}
}
func (d *TelegramDelivery) stream(chat string, state *deliveryState) error {
	for {
		remaining := state.full.Slice(state.immutable, len(state.full))
		if len(TelegramOutbound(remaining)) <= 3998 {
			text := TelegramStreaming(remaining)
			if state.placeholder == nil {
				id, err := d.api.SendMessage(d.ctx, chat, text)
				if err != nil {
					return err
				}
				state.placeholder = &id
			} else if err := d.api.EditMessage(d.ctx, chat, *state.placeholder, text); err != nil {
				return err
			}
			state.delivered = remaining
			return nil
		}
		sealed := SplitTelegramStreaming(remaining, 3998)
		text := TelegramOutbound(sealed.Text)
		if state.placeholder == nil {
			if _, err := d.api.SendMessage(d.ctx, chat, text); err != nil {
				return err
			}
		} else if err := d.api.EditMessage(d.ctx, chat, *state.placeholder, text); err != nil {
			return err
		}
		state.immutable += sealed.ConsumedLength
		state.placeholder = nil
		state.delivered = Text{}
	}
}
func (d *TelegramDelivery) final(chat string, state *deliveryState) error {
	if state.plan == nil {
		mode := "edit"
		if state.placeholder == nil {
			mode = "send"
		}
		state.plan = &finalPlan{chunks: SplitMessage(TelegramOutbound(state.full.Slice(state.immutable, len(state.full))), 4000), mode: mode}
	}
	plan := state.plan
	if plan.mode == "edit" && plan.next == 0 {
		err := d.retry(func() error { return d.api.EditMessage(d.ctx, chat, *state.placeholder, plan.chunks[0]) })
		if err == nil {
			state.delivered = plan.chunks[0]
			plan.next = 1
		} else {
			d.options.Warn("[Telegram] final edit failed for " + chat + "; falling back to sendMessage: " + err.Error())
			plan.mode = "send"
		}
	}
	for plan.next < len(plan.chunks) {
		if err := d.retry(func() error { _, err := d.api.SendMessage(d.ctx, chat, plan.chunks[plan.next]); return err }); err != nil {
			return err
		}
		plan.next++
	}
	return nil
}

// TelegramAPIError mirrors grammY's Bot API error fields, including retry_after.
type TelegramAPIError struct {
	Code        int
	Description string
	RetryAfter  *float64
	Raw         any
}

type TelegramTransportError struct {
	Code, Name, Message string
	Raw                 any
}

func (e *TelegramTransportError) Error() string { return e.Message }

func (e *TelegramAPIError) Error() string { return e.Description }
func RetryDelay(err error, attempt int) (time.Duration, bool) {
	var api *TelegramAPIError
	if errors.As(err, &api) && api.Code == 429 {
		seconds := 1.0
		if api.RetryAfter != nil {
			seconds = *api.RetryAfter
		}
		if seconds < 0 {
			seconds = 0
		}
		return time.Duration(seconds * float64(time.Second)), true
	}
	var network net.Error
	var transport *TelegramTransportError
	if errors.As(err, &transport) && (transport.Name == "HttpError" || transport.Name == "TypeError" || strings.HasPrefix(transport.Code, "ECONNRESET") || strings.HasPrefix(transport.Code, "ECONNREFUSED") || strings.HasPrefix(transport.Code, "EPIPE") || strings.HasPrefix(transport.Code, "ETIMEDOUT") || strings.HasPrefix(transport.Code, "EAI_AGAIN") || strings.HasPrefix(transport.Code, "UND_ERR_")) {
		return 250 * time.Millisecond * time.Duration(1<<(attempt-1)), true
	}
	if errors.As(err, &network) || strings.Contains(strings.ToLower(err.Error()), "fetch failed") || strings.Contains(strings.ToLower(err.Error()), "network error") || strings.Contains(strings.ToLower(err.Error()), "socket hang up") {
		return 250 * time.Millisecond * time.Duration(1<<(attempt-1)), true
	}
	return 0, false
}
func (d *TelegramDelivery) retry(operation func() error) error {
	for attempt := 1; ; attempt++ {
		err := operation()
		if err == nil {
			return nil
		}
		delay, retry := RetryDelay(err, attempt)
		if !retry || attempt >= d.options.MaxAttempts {
			return err
		}
		if err = d.options.Sleep(d.ctx, delay); err != nil {
			return fmt.Errorf("Telegram retry: %w", err)
		}
	}
}
