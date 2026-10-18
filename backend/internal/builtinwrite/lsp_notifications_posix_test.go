//go:build !windows

package builtinwrite

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/lspruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestWriteReturnsAndUpdatesSharedReadCacheWhileActualLSPInitializeIsHeld(t *testing.T) {
	s, owner, _, _, home := fixture(t)
	peerPath, receipt := filepath.Join(home, "private-lsp-peer"), filepath.Join(home, "lsp-held-frames.jsonl")
	peer, err := os.ReadFile("../../testdata/lsp-file-notify-peer.py")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(peerPath, peer, 0700); err != nil {
		t.Fatal(err)
	}
	pipePath := filepath.Join(home, "initialize-release")
	if err = syscall.Mkfifo(pipePath, 0600); err != nil {
		t.Fatal(err)
	}
	release, err := os.OpenFile(pipePath, os.O_RDWR|syscall.O_NONBLOCK, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer release.Close()
	path := filepath.Join(home, "cold space 中文.txt")
	if err = os.WriteFile(path, []byte("old\nline\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := lspruntime.New(lspruntime.Options{Owner: owner, Binding: s.options.NativeBinding, CWD: home, PID: os.Getpid(), Environment: map[string]string{"HOME": home, "PATH": "/usr/bin:/bin", "CC_HAHA_LSP_INITIALIZE_RELEASE": pipePath}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(context.Background())
	manager.Configure([]lspruntime.ServerConfig{{Name: "private", Config: protocol.NewJSONObject(protocol.JSONField{Name: "command", Value: quoted(peerPath)}, protocol.JSONField{Name: "args", Value: quotedList([]string{receipt, home})}, protocol.JSONField{Name: "extensionToLanguage", Value: json.RawMessage(`{".txt":"plaintext"}`)})}}, nil)
	options := s.options
	options.LSP = manager
	options.HostState = func(*cooperative.Scope) (Host, error) { return Host{NonInteractive: true, LSPEnabled: true}, nil }
	actual, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		finished <- owner.Run(ctx, func(scope *cooperative.Scope) error {
			if err := actual.ReadForCache(scope, quoted(path)); err != nil {
				return err
			}
			if _, err := actual.Call(ctx, input(path, "new\nline\n"), scope); err != nil {
				return err
			}
			cached, found, err := options.Cache.Get(scope, path)
			if err != nil || !found || string(cached.Content) != string(quoted("new\nline\n")) {
				return errors.New("Write did not update the shared Read cache before LSP settled")
			}
			return nil
		})
	}()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("Write awaited an unrelated held LSP initialize", context.Cause(ctx))
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "new\nline\n" {
		t.Fatal("physical write did not settle independently", err, string(data))
	}
	for {
		frames := heldWriteLSPFrames(t, receipt)
		if len(frames) > 0 {
			if frames[len(frames)-1].String("method") != "initialize" {
				t.Fatal("LSP initialize was not actually held", frames)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("private peer did not receive actual initialize", context.Cause(ctx))
		case <-time.After(time.Millisecond):
		}
	}
	if _, err := release.Write([]byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := manager.JoinNotifications(ctx); err != nil {
		t.Fatal(err)
	}
	if _, found, err := manager.Request(path, "$/fixture/barrier", nil); err != nil || !found {
		t.Fatal("actual peer barrier", found, err)
	}
	methods := []string{}
	for _, frame := range heldWriteLSPFrames(t, receipt) {
		method := frame.String("method")
		if method == "textDocument/didOpen" || method == "textDocument/didChange" || method == "textDocument/didSave" {
			methods = append(methods, method)
			params, _ := frame.Object("params")
			document, _ := params.Object("textDocument")
			if frame.String("disk") != "new\nline\n" || document.String("text") != "new\nline\n" || document.String("uri") != lspruntime.URI(path) {
				t.Fatal("cold LSP open did not receive the committed file", string(frame.Get("params")))
			}
		}
	}
	if !reflect.DeepEqual(methods, []string{"textDocument/didOpen"}) {
		t.Fatal("original cold start opens changed contents and drops save before running", methods)
	}
}

func heldWriteLSPFrames(t *testing.T, path string) []protocol.JSONObject {
	t.Helper()
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	frames := []protocol.JSONObject{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var frame protocol.JSONObject
		if err := frame.UnmarshalJSON(scanner.Bytes()); err != nil {
			t.Fatal(err)
		}
		frames = append(frames, frame)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return frames
}
