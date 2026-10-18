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
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualTerminalConversationClipboardExportRenameColorAndNextTurn(t *testing.T) {
	var ordinary, names atomic.Int32
	var work string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "Generate a short kebab-case name") {
			names.Add(1)
			if !strings.Contains(string(body), `"model":"name-fixture"`) || !strings.Contains(string(body), `"required":["name"]`) || !strings.Contains(string(body), "SECOND_RESPONSE") || strings.Contains(string(body), `"name":"Write"`) {
				t.Error("rename did not consume actual small-model purpose/schema/context", string(body))
			}
			response(w, "generated-name", "", nil, `{"name":"generated-topic"}`)
			return
		}
		n := ordinary.Add(1)
		if strings.Contains(string(body), "/copy") || strings.Contains(string(body), "/export") || strings.Contains(string(body), "/rename") || strings.Contains(string(body), "/color") {
			t.Error("local conversation command leaked into model", string(body))
		}
		switch n {
		case 1:
			response(w, "physical-write", "Write", map[string]string{"file_path": filepath.Join(work, "actual-command-file.txt"), "content": "ACTUAL_COMMAND_FILE"}, "")
		case 2:
			response(w, "first-response", "", nil, "FIRST_RESPONSE\n```go\nfmt.Println(\"CODE_ONE\")\n```")
		case 3:
			response(w, "second-response", "", nil, "SECOND_RESPONSE")
		default:
			response(w, "next-human", "", nil, "NEXT_HUMAN_COMPLETE")
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	work = o.WorkDir
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = "name-fixture"
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, ".claude.json"), []byte(`{"future":{"preserve":17}}`), 0600); err != nil {
		t.Fatal(err)
	}
	session, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close(context.Background())
	clipboard := t.TempDir()
	receipt := filepath.Join(clipboard, "literal-clipboard.txt")
	if err := os.WriteFile(filepath.Join(clipboard, "pbcopy"), []byte("#!/bin/sh\n/bin/cat > \"$CLIPBOARD_RECEIPT\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	var panel *tui.LocalPanel
	var notice string
	var presentation json.RawMessage
	emit := func(event tui.Event) error {
		if event.LocalPanel != nil {
			copy := *event.LocalPanel
			panel = &copy
		}
		if event.Kind == tui.Notice {
			notice = event.Text
		}
		if event.Kind == tui.SessionPresentationChanged {
			presentation = event.Result
		}
		if event.Kind == tui.ClipboardTextRequested {
			var output bytes.Buffer
			err := tui.WriteClipboardText(context.Background(), "darwin", []string{"PATH=" + clipboard, "HOME=" + clipboard, "CLIPBOARD_RECEIPT=" + receipt}, event.Text, &output)
			event.ClipboardTextReply <- err
			if !strings.Contains(output.String(), "\x1b]52;c;") {
				t.Error("actual native clipboard fallback OSC missing")
			}
		}
		return nil
	}
	invoke := func(name, args string) {
		t.Helper()
		handled, err := session.InvokeTerminalCommand(context.Background(), name, args, emit)
		if err != nil || !handled {
			t.Fatal("actual registered conversation entry failed", name, args, handled, err)
		}
	}
	invoke("rename", " human-title ")
	invoke("color", "PINK")
	if obj(presentation).String("name") != "human-title" || obj(presentation).String("color") != "pink" {
		t.Fatal("prompt presentation did not update", string(presentation))
	}
	if ordinary.Load() != 0 || names.Load() != 0 {
		t.Fatal("cold explicit name/color performed inference")
	}
	if err := session.Submit(context.Background(), tui.Input{Text: "HUMAN_SOURCE_ONE actual Write"}, nil); err != nil {
		t.Fatal(err)
	}
	if contents, err := os.ReadFile(filepath.Join(work, "actual-command-file.txt")); err != nil || string(contents) != "ACTUAL_COMMAND_FILE" {
		t.Fatal("real history lacked actual tool effect", err, string(contents))
	}
	invoke("copy", "bad")
	if !strings.HasPrefix(notice, "Usage: /copy") {
		value, _ := session.process.TerminalConversation(context.Background())
		t.Fatal(notice, ordinary.Load(), string(raw(value.Messages)))
	}
	invoke("copy", "1")
	if panel == nil || panel.Kind != "copy" || len(panel.Rows) != 3 {
		t.Fatal("Source code picker did not open", panel)
	}
	id := panel.ID
	if err := session.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: id, Action: "select", Row: "0"}, emit); err != nil {
		t.Fatal(err)
	}
	if contents, err := os.ReadFile(receipt); err != nil || string(contents) != `fmt.Println("CODE_ONE")` {
		t.Fatal("actual clipboard stdin did not receive selected code", err, string(contents))
	}
	value, err := session.process.TerminalConversation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if contents, err := os.ReadFile(filepath.Join(value.Temp, "claude", "copy.go")); err != nil || string(contents) != `fmt.Println("CODE_ONE")` {
		t.Fatal("reliable selected-code file fallback absent", err, string(contents))
	}
	if err := session.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: id, Action: "select", Row: "always"}, emit); err == nil {
		t.Fatal("retired panel accepted stale selection")
	}
	invoke("copy", "")
	if err := session.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "select", Row: "always"}, emit); err != nil {
		t.Fatal(err)
	}
	var global map[string]json.RawMessage
	contents, err := os.ReadFile(filepath.Join(config, ".claude.json"))
	if err != nil || json.Unmarshal(contents, &global) != nil || string(global["copyFullResponse"]) != "true" || string(obj(global["future"]).Get("preserve")) != "17" {
		t.Fatal("copy preference lost old unknown global fields", err, string(contents))
	}
	if err := session.Submit(context.Background(), tui.Input{Text: "HUMAN_SOURCE_TWO"}, nil); err != nil {
		t.Fatal(err)
	}
	invoke("copy", "2")
	contents, err = os.ReadFile(receipt)
	if err != nil || !strings.HasPrefix(string(contents), "FIRST_RESPONSE") {
		t.Fatal("Nth-latest text did not skip native tool-only message", err, string(contents))
	}
	invoke("copy", "")
	contents, err = os.ReadFile(receipt)
	if err != nil || string(contents) != "SECOND_RESPONSE" {
		t.Fatal("always-copy setting did not reach next actual consumer", err, string(contents))
	}
	invoke("export", "export-result.md")
	contents, err = os.ReadFile(filepath.Join(work, "export-result.txt"))
	if err != nil || !strings.Contains(string(contents), "HUMAN_SOURCE_ONE") || !strings.Contains(string(contents), "SECOND_RESPONSE") || !strings.Contains(string(contents), "File created successfully at:") {
		t.Fatal("actual transcript export dropped message/tool data", err, string(contents))
	}
	invoke("export", "")
	if panel.Kind != "export" {
		t.Fatal(panel)
	}
	if err := session.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "select", Row: "file"}, emit); err != nil {
		t.Fatal(err)
	}
	if len(panel.Fields) != 1 {
		t.Fatal("real export filename form missing", panel)
	}
	if err := session.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "close", Fields: map[string]string{"filename": "kept-filename"}}, emit); err != nil || len(panel.Rows) != 2 {
		t.Fatal("export Esc did not return original options", err, panel)
	}
	if err := session.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "select", Row: "file"}, emit); err != nil || panel.Fields[0].Value != "kept-filename" {
		t.Fatal("backtracking lost typed filename", err, panel)
	}
	if err := session.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "save", Fields: map[string]string{"filename": "form-export.json"}}, emit); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(work, "form-export.txt")); err != nil {
		t.Fatal("actual form export consumer did not create selected file", err)
	}
	invoke("rename", "")
	if names.Load() != 1 || notice != "Session renamed to: generated-topic" {
		t.Fatal("automatic rename side model did not settle", names.Load(), notice)
	}
	invoke("color", "grey")
	if obj(presentation).String("name") != "generated-topic" || obj(presentation).String("color") != "" {
		t.Fatal("actual prompt reset sentinel projection differed", string(presentation))
	}
	if handled, err := session.InvokeTerminalCommand(context.Background(), "tag", "private", emit); err != nil || handled {
		t.Fatal("ant-only command escaped normal Source gate", handled, err)
	}
	if err := session.Submit(context.Background(), tui.Input{Text: "NEXT_HUMAN_REAL"}, nil); err != nil {
		t.Fatal(err)
	}
	if ordinary.Load() != 4 || names.Load() != 1 {
		t.Fatal("local commands opened extra requests or destroyed next turn", ordinary.Load(), names.Load())
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	transcripts, err := filepath.Glob(filepath.Join(config, "projects", "*", o.Arguments.SessionID+".jsonl"))
	if err != nil || len(transcripts) != 1 {
		t.Fatal("actual native transcript missing", err, transcripts)
	}
	contents, err = os.ReadFile(transcripts[0])
	if err != nil || !strings.Contains(string(contents), `"customTitle":"generated-topic"`) || !strings.Contains(string(contents), `"agentName":"generated-topic"`) || !strings.Contains(string(contents), `"agentColor":"default"`) {
		t.Fatal("native name/color reset did not persist original entry shapes", err, string(contents))
	}
	o.Arguments.Resume = o.Arguments.SessionID
	o.Arguments.SessionID = ""
	resumed, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close(context.Background())
	if history, _, err := resumed.initialTUIHistory(context.Background()); err != nil || len(history) == 0 {
		t.Fatal("actual CLI resume startup did not restore old history", err, len(history))
	}
	restored, err := resumed.TerminalPresentation(context.Background())
	// Original getLastSessionLog's SID selector takes agentName from the
	// root conversation entry. Persisted agent-name/color metadata is restored
	// by loadFullLog used by --continue/the picker, not this SID-only branch.
	if err != nil || obj(restored).String("name") != "" || obj(restored).String("color") != "" {
		t.Fatal("SID presentation diverged from original selector", err, string(restored))
	}
	if err := resumed.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	o.Arguments.Resume, o.Arguments.Continue = "", true
	continued, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer continued.Close(context.Background())
	if history, _, err := continued.initialTUIHistory(context.Background()); err != nil || len(history) == 0 {
		t.Fatal("actual CLI continue startup did not restore old history", err, len(history))
	}
	restored, err = continued.TerminalPresentation(context.Background())
	if err != nil || obj(restored).String("name") != "generated-topic" || obj(restored).String("color") != "" {
		t.Fatal("cold continue metadata did not restore actual prompt presentation", err, string(restored))
	}
}
