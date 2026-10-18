package builtinwrite

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/lspruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestWriteActualHotLSPPeerReceivesChangedContentAndSaveAfterPhysicalWrite(t *testing.T) {
	s, owner, _, _, home := fixture(t)
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
	manager.Configure([]lspruntime.ServerConfig{{Name: "private", Config: protocol.NewJSONObject(protocol.JSONField{Name: "command", Value: quoted(peerPath)}, protocol.JSONField{Name: "args", Value: quotedList([]string{receipt, home})}, protocol.JSONField{Name: "extensionToLanguage", Value: json.RawMessage(`{".txt":"plaintext"}`)})}}, nil)
	if _, err := os.Stat(receipt); !os.IsNotExist(err) {
		t.Fatal("configuration eagerly started LSP", err)
	}
	if err := manager.Open(path, quoted("old\nline\n")); err != nil {
		t.Fatal(err)
	}
	options := s.options
	options.LSP = manager
	options.HostState = func(*cooperative.Scope) (Host, error) { return Host{NonInteractive: true, LSPEnabled: true}, nil }
	actual, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	run(t, owner, func(scope *cooperative.Scope) error {
		if err := actual.ReadForCache(scope, quoted(path)); err != nil {
			return err
		}
		_, err := actual.Call(context.Background(), input(path, "new\nline\n"), scope)
		return err
	})
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
			t.Fatal("LSP observed file before actual write", string(scanner.Bytes()))
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
				t.Fatal("actual change payload changed", string(scanner.Bytes()))
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

func quotedList(values []string) json.RawMessage { data, _ := json.Marshal(values); return data }
