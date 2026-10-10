package sessionactivity

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/runtimecleanup"
)

type activityHarness struct {
	owner               *cooperative.Owner
	registry            *runtimecleanup.Registry
	tracker             *Tracker
	process             *diagnostics.Process
	now                 float64
	nowCalls, dateCalls int
	nowFailure          error
	date                time.Time
	enabled             bool
	stringifyFailure    error
	entries             []string
}

func newActivityHarness(t *testing.T, signalEnvironment ...*string) *activityHarness {
	t.Helper()
	h := &activityHarness{owner: cooperative.New(), now: 1000, enabled: true, date: time.UnixMilli(1700000000123)}
	t.Cleanup(h.owner.Close)
	var err error
	h.registry, err = runtimecleanup.New(h.owner)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(t.TempDir(), "diag")
	h.process, err = diagnostics.NewProcess(diagnostics.Options{
		Environment: func(name string) string {
			if name == "CLAUDE_CODE_REMOTE_SEND_KEEPALIVES" && len(signalEnvironment) != 0 {
				return *signalEnvironment[0]
			}
			if h.enabled {
				return base
			}
			return ""
		},
		Now: func() time.Time { h.dateCalls++; return h.date }, NowMillis: func() int64 { return int64(h.now) },
		ReadNowMillis: func() (float64, error) {
			h.nowCalls++
			if h.nowFailure != nil {
				return 0, h.nowFailure
			}
			return h.now, nil
		},
		Stringify: func(v protocol.JSONObject) ([]byte, error) {
			if h.stringifyFailure != nil {
				return nil, h.stringifyFailure
			}
			raw, err := diagnostics.StringifyEntry(v)
			if err == nil {
				h.entries = append(h.entries, string(raw))
			}
			return raw, err
		},
		PID: 1, Platform: "darwin", Files: diagnostics.OSFileSystem{},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.tracker, err = NewCallbackNull(h.owner, h.registry, h.process)
	if err != nil {
		t.Fatal(err)
	}
	return h
}
func (h *activityHarness) run(t *testing.T, fn func(*cooperative.Scope) error) {
	t.Helper()
	if err := h.owner.Run(context.Background(), fn); err != nil {
		t.Fatal(err)
	}
}
func (h *activityHarness) cleanup(s *cooperative.Scope) error {
	f, e := h.registry.Start(s)
	if e != nil {
		return e
	}
	_, e = f.Await(s)
	return e
}
func dataOf(t *testing.T, entry string) string {
	t.Helper()
	var o protocol.JSONObject
	if err := o.UnmarshalJSON([]byte(entry)); err != nil {
		t.Fatal(err)
	}
	return string(o.Get("data"))
}

func TestActivityCleanupPreservesIndependentRefcountAndReasonMap(t *testing.T) {
	h := newActivityHarness(t)
	h.run(t, func(s *cooperative.Scope) error {
		if err := h.tracker.Stop(s, APICall); err != nil {
			return err
		}
		if err := h.cleanup(s); err != nil {
			return err
		}
		if len(h.entries) != 0 {
			return errors.New("unpaired stop registered cleanup")
		}
		if err := h.tracker.Start(s, ToolExec); err != nil {
			return err
		}
		if err := h.tracker.Stop(s, APICall); err != nil {
			return err
		}
		if err := h.cleanup(s); err != nil {
			return err
		}
		h.now = 5000
		if err := h.tracker.Start(s, APICall); err != nil {
			return err
		}
		h.now = 850
		return h.cleanup(s)
	})
	want := []string{`{"refcount":0,"active":{"tool_exec":1},"oldest_activity_ms":null}`, `{"refcount":1,"active":{"tool_exec":1,"api_call":1},"oldest_activity_ms":-4150}`}
	if len(h.entries) != 2 || h.nowCalls != 3 {
		t.Fatalf("entries=%v clocks=%d", h.entries, h.nowCalls)
	}
	for i, v := range want {
		if got := dataOf(t, h.entries[i]); got != v {
			t.Fatalf("snapshot %d want %s got %s", i, v, got)
		}
	}
}

func TestActivityReasonDeleteReinsertAndLateCleanup(t *testing.T) {
	h := newActivityHarness(t)
	h.run(t, func(s *cooperative.Scope) error {
		for _, r := range []Reason{ToolExec, APICall} {
			if e := h.tracker.Start(s, r); e != nil {
				return e
			}
		}
		if e := h.tracker.Stop(s, ToolExec); e != nil {
			return e
		}
		if e := h.tracker.Start(s, ToolExec); e != nil {
			return e
		}
		h.now = 850
		if e := h.cleanup(s); e != nil {
			return e
		}
		if e := h.tracker.Stop(s, APICall); e != nil {
			return e
		}
		h.now = 5000
		return h.cleanup(s)
	})
	if len(h.entries) != 2 || h.nowCalls != 3 {
		t.Fatalf("entries=%v clocks=%d", h.entries, h.nowCalls)
	}
	if got := dataOf(t, h.entries[0]); got != `{"refcount":2,"active":{"api_call":1,"tool_exec":1},"oldest_activity_ms":-150}` {
		t.Fatal(got)
	}
	if got := dataOf(t, h.entries[1]); got != `{"refcount":1,"active":{"tool_exec":1},"oldest_activity_ms":4000}` {
		t.Fatal(got)
	}
}

func TestActivityOriginalClockFailurePrecedesCleanupRegistration(t *testing.T) {
	h := newActivityHarness(t)
	original := errors.New("original Date.now error")
	h.nowFailure = original
	h.run(t, func(s *cooperative.Scope) error {
		if e := h.tracker.Start(s, APICall); e != original {
			return errors.New("clock error identity lost")
		}
		if h.tracker.refs != 1 || len(h.tracker.reasons) != 1 || h.tracker.oldest != nil || h.tracker.cleanupRegistered {
			return errors.New("first clock exception moved past registration")
		}
		if e := h.cleanup(s); e != nil {
			return e
		}
		if len(h.entries) != 0 {
			return errors.New("failed first start registered callback")
		}
		h.nowFailure = nil
		if e := h.tracker.Start(s, APICall); e != nil {
			return e
		}
		return h.cleanup(s)
	})
	if h.nowCalls != 1 || len(h.entries) != 1 || dataOf(t, h.entries[0]) != `{"refcount":2,"active":{"api_call":2},"oldest_activity_ms":null}` {
		t.Fatalf("clock=%d entries=%v", h.nowCalls, h.entries)
	}
}

func TestActivityDisabledSinkStillReadsConditionalElapsedClock(t *testing.T) {
	h := newActivityHarness(t)
	h.enabled = false
	h.now = 0
	h.run(t, func(s *cooperative.Scope) error {
		if e := h.tracker.Start(s, APICall); e != nil {
			return e
		}
		if e := h.cleanup(s); e != nil {
			return e
		}
		if e := h.tracker.Stop(s, APICall); e != nil {
			return e
		}
		if e := h.tracker.Stop(s, APICall); e != nil {
			return e
		}
		return h.cleanup(s)
	})
	if h.nowCalls != 2 || h.dateCalls != 0 || len(h.entries) != 0 {
		t.Fatalf("disabled now=%d date=%d entries=%v", h.nowCalls, h.dateCalls, h.entries)
	}
}

func TestActivityCleanupAsyncDateSerializeAndClockErrorsAllowLaterCallback(t *testing.T) {
	for _, name := range []string{"date", "serialize", "clock"} {
		t.Run(name, func(t *testing.T) {
			h := newActivityHarness(t)
			original := errors.New("original cleanup body error")
			later := 0
			h.run(t, func(s *cooperative.Scope) error {
				if e := h.tracker.Start(s, APICall); e != nil {
					return e
				}
				switch name {
				case "date":
					h.date = time.UnixMilli(8640000000000001)
				case "serialize":
					h.stringifyFailure = original
				case "clock":
					h.nowFailure = original
				}
				_, e := h.registry.Register(s, runtimecleanup.NewCallback(func(*cooperative.Scope) (*runtimecleanup.Future, error) { later++; return nil, nil }))
				if e != nil {
					return e
				}
				f, e := h.registry.Start(s)
				if e != nil {
					return errors.New("async activity error threw from registry map")
				}
				if later != 1 {
					return errors.New("later cleanup callback did not start inline")
				}
				_, e = f.Await(s)
				if name == "date" {
					var invalid *diagnostics.InvalidDateError
					if !errors.As(e, &invalid) {
						return errors.New("date class changed")
					}
				} else if e != original {
					return errors.New("original async error identity changed")
				}
				return nil
			})
			if later != 1 || len(h.entries) != 0 {
				t.Fatalf("later=%d entries=%v", later, h.entries)
			}
		})
	}
}

func TestActivityChecksSameOwnerAndRegistryBeforeMutation(t *testing.T) {
	h := newActivityHarness(t)
	other := cooperative.New()
	defer other.Close()
	foreign, _ := runtimecleanup.New(other)
	if _, e := NewCallbackNull(other, h.registry, h.process); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if _, e := NewCallbackNull(h.owner, foreign, h.process); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if _, e := NewCallbackNull(h.owner, h.registry, nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if h.tracker.Owns(other) || h.tracker.UsesRegistry(foreign) || h.tracker.UsesDiagnostics(nil) {
		t.Fatal("construction identities collapsed")
	}
	h.run(t, func(s *cooperative.Scope) error {
		if e := h.tracker.Start(s, Reason("unknown")); !errors.Is(e, cooperative.ErrRestricted) {
			return errors.New("unknown reason admitted")
		}
		return nil
	})
	if e := other.Run(context.Background(), func(s *cooperative.Scope) error {
		if e := h.tracker.Start(s, APICall); !errors.Is(e, cooperative.ErrRestricted) {
			return errors.New("foreign start admitted")
		}
		if e := h.tracker.Stop(s, APICall); !errors.Is(e, cooperative.ErrRestricted) {
			return errors.New("foreign stop admitted")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if h.tracker.refs != 0 || h.nowCalls != 0 {
		t.Fatal("admission mutated process state")
	}
	h.run(t, func(s *cooperative.Scope) error {
		if e := h.tracker.Start(s, APICall); e != nil {
			return e
		}
		if e := h.tracker.Start(s, APICall); e != nil {
			return e
		}
		if e := h.tracker.Stop(s, APICall); e != nil {
			return e
		}
		return h.cleanup(s)
	})
	if got := dataOf(t, h.entries[0]); !strings.Contains(got, `"api_call":1`) {
		t.Fatal(got)
	}
	var d map[string]any
	if e := json.Unmarshal([]byte(h.entries[0]), &d); e != nil {
		t.Fatal(e)
	}
}
