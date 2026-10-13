package tracecapture

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestTraceNearLargeFileTimeLookupAndExactLocatorOutsideOverview(t *testing.T) {
	s, e := New(Options{Scope: t.TempDir(), ResolveMode: func() string { return "off" }})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	os.MkdirAll(s.storageDir(), 0700)
	f, e := os.Create(s.filePath("s"))
	if e != nil {
		t.Fatal(e)
	}
	base := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	padding := strings.Repeat("x", 256*1024)
	for n := 0; n < 270; n++ {
		stamp := iso(base.Add(time.Duration(n) * time.Minute))
		_, e = fmt.Fprintf(f, `{"type":"call","record":{"id":"call%d","sessionId":"s","source":"anthropic","startedAt":"%s","completedAt":"%s","request":{"method":"POST","url":"http://fixture.invalid","headers":{},"body":{"preview":"%s","bytes":262144}}}}`+"\n", n, stamp, stamp, padding)
		if e != nil {
			t.Fatal(e)
		}
	}
	f.Close()
	stat, _ := os.Stat(s.filePath("s"))
	if stat.Size() <= WindowBytesLimit {
		t.Fatal("fixture does not exceed overview window")
	}
	from := base.Add(260 * time.Minute)
	near, e := s.Near(context.Background(), "s", from, from.Add(3*time.Minute))
	if e != nil {
		t.Fatal(e)
	}
	var calls []protocol.JSONObject
	if e = json.Unmarshal(near.Get("calls"), &calls); e != nil {
		t.Fatal(e)
	}
	if len(calls) != 4 || text(calls[0].Get("id")) != "call260" || text(calls[3].Get("id")) != "call263" || string(near.Get("captured")) != "true" {
		t.Fatalf("time lookup failed: %s", raw(near))
	}
	locs := object(near.Get("locs"))
	parts := strings.Split(text(locs.Get("call262")), "-")
	if len(parts) != 2 {
		t.Fatal(locs)
	}
	var start, end int64
	fmt.Sscan(parts[0], &start)
	fmt.Sscan(parts[1], &end)
	if start <= WindowBytesLimit {
		t.Fatal("locator not past overview")
	}
	call, e := s.CallAt(context.Background(), "s", "call262", &[2]int64{start, end})
	if e != nil || text(call.Get("id")) != "call262" || len(text(object(object(call.Get("request")).Get("body")).Get("preview"))) != len(padding) {
		t.Fatalf("located body failed: %v %s", e, raw(call))
	}
	bad, e := s.CallAt(context.Background(), "s", "missing", &[2]int64{start, end})
	if e != nil || bad.IsObject() {
		t.Fatal("locator granted a different call", e)
	}
}
func TestTraceNearIndexedRangeDedupLimitMissingAndCancel(t *testing.T) {
	for _, mode := range []string{"off", "on"} {
		t.Run(mode, func(t *testing.T) {
			s, e := New(Options{Scope: t.TempDir(), ResolveMode: func() string { return mode }})
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close(context.Background())
			from := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
			missing, e := s.Near(context.Background(), "s", from, from)
			if e != nil || string(missing.Get("captured")) != "false" {
				t.Fatal(missing, e)
			}
			os.MkdirAll(s.storageDir(), 0700)
			f, _ := os.Create(s.filePath("s"))
			for n := 0; n < 55; n++ {
				stamp := iso(from.Add(time.Duration(n) * time.Second))
				fmt.Fprintf(f, `{"id":"c%d","sessionId":"s","source":"anthropic","startedAt":"%s","request":{"body":{"bytes":0}}}`+"\n", n, stamp)
			}
			f.Close()
			near, e := s.Near(context.Background(), "s", from, from.Add(time.Minute))
			if e != nil {
				t.Fatal(e)
			}
			var calls []protocol.JSONObject
			json.Unmarshal(near.Get("calls"), &calls)
			if len(calls) != 50 || string(near.Get("limited")) != "true" || text(calls[0].Get("id")) != "c0" {
				t.Fatalf("near cap: %s", raw(near))
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, e = s.Near(ctx, "s", from, from); e == nil {
				t.Fatal("cancel ignored")
			}
			for _, to := range []time.Time{from.Add(-time.Second), from.Add(time.Hour + time.Second)} {
				if _, e = s.Near(context.Background(), "s", from, to); e == nil {
					t.Fatal("invalid window accepted")
				}
			}
		})
	}
}
