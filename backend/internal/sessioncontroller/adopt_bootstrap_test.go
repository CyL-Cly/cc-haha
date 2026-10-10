package sessioncontroller

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestAdoptedWriterUsesActualSwitchedBootstrapSIDAndPhysicalFlush(t *testing.T) {
	for _, async := range []bool{false, true} {
		for _, mode := range []string{"existing", "missing", "delete", "replace"} {
			t.Run(mode+map[bool]string{false: "-sync", true: "-owner-future"}[async], func(t *testing.T) {
				writer := readyWriter(t, OSFiles{})
				owner := cooperative.New()
				defer owner.Close()
				state := bindRecordState(t, owner, writer)
				initial := writer.target()
				sid := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
				expected := filepath.Join(writer.options.ConfigDir, "projects", sanitize(writer.options.CWD), sid+".jsonl")
				pendingCall(t, owner, cooperative.Read, func(scope *cooperative.Scope) error {
					if e := state.SwitchSession(scope, sid, nil); e != nil {
						return e
					}
					writer.Switch(sid, "")
					writer.AdoptSessionFile()
					return nil
				})
				if writer.path != expected || initial == expected {
					t.Fatal("adopt did not capture actual switched process identity", writer.path, expected)
				}
				if mode != "missing" {
					writePrivate(t, expected, "sentinel\n")
				}
				entry := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: json.RawMessage(`"queue-operation"`)}, protocol.JSONField{Name: "operation", Value: json.RawMessage(`"enqueue"`)}, protocol.JSONField{Name: "sessionId", Value: quote(sid)}, protocol.JSONField{Name: "content", Value: json.RawMessage(`"same-source-adopted"`)})
				var pending *PendingWriter
				if async {
					var e error
					pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error { pending, e = NewPendingWriter(writer, owner); return e })
					loader := recordLoader(t, nil)
					future := beginAppend(t, owner, pending, loader, &entry)
					pendingCall(t, owner, cooperative.Read, func(scope *cooperative.Scope) error { return future.Await(scope) })
				} else if e := writer.Append(&entry); e != nil {
					t.Fatal(e)
				}
				switch mode {
				case "delete":
					if e := os.Remove(expected); e != nil {
						t.Fatal(e)
					}
				case "replace":
					if e := os.Rename(expected, expected+".old"); e != nil {
						t.Fatal(e)
					}
					writePrivate(t, expected, "replacement-sentinel\n")
				}
				if async {
					ownedFlush(t, owner, pending)
					if e := pending.Close(context.Background()); e != nil {
						t.Fatal(e)
					}
				} else if e := writer.Flush(); e != nil {
					t.Fatal(e)
				}
				raw, e := os.ReadFile(expected)
				switch mode {
				case "missing", "delete":
					if !errors.Is(e, os.ErrNotExist) {
						t.Fatal("missing adopted file materialized", e)
					}
				case "replace":
					if e != nil || string(raw) != "replacement-sentinel\n" {
						t.Fatal("replacement identity received old append", string(raw), e)
					}
				default:
					if e != nil || !strings.HasPrefix(string(raw), "sentinel\n") || !strings.Contains(string(raw), `"content":"same-source-adopted"`) {
						t.Fatal("adopted entry not physically drained", string(raw), e)
					}
				}
				if _, e := os.Stat(initial); !errors.Is(e, os.ErrNotExist) {
					t.Fatal("stale bootstrap transcript created", e)
				}
			})
		}
	}
}
