package application

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestActualInteractiveBatchKeepsThreeOriginalUsersAndOneTurnAttachment(t *testing.T) {
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests.Add(1)
		if !strings.Contains(string(body), "FIRST_INTERACTIVE_QUEUED @context.txt") || !strings.Contains(string(body), "SECOND_INTERACTIVE_QUEUED") || !strings.Contains(string(body), "THIRD_DIRECT_CURRENT") || strings.Count(string(body), "ACTUAL_ONE_ATTACHMENT_C0") != 1 {
			t.Errorf("actual batch/first-only context lost %.3000s", body)
		}
		response(w, "queued-done", "", nil, "ORIGINAL_INTERACTIVE_BATCH_DONE")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.ForegroundShellProgress = true
	if e := os.WriteFile(filepath.Join(o.WorkDir, "context.txt"), []byte("ACTUAL_ONE_ATTACHMENT_C0"), 0600); e != nil {
		t.Fatal(e)
	}
	s, e := NewSession(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	ids := []string{"57e3f0f6-73b3-4d00-997d-812e9c36a001", "57e3f0f6-73b3-4d00-997d-812e9c36a002"}
	for i, text := range []string{"FIRST_INTERACTIVE_QUEUED @context.txt", "SECOND_INTERACTIVE_QUEUED"} {
		e = s.process.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("user")}, protocol.JSONField{Name: "uuid", Value: raw(ids[i])}, protocol.JSONField{Name: "message", Value: raw(map[string]any{"role": "user", "content": text})})
			future, e := s.process.Host.AdmitParsedSDK(scope, input)
			if e != nil {
				return e
			}
			_, e = future.Await(scope)
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	if e = s.Submit(context.Background(), tui.Input{TurnID: "direct-current", Text: "THIRD_DIRECT_CURRENT", Mode: "prompt"}, nil); e != nil {
		t.Fatal(e)
	}
	if requests.Load() != 1 {
		t.Fatal("interactive batch opened more than one Harness", requests.Load())
	}
	if e = s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	paths, _ := filepath.Glob(filepath.Join(o.Native.Home, ".claude", "projects", "*", "*.jsonl"))
	found := map[string]string{}
	for _, path := range paths {
		body, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		for _, line := range strings.Split(string(body), "\n") {
			var row protocol.JSONObject
			if e = row.UnmarshalJSON(json.RawMessage(line)); e != nil {
				continue
			}
			message, _ := row.Object("message")
			content := message.String("content")
			if strings.HasPrefix(content, "FIRST_INTERACTIVE_QUEUED") || strings.HasPrefix(content, "SECOND_INTERACTIVE_QUEUED") || content == "THIRD_DIRECT_CURRENT" {
				found[row.String("uuid")] = content
			}
		}
	}
	if len(found) != 3 || found[ids[0]] != "FIRST_INTERACTIVE_QUEUED @context.txt" || found[ids[1]] != "SECOND_INTERACTIVE_QUEUED" {
		t.Fatal("interactive users lost original distinct UUID/history", found)
	}
}
