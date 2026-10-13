package sessioncontroller

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"strings"
	"testing"
	"time"
)

func TestFileHistoryLiveSnapshotBeforeLazyDrain(t *testing.T) {
	owner, writer, pending, loader := uuidHarness(t, "", nil, false)
	pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
		writer.path = writer.target()
		snapshot := protocol.NewJSONObject(protocol.JSONField{Name: "messageId", Value: json.RawMessage(`"original"`)}, protocol.JSONField{Name: "future", Value: json.RawMessage(`"before"`)})
		sink, e := NewFileHistorySink(pending, loader)
		if e != nil {
			return e
		}
		record, e := sink.BeginFileHistorySnapshot(s, snapshot.Get("messageId"), &snapshot, false)
		if e != nil {
			return e
		}
		if _, e = record.Await(s); e != nil {
			return e
		}
		snapshot.Set("future", json.RawMessage(`"after"`))
		f, e := pending.Flush(s)
		if e != nil {
			return e
		}
		return f.Await(s)
	})
	b, e := os.ReadFile(writer.target())
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(b), `"future":"after"`) {
		t.Fatalf("lazy snapshot lost original reference: %s", b)
	}
}
func TestFileHistoryOuterTrackingBeforeAppend(t *testing.T) {
	owner, _, pending, loader := uuidHarness(t, "", nil, false)
	pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
		snapshot := protocol.NewJSONObject(protocol.JSONField{Name: "messageId", Value: json.RawMessage(`"original"`)})
		sink, e := NewFileHistorySink(pending, loader)
		if e != nil {
			return e
		}
		if _, e = sink.BeginFileHistorySnapshot(s, snapshot.Get("messageId"), &snapshot, false); e != nil {
			return e
		}
		if pending.tracked == 0 {
			t.Error("direct snapshot Append skipped original insertFileHistorySnapshot trackWrite admission")
		}
		return nil
	})
	_ = pending.WaitRuntimeNative(context.Background())
}

func TestFileHistoryOriginalBindingLifetimeBufferResetAndPolicySkip(t *testing.T) {
	for _, kind := range []string{"buffer-reset", "policy-skip", "invalid", "enabled-no-url", "closed"} {
		t.Run(kind, func(t *testing.T) {
			owner, writer, pending, loader := uuidHarness(t, "", nil, false)
			pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
				sink, e := NewFileHistorySink(pending, loader)
				if e != nil {
					return e
				}
				snapshot := plainMessage("snapshot-id", "original")
				switch kind {
				case "policy-skip":
					writer.options.Environment["CLAUDE_CODE_SKIP_PROMPT_HISTORY"] = "1"
				case "enabled-no-url":
					writer.options.Environment["ENABLE_SESSION_PERSISTENCE"] = "1"
				case "invalid":
					snapshot = nil
				case "closed":
					pending.closing = true
				}
				f, e := sink.BeginFileHistorySnapshot(s, json.RawMessage(`"original"`), snapshot, false)
				if e != nil {
					return e
				}
				_, e = f.Await(s)
				if kind == "invalid" || kind == "closed" {
					if e == nil {
						t.Error("rejected original boundary fulfilled")
					}
				} else if e != nil {
					return e
				}
				if pending.tracked != 0 {
					t.Error("finally leaked outer tracking", pending.tracked)
				}
				if kind == "buffer-reset" || kind == "enabled-no-url" {
					if len(writer.liveFileHistoryEntries) != 1 || len(writer.pending) != 1 {
						t.Error("unmaterialized live reference lost")
					}
					flush, e := pending.Flush(s)
					if e != nil {
						return e
					}
					if e = flush.Await(s); e != nil {
						return e
					}
					if len(writer.liveFileHistoryEntries) != 1 || len(writer.pending) != 1 {
						t.Error("flush cleared unmaterialized buffer")
					}
					writer.Reset()
					if len(writer.liveFileHistoryEntries) != 0 || len(writer.pending) != 0 {
						t.Error("actual reset retained dropped entries")
					}
				} else if len(writer.liveFileHistoryEntries) != 0 {
					t.Error("skip/reject retained reference", len(writer.liveFileHistoryEntries))
				}
				pending.closing = false
				return nil
			})
		})
	}
}
func TestFileHistoryDrainErrorAndMissingTargetReleaseOriginalBinding(t *testing.T) {
	for _, kind := range []string{"write", "missing", "policy-drain", "identity"} {
		t.Run(kind, func(t *testing.T) {
			owner, writer, pending, loader := uuidHarness(t, "", nil, false)
			files := &pendingFaultFiles{}
			writer.options.Files = files
			pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
				writer.path = writer.target()
				writer.options.Schedule = func(_ time.Duration, _ func()) func() { return func() {} }
				sink, e := NewFileHistorySink(pending, loader)
				if e != nil {
					return e
				}
				snapshot := plainMessage("original", "raw")
				f, e := sink.BeginFileHistorySnapshot(s, snapshot.Get("uuid"), snapshot, false)
				if e != nil {
					return e
				}
				if _, e = f.Await(s); e != nil {
					return e
				}
				if len(writer.liveFileHistoryEntries) != 1 {
					t.Error("queued reference absent")
				}
				switch kind {
				case "write":
					files.arm("write")
				case "missing":
					files.arm("missing")
				case "policy-drain":
					writer.options.Environment["CLAUDE_CODE_SKIP_PROMPT_HISTORY"] = "1"
				case "identity":
					if e = os.Remove(writer.path); e != nil {
						return e
					}
					if e = os.WriteFile(writer.path, []byte("replacement"), 0600); e != nil {
						return e
					}
				}
				flush, e := pending.Flush(s)
				if e != nil {
					return e
				}
				e = flush.Await(s)
				if kind == "write" {
					if e == nil {
						t.Error("original physical write rejection absent")
					}
				} else if e != nil {
					return e
				}
				if len(writer.liveFileHistoryEntries) != 0 {
					t.Error("settled/dropped batch retained snapshot", len(writer.liveFileHistoryEntries))
				}
				return nil
			})
		})
	}
}
func TestFileHistorySnapshotSinkRejectsForeignOwnerAndMissingConstructor(t *testing.T) {
	owner, _, pending, loader := uuidHarness(t, "", nil, false)
	if _, e := NewFileHistorySink(nil, loader); e == nil {
		t.Fatal("missing pending admitted")
	}
	if _, e := NewFileHistorySink(pending, nil); e == nil {
		t.Fatal("missing real loader admitted")
	}
	if (*PendingWriter)(nil).NativeCopyBinding() != nil || (*FileHistorySink)(nil).NativeCopyBinding() != nil {
		t.Fatal("fabricated binding")
	}
	sink, e := NewFileHistorySink(pending, loader)
	if e != nil {
		t.Fatal(e)
	}
	other := cooperative.New()
	defer other.Close()
	if e = other.Run(context.Background(), func(s *cooperative.Scope) error { _, e := sink.BeginFileHistorySnapshot(s, nil, nil, false); return e }); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	_ = owner
}
