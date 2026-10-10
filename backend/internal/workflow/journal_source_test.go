package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceWorkflowJournalFIFOReplayCachedPrefixAndKeyOrder(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	rows := []map[string]any{{"entries": []string{`{"type":"started","key":"a","agentId":"agent-1"}`, `{"type":"started","key":"a","agentId":"agent-2"}`, `{"type":"result","key":"a","agentId":"agent-1","result":{"future":"\ud800","z":1,"a":2}}`, `{"type":"started","key":"a","agentId":"stale-after-result"}`, `{"type":"started","key":"b","agentId":"in-flight"}`}, "keys": []map[string]string{{"prompt": `"review\ud800"`, "opts": `{"model":"fixture","label":"one","schema":{"z":1,"a":2}}`, "previous": `""`}, {"prompt": `"review\ud800"`, "opts": `{"label":"one","model":"fixture","schema":{"z":1,"a":2}}`, "previous": `"prefix"`}}}, {"seed": "not-json\n{\"type\":\"result\",\"key\":\"old\",\"agentId\":\"old-id\",\"result\":null}\n{truncated\n", "entries": []string{`{"type":"result","key":"old","agentId":"new-id","result":[0,false,null]}`, `{"type":"started","key":"pending","agentId":"one"}`, `{"type":"started","key":"pending","agentId":"two"}`}}, {"entries": []string{}}, {"seed": "null\n", "entries": []string{}}}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-workflow.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_WORKFLOW_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "journals": rows})
	cmd.Stdin = bytes.NewReader(data)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.3000s", e, output)
	}
	var source struct {
		Journals []struct {
			File     *string
			Snapshot struct{ Results, Started [][]json.RawMessage }
			Error    string
			Keys     []json.RawMessage
		}
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(output, &source); e != nil || len(source.Journals) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, len(source.Journals), source.Capabilities.Blocked)
	}
	for i, row := range rows {
		file := filepath.Join(home, "go", string(rune('a'+i)), "journal.jsonl")
		_ = os.MkdirAll(filepath.Dir(file), 0700)
		if seed, ok := row["seed"].(string); ok {
			_ = os.WriteFile(file, []byte(seed), 0600)
		}
		journal, e := NewJournal(file, NativeJournalFiles())
		if e != nil {
			t.Fatal(e)
		}
		for _, value := range row["entries"].([]string) {
			var entry protocol.JSONObject
			_ = entry.UnmarshalJSON([]byte(value))
			if _, e := journal.BeginAppend(entry); e != nil {
				t.Fatal(e)
			}
		}
		done, settled := journal.PendingFlush()
		<-done
		if e := settled(); e != nil {
			t.Fatal(e)
		}
		actual, e := os.ReadFile(file)
		if source.Journals[i].File == nil {
			if !os.IsNotExist(e) {
				t.Fatal("empty journal created disk file")
			}
		} else if e != nil || string(actual) != *source.Journals[i].File {
			t.Errorf("physical bytes%d %s/%v Source=%s", i, actual, e, *source.Journals[i].File)
		}
		loaded, e := journal.Load()
		if source.Journals[i].Error != "" {
			if e == nil || e.Error() != source.Journals[i].Error {
				t.Fatal("original load error", i, e, source.Journals[i].Error)
			}
			continue
		}
		if e != nil {
			t.Fatal(e)
		}
		wantResults := map[string]CachedAgentResult{}
		wantStarted := map[string][]json.RawMessage{}
		for _, pair := range source.Journals[i].Snapshot.Results {
			var item protocol.JSONObject
			_ = item.UnmarshalJSON(pair[1])
			wantResults[journalKey(pair[0])] = CachedAgentResult{AgentID: item.Get("agentId"), Result: item.Get("result")}
		}
		for _, pair := range source.Journals[i].Snapshot.Started {
			var ids []json.RawMessage
			_ = json.Unmarshal(pair[1], &ids)
			wantStarted[journalKey(pair[0])] = ids
		}
		canonical := func(v any) any {
			raw, _ := json.Marshal(v)
			var value any
			_ = json.Unmarshal(raw, &value)
			return value
		}
		if !reflect.DeepEqual(canonical(loaded.Results), canonical(wantResults)) || !reflect.DeepEqual(canonical(loaded.Started), canonical(wantStarted)) {
			t.Errorf("snapshot%d Go=%v/%v Source=%v/%v", i, loaded.Results, loaded.Started, wantResults, wantStarted)
		}
		if keys, ok := row["keys"].([]map[string]string); ok {
			for k, key := range keys {
				actual, e := CacheKey([]byte(key["prompt"]), []byte(key["opts"]), []byte(key["previous"]))
				if e != nil || string(actual) != string(source.Journals[i].Keys[k]) {
					t.Errorf("key%d:%d Go=%s/%v Source=%s", i, k, actual, e, source.Journals[i].Keys[k])
				}
			}
		}
	}
}

func TestJournalFailurePoisonsNativeAppendChainAndFlush(t *testing.T) {
	called := []string{}
	journal, e := NewJournal(filepath.Join(t.TempDir(), "journal.jsonl"), JournalFiles{ReadFile: os.ReadFile, MkdirAll: func(string) error { called = append(called, "mkdir"); return nil }, AppendFile: func(string, []byte) error { called = append(called, "append"); return os.ErrPermission }})
	if e != nil {
		t.Fatal(e)
	}
	first, _ := journal.BeginAppend(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: []byte(`"started"`)}))
	second, _ := journal.BeginAppend(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: []byte(`"result"`)}))
	<-first.Done()
	<-second.Done()
	done, err := journal.PendingFlush()
	<-done
	if first.Err() != os.ErrPermission || second.Err() != os.ErrPermission || err() != os.ErrPermission {
		t.Fatal("original rejected chain lost failure")
	}
	if !reflect.DeepEqual(called, []string{"mkdir", "append"}) {
		sort.Strings(called)
		t.Fatal("wrote through failed chain", called)
	}
}
