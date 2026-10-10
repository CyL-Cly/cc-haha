package application

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAgentExecutesSharedHarnessAndRecordsOriginalSidechain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var calls atomic.Int32
	var file string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch calls.Add(1) {
		case 1:
			response(w, "parent-agent", "Agent", map[string]any{"prompt": "Create the child file and report completion", "description": "actual shared child", "subagent_type": "general-purpose"}, "")
		case 2:
			for _, want := range []string{"You are an agent for Claude Code", "Notes:", "<env>", "Create the child file"} {
				if !bytes.Contains(body, []byte(want)) {
					t.Errorf("child context missing %q: %s", want, body)
				}
			}
			if bytes.Contains(body, []byte("actual shared child")) {
				t.Error("parent description injected into child prompt")
			}
			response(w, "child-write", "Write", map[string]any{"file_path": file, "content": "actual child output\n"}, "")
		case 3:
			if data, err := os.ReadFile(file); err != nil || string(data) != "actual child output\n" {
				t.Errorf("child tool did not execute: %s %v", data, err)
			}
			response(w, "child-done", "", nil, "CHILD ORIGINAL RESULT")
		case 4:
			if !bytes.Contains(body, []byte("CHILD ORIGINAL RESULT")) || !bytes.Contains(body, []byte("agentId:")) {
				t.Errorf("Agent finalizer did not reach parent request: %s", body)
			}
			response(w, "parent-done", "", nil, "PARENT ORIGINAL RESULT")
		default:
			t.Error("unexpected extra model consumer")
			http.Error(w, "unexpected", 400)
		}
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	file = filepath.Join(o.WorkDir, "child.txt")
	o.Arguments.Tools = []string{"Read,Write,Edit,Bash,Glob,Grep,Agent"}
	var mu sync.Mutex
	output := []json.RawMessage{}
	o.OnSDK = func(data json.RawMessage) error {
		mu.Lock()
		output = append(output, append(json.RawMessage(nil), data...))
		mu.Unlock()
		return nil
	}
	s, err := NewSession(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Submit(ctx, tui.Input{Text: "Run an actual child Agent"}, nil); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 4 {
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("child didn't reach its own Harness: calls=%d output=%s", calls.Load(), raw(output))
	}
	var parent, child []byte
	var childPath string
	if err = filepath.WalkDir(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if strings.Contains(path, string(filepath.Separator)+"subagents"+string(filepath.Separator)) {
			child = append(child, data...)
			childPath = path
		} else {
			parent = append(parent, data...)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(child, []byte("CHILD ORIGINAL RESULT")) || !bytes.Contains(child, []byte(`"isSidechain":true`)) || !bytes.Contains(child, []byte(`"agentId":"a`)) {
		t.Fatalf("original sidechain not persisted: path=%s %s", childPath, child)
	}
	if !bytes.Contains(parent, []byte("PARENT ORIGINAL RESULT")) || bytes.Contains(parent, []byte(`"id":"child-done"`)) {
		t.Fatalf("child original assistant leaked into parent transcript: %s", parent)
	}
	if _, err = os.Stat(strings.TrimSuffix(childPath, ".jsonl") + ".meta.json"); err != nil {
		t.Fatalf("actual Source agent metadata missing: %v", err)
	}
}
