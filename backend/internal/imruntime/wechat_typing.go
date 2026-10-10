package imruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

type WechatTypingOptions struct {
	Log      func(string)
	Schedule func(time.Duration, func()) func()
}
type WechatTypingController struct {
	api      *WechatAPI
	contexts *WechatContexts
	o        WechatTypingOptions
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	timers   map[string]func()
	tickets  map[string]json.RawMessage
	wg       sync.WaitGroup
	closed   bool
	once     sync.Once
}

func NewWechatTypingController(ctx context.Context, api *WechatAPI, contexts *WechatContexts, o WechatTypingOptions) *WechatTypingController {
	if o.Log == nil {
		o.Log = func(string) {}
	}
	if o.Schedule == nil {
		o.Schedule = func(d time.Duration, f func()) func() {
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				defer close(done)
				timer := time.NewTicker(d)
				defer timer.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-timer.C:
						f()
					}
				}
			}()
			return func() { cancel(); <-done }
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	return &WechatTypingController{api: api, contexts: contexts, o: o, ctx: ctx, cancel: cancel, timers: map[string]func(){}, tickets: map[string]json.RawMessage{}}
}
func (t *WechatTypingController) launch(chat, status string) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.wg.Add(1)
	t.mu.Unlock()
	go func() { defer t.wg.Done(); t.SendIndicator(t.ctx, chat, status) }()
}
func (t *WechatTypingController) Start(chat string) {
	t.launch(chat, "typing")
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.timers[chat] != nil {
		return
	}
	t.timers[chat] = t.o.Schedule(5*time.Second, func() { t.launch(chat, "typing") })
}
func (t *WechatTypingController) Stop(chat string) {
	t.mu.Lock()
	stop := t.timers[chat]
	delete(t.timers, chat)
	t.mu.Unlock()
	if stop != nil {
		stop()
	}
	t.launch(chat, "cancel")
}

// Destroy is the original timer-only teardown; it does not clear the ticket
// cache or suppress already submitted sends. Close additionally joins owned I/O.
func (t *WechatTypingController) Destroy() {
	t.mu.Lock()
	stops := t.timers
	t.timers = map[string]func(){}
	t.mu.Unlock()
	for _, stop := range stops {
		stop()
	}
}
func (t *WechatTypingController) Close() {
	t.once.Do(func() {
		t.mu.Lock()
		t.closed = true
		t.cancel()
		t.mu.Unlock()
		t.Destroy()
		t.wg.Wait()
	})
}
func (t *WechatTypingController) Ticket(chat string) json.RawMessage {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append(json.RawMessage(nil), t.tickets[chat]...)
}
func (t *WechatTypingController) getTicket(ctx context.Context, chat, status string) (json.RawMessage, error) {
	ticket := t.Ticket(chat)
	if !wechatTruthy(ticket) && status == "typing" {
		var contextToken json.RawMessage
		if t.contexts != nil {
			contextToken = t.contexts.Get(chat)
		}
		body, e := t.api.GetConfig(ctx, raw(chat), contextToken)
		if e != nil {
			return nil, e
		}
		if string(body) == "null" {
			return nil, fmt.Errorf("null is not an object (evaluating 'configResp.ret')")
		}
		ret := wechatProperty(body, "ret")
		if n, ok := wechatNumeric(ret); ok && n != 0 {
			message := wechatProperty(body, "errmsg")
			if len(message) == 0 || string(message) == "null" {
				message = raw("")
			}
			text, e := wechatString(message)
			if e != nil {
				return nil, e
			}
			retText, e := wechatString(ret)
			if e != nil {
				return nil, e
			}
			return nil, fmt.Errorf("getconfig returned %s: %s", retText.String(), text.String())
		}
		ticket = wechatProperty(body, "typing_ticket")
		if wechatTruthy(ticket) {
			t.mu.Lock()
			t.tickets[chat] = append(json.RawMessage(nil), ticket...)
			t.mu.Unlock()
		}
	}
	if !wechatTruthy(ticket) {
		return nil, nil
	}
	return ticket, nil
}
func (t *WechatTypingController) send(ctx context.Context, chat, status string) error {
	ticket, e := t.getTicket(ctx, chat, status)
	if e != nil || !wechatTruthy(ticket) {
		return e
	}
	return t.api.SendTyping(ctx, raw(chat), ticket, status)
}
func (t *WechatTypingController) SendIndicator(ctx context.Context, chat, status string) {
	e := t.send(ctx, chat, status)
	if e == nil {
		return
	}
	t.mu.Lock()
	delete(t.tickets, chat)
	t.mu.Unlock()
	if status == "typing" {
		if t.send(ctx, chat, status) == nil {
			return
		}
	}
	t.o.Log("[WeChat] sendTyping failed: " + e.Error())
}
