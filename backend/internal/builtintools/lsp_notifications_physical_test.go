package builtintools

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/lspruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestEditActualHotLSPPeerReceivesChangedContentAndSaveAfterPhysicalWrite(t *testing.T) {
	s, owner, home, _ := fixture(t)
	peerPath, receipt := filepath.Join(home, "private-lsp-peer"), filepath.Join(home, "lsp-frames.jsonl")
	peer, err := os.ReadFile("../../testdata/lsp-file-notify-peer.py")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(peerPath, peer, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "actual space 中文.txt")
	if err := os.WriteFile(path, []byte("old\nline\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := lspruntime.New(lspruntime.Options{Owner: owner, Binding: s.options.NativeBinding, CWD: home, PID: os.Getpid(), Environment: map[string]string{"HOME": home, "PATH": "/usr/bin:/bin"}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(context.Background())
	manager.Configure([]lspruntime.ServerConfig{{Name: "private", Config: protocol.NewJSONObject(field("command", peerPath), field("args", []string{receipt, home}), protocol.JSONField{Name: "extensionToLanguage", Value: json.RawMessage(`{".txt":"plaintext"}`)})}}, nil)
	if _, err := os.Stat(receipt); !os.IsNotExist(err) {
		t.Fatal("configuration eagerly started LSP", err)
	}
	if err := manager.Open(path, quote("old\nline\n")); err != nil {
		t.Fatal(err)
	}
	options := s.options
	options.LSP = manager
	actual, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	call(t, actual, owner, "Read", protocol.NewJSONObject(field("file_path", path)))
	call(t, actual, owner, "Edit", protocol.NewJSONObject(field("file_path", path), field("old_string", "old"), field("new_string", "new")))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := manager.JoinNotifications(ctx); err != nil {
		t.Fatal(err)
	}
	if _, found, err := manager.Request(path, "$/fixture/barrier", nil); err != nil || !found {
		t.Fatal("actual peer barrier", found, err)
	}
	file, err := os.Open(receipt)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	methods := []string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var row protocol.JSONObject
		if err := row.UnmarshalJSON(scanner.Bytes()); err != nil {
			t.Fatal(err)
		}
		method := row.String("method")
		if method != "textDocument/didChange" && method != "textDocument/didSave" {
			continue
		}
		methods = append(methods, method)
		if row.String("disk") != "new\nline\n" {
			t.Fatal("LSP observed file before actual edit", string(scanner.Bytes()))
		}
		params, _ := row.Object("params")
		document, _ := params.Object("textDocument")
		if document.String("uri") != lspruntime.URI(path) {
			t.Fatal("actual file URI changed", string(scanner.Bytes()))
		}
		if method == "textDocument/didChange" {
			var changes []protocol.JSONObject
			_ = json.Unmarshal(params.Get("contentChanges"), &changes)
			if len(changes) != 1 || changes[0].String("text") != "new\nline\n" || string(document.Get("version")) != "1" {
				t.Fatal("actual edit change payload changed", string(scanner.Bytes()))
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(methods, []string{"textDocument/didChange", "textDocument/didSave"}) {
		t.Fatal("original hot LSP notification order", methods)
	}
}
