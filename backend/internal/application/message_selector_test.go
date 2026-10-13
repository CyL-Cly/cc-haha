package application

import (
	"context"
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

func TestActualTerminalSelectorSummarizesThenResubmitsSameSession(t *testing.T) {
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := requests.Add(1)
		switch n {
		case 1:
			response(w, "selector-original", "", nil, "SELECTOR_ORIGINAL_TASK_DONE")
		case 2:
			if !strings.Contains(string(body), "CRITICAL: Respond with TEXT ONLY") || !strings.Contains(string(body), "retain user instructions") {
				t.Error("selector feedback didn't reach actual model", string(body))
			}
			response(w, "selector-summary", "", nil, "<summary>SELECTOR_SAVED_TASK</summary>")
		case 3:
			if !strings.Contains(string(body), "SELECTOR_SAVED_TASK") {
				t.Error("selector next turn lost summary", string(body))
			}
			response(w, "selector-after", "", nil, "SELECTOR_AFTER_DONE")
		default:
			t.Error(n)
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	s, e := NewSession(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	if e = s.Submit(context.Background(), tui.Input{Text: "Original authored request", TurnID: "original"}, nil); e != nil {
		t.Fatal(e)
	}
	if e = s.process.SetSessionPermissionMode(context.Background(), "acceptEdits"); e != nil {
		t.Fatal(e)
	}
	snapshot, e := s.MessageSelectorSnapshot(context.Background())
	if e != nil || len(snapshot.Choices) != 1 {
		t.Fatal(snapshot, e)
	}
	got, e := s.SelectMessage(context.Background(), tui.MessageSelection{ID: snapshot.Choices[0].ID, Action: "summarize", Feedback: "retain user instructions"}, nil)
	if e != nil || got.Prefill != "Original authored request" || len(got.History) == 0 {
		t.Fatal(got, e)
	}
	if e = s.Submit(context.Background(), tui.Input{Text: "Continue the original task", TurnID: "continue"}, nil); e != nil {
		t.Fatal(e)
	}
	if requests.Load() != 3 {
		t.Fatal(requests.Load())
	}
	if e = s.process.SetSessionPermissionMode(context.Background(), "default"); e != nil {
		t.Fatal(e)
	}
	snapshot, e = s.MessageSelectorSnapshot(context.Background())
	if e != nil || len(snapshot.Choices) == 0 {
		t.Fatal(snapshot, e)
	}
	restored, e := s.SelectMessage(context.Background(), tui.MessageSelection{ID: snapshot.Choices[len(snapshot.Choices)-1].ID, Action: "conversation"}, nil)
	if e != nil || restored.PermissionMode != "acceptEdits" {
		t.Fatal("authored permission mode was not restored", restored, e)
	}
}

func TestActualTerminalSelectorRestoresPhysicalCodeAndConversation(t *testing.T) {
	for _, action := range []string{"code", "both"} {
		t.Run(action, func(t *testing.T) {
			var count atomic.Int32
			var path string
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch n := count.Add(1); n {
				case 1:
					response(w, "rewind-read", "Read", map[string]any{"file_path": path}, "")
				case 2:
					response(w, "rewind-write", "Write", map[string]any{"file_path": path, "content": "AFTER_NATIVE_WRITE"}, "")
				case 3:
					response(w, "rewind-end", "", nil, "NATIVE_FILE_EDIT_DONE")
				default:
					t.Error("unexpected model request", n)
				}
			}))
			defer peer.Close()
			o := nativeSessionOptions(t, peer.URL)
			o.Interactive = true
			path = filepath.Join(o.WorkDir, "rewind.txt")
			if e := os.WriteFile(path, []byte("BEFORE_NATIVE_WRITE"), 0600); e != nil {
				t.Fatal(e)
			}
			s, e := NewSession(context.Background(), o)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close(context.Background())
			if e = s.Submit(context.Background(), tui.Input{Text: "Edit the tracked file", TurnID: "edit"}, nil); e != nil {
				t.Fatal(e)
			}
			data, e := os.ReadFile(path)
			if e != nil || string(data) != "AFTER_NATIVE_WRITE" {
				t.Fatal("physical tool write missing", string(data), e)
			}
			snapshot, e := s.MessageSelectorSnapshot(context.Background())
			if e != nil || len(snapshot.Choices) != 1 || !snapshot.FileHistoryEnabled || !snapshot.Choices[0].CanRestoreCode {
				t.Fatal(snapshot, e)
			}
			got, e := s.SelectMessage(context.Background(), tui.MessageSelection{ID: snapshot.Choices[0].ID, Action: action}, nil)
			if e != nil {
				t.Fatal(e)
			}
			data, e = os.ReadFile(path)
			if e != nil || string(data) != "BEFORE_NATIVE_WRITE" {
				t.Fatal("physical rewind missing", string(data), e)
			}
			if count.Load() != 3 {
				t.Fatal("rewind must perform filesystem/conversation operation without model", count.Load())
			}
			if action == "both" {
				if len(got.History) != 0 || got.Prefill != "Edit the tracked file" || got.Mode != "prompt" {
					t.Fatal(got)
				}
			} else if len(got.History) == 0 || got.Mode != "" {
				t.Fatal("code-only changed conversation", got)
			}
		})
	}
}
