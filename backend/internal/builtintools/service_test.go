package builtintools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func fixture(t *testing.T) (*Service, *cooperative.Owner, string, *protocol.JSONObject) {
	t.Helper()
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	owner := cooperative.New()
	refs := &ownerasync.NativeReferences{}
	binding, e := refs.Bind(owner)
	if e != nil {
		t.Fatal(e)
	}
	cache, e := builtinwrite.NewFileStateCache(owner, 100, 25*1024*1024)
	if e != nil {
		t.Fatal(e)
	}
	permissions := protocol.NewJSONObject(field("mode", "bypassPermissions"), field("alwaysDenyRules", map[string][]string{}), field("alwaysAllowRules", map[string][]string{}), field("alwaysAskRules", map[string][]string{}))
	permission := func(*cooperative.Scope) (protocol.JSONObject, error) { return permissions.Clone(), nil }
	write, e := builtinwrite.New(builtinwrite.Options{Owner: owner, NativeBinding: binding, Cache: cache, Home: home, CWD: home, PID: os.Getpid(), NowMillis: func() float64 { return float64(time.Now().UnixMilli()) }, Environment: func(string) string { return "" }, PermissionContext: permission, Host: builtinwrite.Host{Platform: runtime.GOOS}, HostState: func(*cooperative.Scope) (builtinwrite.Host, error) {
		return builtinwrite.Host{NonInteractive: true}, nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	s, e := New(Options{NonInteractive: true, Owner: owner, NativeBinding: binding, Cache: cache, Write: write, Home: home, CWD: home, Platform: runtime.GOOS, NowMillis: func() float64 { return float64(time.Now().UnixMilli()) }, Environment: map[string]string{"HOME": home, "PATH": "/opt/homebrew/bin:/usr/bin:/bin", "SHELL": "/bin/bash"}, PermissionContext: permission, PermissionRoots: map[string]string{"projectSettings": home, "userSettings": home}})
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
				t.Error("native work leaked")
			}
		}
		n, _ = refs.Snapshot()
		if n != 0 {
			t.Errorf("native obligations %d", n)
		}
	})
	return s, owner, home, &permissions
}
func input(text string) protocol.JSONObject {
	var o protocol.JSONObject
	if e := json.Unmarshal([]byte(text), &o); e != nil {
		panic(e)
	}
	return o
}
func call(t *testing.T, s *Service, o *cooperative.Owner, name string, in protocol.JSONObject) json.RawMessage {
	t.Helper()
	var data json.RawMessage
	e := o.Run(context.Background(), func(scope *cooperative.Scope) error {
		if e := s.Validate(context.Background(), name, in, scope); e != nil {
			return e
		}
		behavior, e := s.Permission(scope, name, in)
		if e != nil {
			return e
		}
		if behavior != "allow" {
			return fmt.Errorf("permission %s", behavior)
		}
		data, e = s.Call(context.Background(), name, in, scope, false)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	return data
}
func TestActualReadEditOverwriteWriteShareOneLiveCache(t *testing.T) {
	s, o, home, _ := fixture(t)
	p := filepath.Join(home, "chain.txt")
	if e := os.WriteFile(p, []byte("hello\r\n世界\r\n"), 0640); e != nil {
		t.Fatal(e)
	}
	read := call(t, s, o, "Read", input(`{"file_path":"chain.txt"}`))
	mapped, e := MapResult("Read", read, quote("read-id"))
	if e != nil || !strings.HasPrefix(mapped.String("content"), "1\thello\n2\t世界\n3\t") {
		t.Fatalf("read mapping %s %v", raw(mapped), e)
	}
	edited := call(t, s, o, "Edit", input(`{"file_path":"chain.txt","old_string":"hello","new_string":"goodbye"}`))
	var result protocol.JSONObject
	_ = json.Unmarshal(edited, &result)
	if result.String("originalFile") != "hello\n世界\n" {
		t.Fatalf("original %s", edited)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "goodbye\r\n世界\r\n" {
		t.Fatalf("CRLF %q", b)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0640 {
		t.Fatalf("mode %v", st.Mode())
	}
	data := call(t, s, o, "Write", input(`{"file_path":"chain.txt","content":"overwritten\n"}`))
	_ = json.Unmarshal(data, &result)
	if result.String("type") != "update" || result.String("originalFile") != "goodbye\n世界\n" {
		t.Fatalf("write result %s", data)
	}
	b, _ = os.ReadFile(p)
	if string(b) != "overwritten\n" {
		t.Fatalf("actual bytes %q", b)
	}
}
func TestReadRangeDedupAndStaleEditRejection(t *testing.T) {
	s, o, home, _ := fixture(t)
	p := filepath.Join(home, "range")
	_ = os.WriteFile(p, []byte("one\ntwo\nthree\n"), 0600)
	in := input(`{"file_path":"range","offset":2,"limit":1}`)
	first := call(t, s, o, "Read", in)
	var result protocol.JSONObject
	_ = json.Unmarshal(first, &result)
	var file protocol.JSONObject
	_ = json.Unmarshal(result.Get("file"), &file)
	if file.String("content") != "two" || number(file, "totalLines", 0) != 4 {
		t.Fatal(string(first))
	}
	second := call(t, s, o, "Read", in)
	_ = json.Unmarshal(second, &result)
	if result.String("type") != "file_unchanged" {
		t.Fatalf("dedup %s", second)
	}
	_ = os.WriteFile(p, []byte("changed\n"), 0600)
	future := time.Now().Add(time.Second)
	_ = os.Chtimes(p, future, future)
	e := o.Run(context.Background(), func(scope *cooperative.Scope) error {
		return s.Validate(context.Background(), "Edit", input(`{"file_path":"range","old_string":"changed","new_string":"bad"}`), scope)
	})
	if e == nil || !strings.Contains(e.Error(), "modified since read") {
		t.Fatalf("stale edit admitted: %v", e)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "changed\n" {
		t.Fatal("stale error changed file")
	}
}
func TestEditUniqueQuoteIndentationAndReplaceAll(t *testing.T) {
	s, o, home, _ := fixture(t)
	p := filepath.Join(home, "quotes")
	_ = os.WriteFile(p, []byte("\tmessage = “old”\n\tx = 1\n\tx = 1\n"), 0600)
	call(t, s, o, "Read", input(`{"file_path":"quotes"}`))
	call(t, s, o, "Edit", input(`{"file_path":"quotes","old_string":"    message = \"old\"","new_string":"    message = \"new\""}`))
	b, _ := os.ReadFile(p)
	if !strings.HasPrefix(string(b), "\tmessage = “new”") {
		t.Fatalf("typography %q", b)
	}
	e := o.Run(context.Background(), func(scope *cooperative.Scope) error {
		return s.Validate(context.Background(), "Edit", input(`{"file_path":"quotes","old_string":"x = 1","new_string":"x = 2"}`), scope)
	})
	if e == nil || !strings.Contains(e.Error(), "Found 2 matches") {
		t.Fatalf("ambiguous %v", e)
	}
	call(t, s, o, "Edit", input(`{"file_path":"quotes","old_string":"x = 1","new_string":"x = 2","replace_all":true}`))
	b, _ = os.ReadFile(p)
	if strings.Count(string(b), "x = 2") != 2 {
		t.Fatalf("replacement %q", b)
	}
}
func TestNativeShellRunsInOwnedWorkspaceAndPreservesFailure(t *testing.T) {
	s, o, home, _ := fixture(t)
	data := call(t, s, o, "Bash", input(`{"command":"printf shell-ok; printf error >&2"}`))
	var result protocol.JSONObject
	_ = json.Unmarshal(data, &result)
	if !strings.Contains(result.String("stdout"), "shell-ok") {
		t.Fatalf("shell %s", data)
	}
	call(t, s, o, "Bash", input(`{"command":"printf data > shell-created.txt"}`))
	b, e := os.ReadFile(filepath.Join(home, "shell-created.txt"))
	if e != nil || string(b) != "data" {
		t.Fatalf("side effect %q %v", b, e)
	}
}
func TestActualRipgrepGlobAndGrepPagination(t *testing.T) {
	s, o, home, _ := fixture(t)
	if _, e := s.executable("rg"); e != nil {
		t.Skip("ripgrep unavailable in test PATH")
	}
	for _, name := range []string{"a.go", "b.go"} {
		_ = os.WriteFile(filepath.Join(home, name), []byte("match\nmatch\n"), 0600)
	}
	glob := call(t, s, o, "Glob", input(`{"pattern":"*.go"}`))
	var result protocol.JSONObject
	_ = json.Unmarshal(glob, &result)
	if number(result, "numFiles", 0) != 2 {
		t.Fatalf("glob %s", glob)
	}
	grep := call(t, s, o, "Grep", input(`{"pattern":"match","output_mode":"content","head_limit":1}`))
	_ = json.Unmarshal(grep, &result)
	if number(result, "numLines", 0) != 1 || number(result, "appliedLimit", 0) != 1 {
		t.Fatalf("grep %s", grep)
	}
	block, e := MapResult("Grep", grep, quote("id"))
	if e != nil || !strings.Contains(block.String("content"), "pagination = limit: 1") {
		t.Fatalf("mapped %s %v", raw(block), e)
	}
}
func TestReadDenySymlinkAndAskOutsideWorkingDirectory(t *testing.T) {
	s, o, home, permissions := fixture(t)
	outside := t.TempDir()
	_ = os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0600)
	_ = os.Symlink(outside, filepath.Join(home, "link"))
	permissions.Set("mode", quote("default"))
	e := o.Run(context.Background(), func(scope *cooperative.Scope) error {
		behavior, e := s.Permission(scope, "Read", input(`{"file_path":"link/secret"}`))
		if e != nil {
			return e
		}
		if behavior != "ask" {
			return fmt.Errorf("symlink escape decision %s", behavior)
		}
		permissions.Set("alwaysDenyRules", raw(map[string][]string{"projectSettings": {"Read(**/secret)"}}))
		behavior, e = s.Permission(scope, "Read", input(`{"file_path":"link/secret"}`))
		if e != nil {
			return e
		}
		if behavior != "deny" {
			return fmt.Errorf("deny rule decision %s", behavior)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}

func TestNativePermissionBypassIgnoresAskAndStillEnforcesDeny(t *testing.T) {
	s, o, home, permissions := fixture(t)
	permissions.Set("alwaysAskRules", raw(map[string][]string{"projectSettings": {"Read", "Read(**/secret)", "Edit", "Agent", "Bash"}}))
	err := o.Run(context.Background(), func(scope *cooperative.Scope) error {
		for _, name := range []string{"Read", "Edit", "Agent", "Bash"} {
			in := input(`{"file_path":"secret","command":"pwd"}`)
			behavior, e := s.Permission(scope, name, in)
			if e != nil || behavior != "allow" {
				return fmt.Errorf("bypass %s=%s: %v", name, behavior, e)
			}
		}
		permissions.Set("alwaysDenyRules", raw(map[string][]string{"projectSettings": {"Read(**/secret)"}}))
		in := input(`{"file_path":"secret"}`)
		decision, e := s.PermissionDecision(scope, "Read", &in)
		if e != nil || decision.Behavior != "deny" || string(decision.Message) != string(raw("Permission to read "+filepath.Join(home, "secret")+" has been denied.")) {
			return fmt.Errorf("filesystem denial %+v: %v", decision, e)
		}
		permissions.Set("alwaysDenyRules", raw(map[string][]string{"projectSettings": {"Read"}}))
		decision, e = s.PermissionDecision(scope, "Read", &in)
		if e != nil || decision.Behavior != "deny" || string(decision.Message) != string(raw("Permission to use Read has been denied.")) {
			return fmt.Errorf("whole denial %+v: %v", decision, e)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
