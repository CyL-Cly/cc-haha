package streamhistory

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestGeneratorThrowKeepsCommitBufferOrdinaryRejectionCloses(t *testing.T) {
	for _, abrupt := range []bool{true, false} {
		c, _ := New(Options{UUID: func() string { return "original" }, Now: func() time.Time { return time.UnixMilli(1700000000000) }})
		ordinary := errors.New("original throw")
		failure := error(ordinary)
		if abrupt {
			failure = &YieldThrow{Original: ordinary}
		}
		event := json.RawMessage(`{"type":"message_start","message":{"id":"original","role":"assistant","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`)
		e := c.Feed(event, Timing{}, func(Update) error { return failure })
		if !errors.Is(e, ordinary) {
			t.Fatal("original error identity lost", e)
		}
		c.pending = []*Envelope{{Content: []*Block{{}}}}
		count := 0
		e = c.FlushProducerError(func(Update) error { count++; return nil })
		if abrupt {
			if e != nil || count != 1 || len(c.pending) != 0 {
				t.Fatalf("catch drain %d %v", count, e)
			}
			if e = c.FlushProducerError(nil); e == nil {
				t.Fatal("nil emitter")
			}
		} else if !errors.Is(e, ErrClosed) {
			t.Fatal("ordinary callback rejection reopened")
		}
	}
	c, _ := New(Options{UUID: func() string { return "id" }, Now: time.Now})
	cause := errors.New("original")
	c.pending = []*Envelope{{Content: []*Block{{}}}}
	if e := c.FlushProducerError(func(Update) error { return &YieldThrow{Original: cause} }); !errors.Is(e, cause) || c.closed {
		t.Fatal("yield throw closed commit catch", e)
	}
	if isYieldThrow(&YieldThrow{}) {
		t.Fatal("nil thrown value treated as actual error")
	}
}
