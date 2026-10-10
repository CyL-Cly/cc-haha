package builtinwrite

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type observedFiles struct {
	OSFiles
	mu     sync.Mutex
	events []string
}

func (f *observedFiles) note(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, s)
}
func (f *observedFiles) WriteFlush(p string, b []byte) error {
	f.note("write " + p)
	return f.OSFiles.WriteFlush(p, b)
}
func (f *observedFiles) Rename(a, b string) error {
	f.note("rename " + a + " -> " + b)
	return f.OSFiles.Rename(a, b)
}
func fixture(t *testing.T) (*Service, *cooperative.Owner, *ownerasync.NativeReferences, *observedFiles, string) {
	t.Helper()
	home := t.TempDir()
	owner := cooperative.New()
	refs := &ownerasync.NativeReferences{}
	binding, e := refs.Bind(owner)
	if e != nil {
		t.Fatal(e)
	}
	cache, e := NewFileStateCache(owner, 100, 25*1024*1024)
	if e != nil {
		t.Fatal(e)
	}
	f := &observedFiles{}
	permissions := protocol.NewJSONObject(protocol.JSONField{Name: "mode", Value: quoted("bypassPermissions")}, protocol.JSONField{Name: "alwaysDenyRules", Value: json.RawMessage("{}")})
	s, e := New(Options{Owner: owner, NativeBinding: binding, Cache: cache, Home: home, CWD: home, PID: os.Getpid(), NowMillis: func() float64 { return 1000 }, Environment: func(string) string { return "" }, PermissionContext: func(*cooperative.Scope) (protocol.JSONObject, error) { return permissions.Clone(), nil }, Host: Host{Platform: "darwin"}, HostState: func(*cooperative.Scope) (Host, error) { return Host{NonInteractive: true}, nil }, Files: f})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		owner.Close()
		n, idle := refs.Snapshot()
		if n != 0 {
			select {
			case <-idle:
			case <-time.After(time.Second):
				t.Error("native reference leaked")
			}
		}
		n, _ = refs.Snapshot()
		if n != 0 {
			t.Errorf("native references %d", n)
		}
	})
	return s, owner, refs, f, home
}
func input(path, content string) protocol.JSONObject {
	return protocol.NewJSONObject(protocol.JSONField{Name: "file_path", Value: quoted(path)}, protocol.JSONField{Name: "content", Value: quoted(content)})
}
func run(t *testing.T, owner *cooperative.Owner, fn func(*cooperative.Scope) error) {
	t.Helper()
	if e := owner.Run(context.Background(), fn); e != nil {
		t.Fatal(e)
	}
}
func TestWriteNativeCreatesActualFileAndMappedResult(t *testing.T) {
	s, o, _, f, home := fixture(t)
	path := filepath.Join(home, "arbitrary.txt")
	in := input("arbitrary.txt", "hello\r\n世界\n")
	var data json.RawMessage
	run(t, o, func(scope *cooperative.Scope) error {
		if e := s.Validate(context.Background(), in, scope); e != nil {
			return e
		}
		decision, e := s.Permission(scope, in)
		if e != nil {
			return e
		}
		if decision != "allow" {
			return fmt.Errorf("unexpected decision %s", decision)
		}
		data, e = s.Call(context.Background(), in, scope)
		return e
	})
	b, e := os.ReadFile(path)
	if e != nil || string(b) != "hello\r\n世界\n" {
		t.Fatalf("real bytes %q %v", b, e)
	}
	if len(f.events) != 2 {
		t.Fatalf("native side effects %v", f.events)
	}
	block, e := MapResultJSON(data, json.RawMessage(`"id\ud800"`))
	if e != nil {
		t.Fatal(e)
	}
	text, e := scalar(block.Get("content"))
	if e != nil || text != "File created successfully at: arbitrary.txt" {
		t.Fatalf("actual result %s %v", raw(block), e)
	}
	run(t, o, func(scope *cooperative.Scope) error {
		state, ok, e := s.options.Cache.Get(scope, path)
		if e != nil {
			return e
		}
		if !ok || string(state.Content) != string(quoted("hello\r\n世界\n")) {
			return fmt.Errorf("post-write cache missing")
		}
		return nil
	})
}
func TestWriteNativeCreatesNestedEmptyFile(t *testing.T) {
	s, o, _, _, home := fixture(t)
	in := input("nested/empty", "")
	run(t, o, func(scope *cooperative.Scope) error { _, e := s.Call(context.Background(), in, scope); return e })
	b, e := os.ReadFile(filepath.Join(home, "nested/empty"))
	if e != nil || len(b) != 0 {
		t.Fatalf("actual empty file %q %v", b, e)
	}
}
func TestWriteNativeExistingEmptyFileRetainsMode(t *testing.T) {
	s, o, _, _, home := fixture(t)
	path := filepath.Join(home, "existing")
	if e := os.WriteFile(path, nil, 0640); e != nil {
		t.Fatal(e)
	}
	in := input(path, "replace\r\n")
	run(t, o, func(scope *cooperative.Scope) error {
		if e := s.ReadForCache(scope, quoted(path)); e != nil {
			return e
		}
		if e := s.Validate(context.Background(), in, scope); e != nil {
			return e
		}
		_, e := s.Call(context.Background(), in, scope)
		return e
	})
	info, e := os.Stat(path)
	if e != nil || info.Mode().Perm() != 0640 {
		t.Fatalf("mode %v %v", info, e)
	}
}
