package resumestate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestOwnedExplicitJSONLMatchesActualSourceRecoveryWithoutSIDState(t *testing.T) {
	data, e := os.ReadFile("testdata/source_contract.json")
	if e != nil {
		t.Fatal(e)
	}
	var corpus struct {
		Contracts []struct {
			Name     string
			Input    struct{ JSONLPath string }
			Files    map[string]string
			Expected struct{ ResultSource string }
		}
	}
	if e = json.Unmarshal(data, &corpus); e != nil {
		t.Fatal(e)
	}
	matched := false
	for _, row := range corpus.Contracts {
		if row.Name != "jsonl-path-skips-original-state" {
			continue
		}
		matched = true
		s, owner, events, refs := publicServiceReferences(t, OSFiles{})
		path := filepath.Join(s.options.OriginalCWD, "outside-current-project.JSONL")
		content := row.Files[row.Input.JSONLPath]
		if e = os.WriteFile(path, []byte(content), 0600); e != nil {
			t.Fatal(e)
		}
		var result *Result
		e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			f, e := s.BeginLoad(scope, Input{Source: quote("not-a-session-UUID"), JSONLPath: path})
			if e != nil {
				return e
			}
			select {
			case <-f.Done():
				t.Error("Native read was synchronously prepared")
			default:
			}
			result, e = f.Await(scope)
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
		actual, e := protocol.ParseAndStringifyJavaScriptJSON(encode(result))
		if e != nil || string(actual) != row.Expected.ResultSource {
			t.Fatalf("Original JSONL recovery differs: %v\nSource %s\nGo %s", e, row.Expected.ResultSource, actual)
		}
		if len(s.State().PlanSlugs) != 0 || len(s.State().SessionUUIDs) != 0 || s.UUIDTranscript(oldSID) != nil || len(s.State().PendingEntries) != 0 || len(events.errors) != 0 {
			t.Fatal("JSONL took the SID plan/backup/cache branch")
		}
		if count, idle := refs.Snapshot(); count > 0 {
			select {
			case <-idle:
			case <-time.After(5 * time.Second):
				t.Fatal("Native read references did not drain")
			}
		}
		unchanged, e := os.ReadFile(path)
		if e != nil || string(unchanged) != content {
			t.Fatal("Explicit resume modified original", e)
		}
	}
	if !matched {
		t.Fatal("Original JSONL capture missing")
	}
}
