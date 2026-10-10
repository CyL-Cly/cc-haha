package imruntime

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"sync"
	"testing"
	"time"
)

func TestDingTalkOriginalIndexSynchronousPrefixBeforeHeldMedia(t *testing.T) {
	frames := []protocol.JSONObject{}
	for _, s := range []string{`{"type":"CALLBACK","headers":{"topic":"/v1.0/im/bot/messages/get","messageId":"one"},"data":{"msgId":"one","conversationType":"1","senderId":"U","sessionWebhook":"webhook-one","msgtype":"picture","content":{"downloadCode":"held"},"text":{"content":"first"}}}`, `{"type":"CALLBACK","headers":{"topic":"/v1.0/im/bot/messages/get","messageId":"two"},"data":{"msgId":"two","conversationType":"1","senderId":"U","sessionWebhook":"webhook-two","text":{"content":"second"}}}`, `{"type":"CALLBACK","headers":{"topic":"/v1.0/card/instances/callback","messageId":"card-one"},"data":{"requestId":"r","action":"allow"}}`} {
		frames = append(frames, object([]byte(s)))
	}
	input := map[string]any{"frames": frames}
	want := runDingTalkSource(t, "dingtalk-admission.fixture.ts", input)
	again := runDingTalkSource(t, "dingtalk-admission.fixture.ts", input)
	exactJSON(t, raw(again), raw(want), "Source admission replay")
	// Source admission is observed in this test's consumer boundary. Parent
	// integration tests bind the actual platform runtime producer to this seam.
	api, _ := NewDingTalkAPI(context.Background(), DingTalkAPIOptions{Credentials: object(raw(map[string]any{"clientId": "id", "clientSecret": "secret"}))})
	defer api.Close()
	stream, _ := NewDingTalkStream(context.Background(), DingTalkStreamOptions{API: api})
	dedup := NewMessageDedup(context.Background())
	defer dedup.Close()
	dispatch := &DingTalkDispatcher{Stream: stream, Dedup: dedup}
	var mu sync.Mutex
	order := []string{}
	webhook := ""
	targets := []any{}
	decisions := []protocol.JSONObject{}
	pending := true
	gate, firstDone, secondDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	dispatch.RobotAdmission = func(_ context.Context, data json.RawMessage) (func(context.Context) error, error) {
		o := object(data)
		webhook = o.String("sessionWebhook")
		targets = []any{[]any{"dingtalk:dm:U", map[string]any{"type": "user", "userId": "U"}}}
		name := ExtractDingTalkText(data).String()
		media := len(ExtractDingTalkAttachments(data)) > 0
		return func(ctx context.Context) error {
			if media {
				mu.Lock()
				order = append(order, "collect:held")
				mu.Unlock()
				select {
				case <-gate:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			mu.Lock()
			order = append(order, "route:"+name)
			mu.Unlock()
			if media {
				close(firstDone)
			} else {
				close(secondDone)
			}
			return nil
		}, nil
	}
	dispatch.CardAdmission = func(_ context.Context, data json.RawMessage) (func(context.Context) error, error) {
		decisions = append(decisions, *ParseDingTalkPermissionCardAction(data))
		pending = false
		return nil, nil
	}
	stream.o.OnRobot = dispatch.HandleRobot
	stream.o.OnCard = dispatch.HandleCard
	if e := stream.handle(frames[0]); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		started := len(order) > 0
		mu.Unlock()
		if started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("held collector not admitted")
		}
		time.Sleep(time.Millisecond)
	}
	for _, frame := range frames[1:] {
		if e := stream.handle(frame); e != nil {
			t.Fatal(e)
		}
	}
	if pending || webhook != "webhook-two" || len(decisions) != 1 {
		t.Fatal("callback prefixes deferred past dispatch return")
	}
	select {
	case <-secondDone:
	case <-time.After(3 * time.Second):
		t.Fatal("later plain input serialized behind held media")
	}
	mu.Lock()
	before := append([]string{}, order...)
	mu.Unlock()
	close(gate)
	<-firstDone
	stream.Close()
	mu.Lock()
	final := append([]string{}, order...)
	mu.Unlock()
	prefix := protocol.NewJSONObject(dingTalkField("webhooks", []any{[]any{"dingtalk:dm:U", webhook}}), dingTalkField("targets", targets), dingTalkField("pending", []any{}), dingTalkField("decisions", decisions))
	exactJSON(t, raw(prefix), want.Get("prefix"), "synchronous before-await producer")
	exactJSON(t, raw(before), want.Get("beforeRelease"), "plain route precedes held media")
	exactJSON(t, raw(final), want.Get("order"), "await completion order")
}
