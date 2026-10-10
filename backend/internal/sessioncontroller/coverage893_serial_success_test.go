package sessioncontroller

import (
	"context"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
	"os"
	"strings"
	"testing"
)

// This is the existing legacy component caller; the explicit Flush is a disk
// observation here, and is not evidence of public natural timer/exit behavior.
func TestCoverage893LegacySerialRecordKeepsLiveBlockUntilExplicitFlush(t *testing.T) {
	c := activeController(t)
	o := cooperative.New()
	defer o.Close()
	a, e := NewSerialASK(c, o)
	if e != nil {
		t.Fatal(e)
	}
	e = o.Run(context.Background(), func(s *cooperative.Scope) error {
		if _, e := a.Begin(s, parsedInput("component-first", "question")); e != nil {
			return e
		}
		producer, e := streamhistory.New(streamhistory.Options{UUID: func() string { return "component-assistant" }, Now: c.options.Now})
		if e != nil {
			return e
		}
		var envelope *streamhistory.Envelope
		for _, frame := range []string{
			`{"type":"message_start","message":{"id":"actual-original","type":"message","role":"assistant","model":"fixture-model","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"before"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`,
			`{"type":"message_stop"}`,
		} {
			if e := producer.Feed([]byte(frame), streamhistory.Timing{}, func(u streamhistory.Update) error {
				if u.Envelope != nil {
					envelope = u.Envelope
				}
				return nil
			}); e != nil {
				return e
			}
		}
		if e := producer.Finish(nil, func(u streamhistory.Update) error {
			if u.Envelope != nil {
				envelope = u.Envelope
			}
			return nil
		}); e != nil {
			return e
		}
		if envelope == nil {
			return fmt.Errorf("actual complete response did not yield assistant")
		}
		envelope.Record.Set("future", []byte(`{"keep":true}`))
		if e := a.Accept(s, queryloop.Message{Assistant: envelope}); e != nil {
			return e
		}
		if e := a.Record(s, a.History()); e != nil {
			return e
		}
		envelope.Content[0].Object.Set("text", quote("actual shared block after Record"))
		raw, e := a.Raw(requestcontext.Context{})
		if e != nil {
			return e
		}
		projected, e := a.Resolve(context.Background(), raw, raw.Context.History)
		if e != nil {
			return e
		}
		if len(projected.History) != 3 || string(projected.History[0].Get("isMeta")) != "true" || projected.History[2].String("uuid") != "component-assistant" {
			return fmt.Errorf("real next request history lost: %v", projected.History)
		}
		if e := c.writer.Flush(); e != nil {
			return e
		}
		data, e := os.ReadFile(c.writer.Snapshot().String("path"))
		if e != nil {
			return e
		}
		if !strings.Contains(string(data), "actual shared block after Record") || !strings.Contains(string(data), `"future":{"keep":true}`) {
			return fmt.Errorf("native component disk severed retained message: %s", data)
		}
		cache := a.cache
		if e := a.Finally(s); e != nil {
			return e
		}
		if c.readCache != cache || a.running {
			return fmt.Errorf("finally lost component cache")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
func TestCoverage893ResumeAtMessageUsesActualLoadedHistoryPrefix(t *testing.T) {
	c, _, o := publicController(t)
	e := o.Run(context.Background(), func(s *cooperative.Scope) error {
		input := activation()
		input.Resume.Source = []byte(`{"sessionId":"11111111-1111-4111-8111-111111111111","messages":[{"type":"user","uuid":"first","timestamp":"2023-11-14T22:13:20.000Z","message":{"role":"user","content":"question"}},{"type":"assistant","uuid":"target","timestamp":"2023-11-14T22:13:20.000Z","message":{"id":"original-model-id","role":"assistant","model":"fixture-model","content":[{"type":"text","text":"answer"}],"usage":{"input_tokens":1,"output_tokens":1}}},{"type":"user","uuid":"later","timestamp":"2023-11-14T22:13:20.000Z","message":{"role":"user","content":"later input"}}]}`)
		input.ResumeAt = "target"
		f, e := c.BeginActivate(s, input)
		if e != nil {
			return e
		}
		if _, e = f.Await(s); e != nil {
			return e
		}
		h := c.History()
		if len(h) != 2 || h[0].String("uuid") != "first" || h[1].String("uuid") != "target" {
			return fmt.Errorf("resume-at sliced another history: %v", h)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
