package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func terminalUserText(t *testing.T, messages json.RawMessage, wanted []uint16) json.RawMessage {
	t.Helper()
	var rows []protocol.JSONObject
	if err := json.Unmarshal(messages, &rows); err != nil {
		t.Fatal(err)
	}
	var matches []json.RawMessage
	var candidates []json.RawMessage
	observe := func(value json.RawMessage) {
		candidates = append(candidates, value)
		units, err := protocol.DecodeJavaScriptJSONString(value)
		if err == nil && reflect.DeepEqual(units, wanted) {
			matches = append(matches, value)
		}
	}
	for _, row := range rows {
		message, _ := row.Object("message")
		if row.String("role") == "user" {
			message = row
		}
		if message.String("role") != "user" {
			continue
		}
		content := message.Get("content")
		if _, err := protocol.DecodeJavaScriptJSONString(content); err == nil {
			observe(content)
			continue
		}
		var blocks []protocol.JSONObject
		if err := json.Unmarshal(content, &blocks); err != nil {
			t.Fatal(err)
		}
		for _, block := range blocks {
			if block.String("type") == "text" {
				observe(block.Get("text"))
			}
		}
	}
	if len(matches) != 1 {
		t.Fatalf("same Harness retained %d exact user texts; candidates=%s", len(matches), raw(candidates))
	}
	return matches[0]
}

func TestTerminalOriginalUTF16ReachesActualHarnessHTTPAndCanonicalTranscript(t *testing.T) {
	for _, attachments := range []bool{false, true} {
		name := "plain"
		if attachments {
			name = "file-attachment"
		}
		t.Run(name, func(t *testing.T) {
			requests := make(chan json.RawMessage, 1)
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				requests <- body
				response(w, "utf16-done", "", nil, "UTF16 DONE")
			}))
			defer provider.Close()
			o := nativeSessionOptions(t, provider.URL)
			s, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			input := tui.Input{Mode: "prompt", Text: "display replacement is not model input", TextJSON: json.RawMessage(`"a\ud83db\udc00c\ud83d\ude42"`)}
			want := []uint16{'a', 0xd83d, 'b', 0xdc00, 'c', 0xd83d, 0xde42}
			if attachments {
				path := filepath.Join(o.WorkDir, "retained.txt")
				if err := os.WriteFile(path, []byte("retained bytes"), 0600); err != nil {
					t.Fatal(err)
				}
				input.Attachments = []json.RawMessage{raw(map[string]any{"type": "file", "path": path})}
				prefix, _ := protocol.DecodeJavaScriptJSONString(raw(`@"` + path + `" `))
				want = append(prefix, want...)
			}
			if err := s.Submit(context.Background(), input, nil); err != nil {
				t.Fatal(err)
			}
			var request protocol.JSONObject
			if err := json.Unmarshal(<-requests, &request); err != nil {
				t.Fatal(err)
			}
			observed, err := protocol.DecodeJavaScriptJSONString(terminalUserText(t, request.Get("messages"), want))
			if err != nil || !reflect.DeepEqual(observed, want) {
				t.Fatal("physical HTTP changed original code units", observed, want, err)
			}
			if err := s.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			files, err := filepath.Glob(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects", "*", o.Arguments.SessionID+".jsonl"))
			if err != nil || len(files) != 1 {
				t.Fatal("canonical checkpoint absent", files, err)
			}
			data, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			rows := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
			checkpoint := append([]byte{'['}, bytes.Join(rows, []byte{','})...)
			checkpoint = append(checkpoint, ']')
			observed, err = protocol.DecodeJavaScriptJSONString(terminalUserText(t, checkpoint, want))
			if err != nil || !reflect.DeepEqual(observed, want) {
				t.Fatal("canonical transcript changed original code units", observed, want, err)
			}
		})
	}
}

func TestTerminalInvalidTextJSONRejectsBeforeModelOrAttachmentStagingAndKeepsSessionUsable(t *testing.T) {
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		response(w, "recovered", "", nil, "RECOVERED")
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	for _, value := range []string{"", `null`, `42`, `[]`, `{"text":"not a string"}`, `"unterminated`, `"one" "two"`} {
		input := tui.Input{Mode: "prompt", Text: "must not be sent", TextJSON: json.RawMessage(value), Attachments: []json.RawMessage{json.RawMessage(`{"type":"file","name":"must-not-write.txt","data":"YQ=="}`)}}
		if err := s.Submit(context.Background(), input, nil); err == nil {
			t.Fatal("invalid raw string reached user admission", value)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid input requested a model", calls.Load())
	}
	if _, err := os.Stat(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "uploads")); !os.IsNotExist(err) {
		t.Fatal("invalid input staged protected input", err)
	}
	if err := s.Submit(context.Background(), tui.Input{Text: "ordinary text fallback"}, nil); err != nil || calls.Load() != 1 {
		t.Fatal("rejected presentation input stranded the owning Session", err, calls.Load())
	}
}
