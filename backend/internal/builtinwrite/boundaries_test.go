package builtinwrite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func expectCapability(err error, boundary string) error {
	var c *CapabilityError
	if !errors.As(err, &c) || !strings.Contains(c.Boundary, boundary) {
		return fmt.Errorf("expected capability %q got %v", boundary, err)
	}
	return nil
}
func TestWriteValidationActualReadPartialAndStaleness(t *testing.T) {
	s, o, _, f, home := fixture(t)
	path := filepath.Join(home, "exists")
	if e := os.WriteFile(path, []byte("old"), 0600); e != nil {
		t.Fatal(e)
	}
	in := input(path, "new")
	run(t, o, func(scope *cooperative.Scope) error {
		e := s.Validate(context.Background(), in, scope)
		var v *ValidationError
		if !errors.As(e, &v) || v.ErrorCode != 2 {
			return fmt.Errorf("unread %v", e)
		}
		if e = s.ReadForCache(scope, quoted(path)); e != nil {
			return e
		}
		state, _, e := s.options.Cache.Get(scope, path)
		if e != nil {
			return e
		}
		state.PartialView = true
		if e = s.options.Cache.Set(scope, path, state); e != nil {
			return e
		}
		e = s.Validate(context.Background(), in, scope)
		if !errors.As(e, &v) || v.ErrorCode != 2 {
			return fmt.Errorf("partial %v", e)
		}
		state.PartialView = false
		if e = s.options.Cache.Set(scope, path, state); e != nil {
			return e
		}
		if e = s.Validate(context.Background(), in, scope); e != nil {
			return e
		}
		changed := time.UnixMilli(int64(state.Timestamp) + 5000)
		if e = os.Chtimes(path, changed, changed); e != nil {
			return e
		}
		e = s.Validate(context.Background(), in, scope)
		if !errors.As(e, &v) || v.ErrorCode != 3 {
			return fmt.Errorf("stale %v", e)
		}
		return nil
	})
	if len(f.events) != 0 {
		t.Fatal("validation wrote", f.events)
	}
}
func TestWriteCallChangedContentFailsBeforeReplacement(t *testing.T) {
	s, o, _, f, home := fixture(t)
	path := filepath.Join(home, "empty")
	if e := os.WriteFile(path, nil, 0600); e != nil {
		t.Fatal(e)
	}
	run(t, o, func(scope *cooperative.Scope) error {
		if e := s.ReadForCache(scope, quoted(path)); e != nil {
			return e
		}
		state, _, e := s.options.Cache.Get(scope, path)
		if e != nil {
			return e
		}
		if e = os.WriteFile(path, []byte("external"), 0600); e != nil {
			return e
		}
		now := time.UnixMilli(int64(state.Timestamp) + 5000)
		if e = os.Chtimes(path, now, now); e != nil {
			return e
		}
		_, e = s.Call(context.Background(), input(path, "overwrite"), scope)
		if e == nil || e.Error() != unexpectedlyModified {
			return fmt.Errorf("critical stale guard %v", e)
		}
		return nil
	})
	b, e := os.ReadFile(path)
	if e != nil || string(b) != "external" || len(f.events) != 0 {
		t.Fatalf("stale replacement %q %v %v", b, e, f.events)
	}
}
func TestWriteCallFullReadSameContentTimestampFallback(t *testing.T) {
	s, o, _, f, home := fixture(t)
	path := filepath.Join(home, "empty")
	if e := os.WriteFile(path, nil, 0600); e != nil {
		t.Fatal(e)
	}
	run(t, o, func(scope *cooperative.Scope) error {
		if e := s.ReadForCache(scope, quoted(path)); e != nil {
			return e
		}
		state, _, e := s.options.Cache.Get(scope, path)
		if e != nil {
			return e
		}
		now := time.UnixMilli(int64(state.Timestamp) + 5000)
		if e = os.Chtimes(path, now, now); e != nil {
			return e
		}
		_, e = s.Call(context.Background(), input(path, "same-content-fallback"), scope)
		return e
	})
	b, e := os.ReadFile(path)
	if e != nil || string(b) != "same-content-fallback" || len(f.events) != 2 {
		t.Fatalf("same content fallback %q %v %v", b, e, f.events)
	}
}
func TestWriteNonemptyUpdateUsesActualReadStateAndPatch(t *testing.T) {
	s, o, _, f, home := fixture(t)
	path := filepath.Join(home, "nonempty")
	if e := os.WriteFile(path, []byte("old\r\n"), 0600); e != nil {
		t.Fatal(e)
	}
	run(t, o, func(scope *cooperative.Scope) error {
		if e := s.ReadForCache(scope, quoted(path)); e != nil {
			return e
		}
		if e := s.Validate(context.Background(), input(path, "new"), scope); e != nil {
			return e
		}
		data, e := s.Call(context.Background(), input(path, "new"), scope)
		if e != nil {
			return e
		}
		var result protocol.JSONObject
		if e = json.Unmarshal(data, &result); e != nil {
			return e
		}
		if result.String("type") != "update" || result.String("originalFile") != "old\n" || !bytes.Contains(result.Get("structuredPatch"), []byte(`"-old"`)) {
			return fmt.Errorf("actual update result %s", data)
		}
		added, removed, e := s.LineChanges(scope)
		if e != nil {
			return e
		}
		if added != 1 || removed != 1 {
			return fmt.Errorf("line counts %d %d", added, removed)
		}
		return nil
	})
	b, e := os.ReadFile(path)
	if e != nil || string(b) != "new" || len(f.events) != 2 {
		t.Fatalf("native update wrote %q %v %v", b, e, f.events)
	}
}

type fallbackFiles struct {
	*observedFiles
	renameFailure, firstFailure, secondFailure error
	writes, unlinks                            int
}

func (f *fallbackFiles) WriteFlush(p string, b []byte) error {
	f.writes++
	if f.writes == 1 && f.firstFailure != nil {
		if e := f.observedFiles.WriteFlush(p, b); e != nil {
			return e
		}
		return f.firstFailure
	}
	if f.writes == 2 && f.secondFailure != nil {
		return f.secondFailure
	}
	return f.observedFiles.WriteFlush(p, b)
}
func (f *fallbackFiles) Rename(a, b string) error {
	if f.renameFailure != nil {
		return f.renameFailure
	}
	return f.observedFiles.Rename(a, b)
}
func (f *fallbackFiles) Unlink(p string) error { f.unlinks++; return f.OSFiles.Unlink(p) }
func TestWriteAtomicRenameFallbackWritesOnceToEachTargetAndCleansTemp(t *testing.T) {
	s, o, _, base, home := fixture(t)
	f := &fallbackFiles{observedFiles: base, renameFailure: fs.ErrPermission}
	s.options.Files = f
	path := filepath.Join(home, "fallback")
	run(t, o, func(scope *cooperative.Scope) error {
		_, e := s.Call(context.Background(), input(path, "real fallback"), scope)
		return e
	})
	b, e := os.ReadFile(path)
	if e != nil || string(b) != "real fallback" || f.writes != 2 || f.unlinks != 1 {
		t.Fatalf("fallback counts writes%d unlink%d bytes%q %v", f.writes, f.unlinks, b, e)
	}
	temps, _ := filepath.Glob(path + ".tmp.*")
	if len(temps) != 0 {
		t.Fatal("temp leaked", temps)
	}
}
func TestWriteAtomicFailureKeepsCacheAndFallbackErrorPriority(t *testing.T) {
	s, o, _, base, home := fixture(t)
	path := filepath.Join(home, "empty")
	if e := os.WriteFile(path, nil, 0640); e != nil {
		t.Fatal(e)
	}
	first := errors.New("actual temp write completed then injected fsync failure")
	second := errors.New("actual fallback admission denied")
	f := &fallbackFiles{observedFiles: base, firstFailure: first, secondFailure: second}
	s.options.Files = f
	run(t, o, func(scope *cooperative.Scope) error {
		if e := s.ReadForCache(scope, quoted(path)); e != nil {
			return e
		}
		before, _, e := s.options.Cache.Get(scope, path)
		if e != nil {
			return e
		}
		_, e = s.Call(context.Background(), input(path, "not committed"), scope)
		if e != second {
			return fmt.Errorf("fallback precedence %v", e)
		}
		after, _, e := s.options.Cache.Get(scope, path)
		if e != nil {
			return e
		}
		if !bytes.Equal(before.Content, after.Content) || before.Timestamp != after.Timestamp {
			return errors.New("failed write changed cache")
		}
		return nil
	})
	b, e := os.ReadFile(path)
	temps, _ := filepath.Glob(path + ".tmp.*")
	if e != nil || len(b) != 0 || f.writes != 2 || f.unlinks != 1 || len(temps) != 0 {
		t.Fatalf("failure native state %q %v writes%d unlink%d temps%v", b, e, f.writes, f.unlinks, temps)
	}
}
func TestWriteLeafSymlinkPreservedWithActualTargetBytes(t *testing.T) {
	s, o, _, _, home := fixture(t)
	target := filepath.Join(home, "target")
	link := filepath.Join(home, "link")
	if e := os.WriteFile(target, nil, 0640); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink("target", link); e != nil {
		t.Fatal(e)
	}
	run(t, o, func(scope *cooperative.Scope) error {
		if e := s.ReadForCache(scope, quoted(link)); e != nil {
			return e
		}
		_, e := s.Call(context.Background(), input(link, "symlink bytes"), scope)
		return e
	})
	value, e := os.Readlink(link)
	b, failure := os.ReadFile(target)
	if e != nil || value != "target" || failure != nil || string(b) != "symlink bytes" {
		t.Fatalf("symlink %s %v bytes %q %v", value, e, b, failure)
	}
}
func TestWriteDynamicSkillGuardRemainsOnRepeatedCall(t *testing.T) {
	s, o, _, f, home := fixture(t)
	if e := os.MkdirAll(filepath.Join(home, "nested/.agents/skills"), 0700); e != nil {
		t.Fatal(e)
	}
	run(t, o, func(scope *cooperative.Scope) error {
		for i := 0; i < 2; i++ {
			_, e := s.Call(context.Background(), input("nested/file", "x"), scope)
			if e = expectCapability(e, "dynamic skill"); e != nil {
				return e
			}
		}
		return nil
	})
	if len(f.events) != 0 {
		t.Fatal("skill guard allowed retry", f.events)
	}
}
func TestWriteEnabledDependenciesRefuseBeforeNativeWrite(t *testing.T) {
	cases := []struct {
		name     string
		host     Host
		env      map[string]string
		boundary string
	}{{"history", Host{NonInteractive: true}, map[string]string{"CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING": "true"}, "fileHistoryTrackEdit"}, {"hooks", Host{NonInteractive: true, ActiveHooks: true}, nil, "hooks"}, {"team", Host{NonInteractive: true, TeamMemoryEnabled: true}, nil, "team-memory"}, {"IDE", Host{NonInteractive: true, DiagnosticClientConnected: true}, nil, "IDE"}, {"conditional", Host{NonInteractive: true, ConditionalSkillsPresent: true}, nil, "conditional"}, {"LSP", Host{NonInteractive: true, LSPEnabled: true}, nil, "LSP"}, {"VSCode", Host{NonInteractive: true, VSCodeClientConnected: true}, map[string]string{"USER_TYPE": "ant"}, "VSCode"}, {"remote", Host{NonInteractive: true, RemoteDiffEnabled: true}, map[string]string{"CLAUDE_CODE_REMOTE": "1"}, "remote"}, {"analytics", Host{NonInteractive: true, AnalyticsEnabled: true}, nil, "analytics"}, {"LOC", Host{NonInteractive: true, LOCCounterEnabled: true}, nil, "LOC"}, {"interactive", Host{}, nil, "interactive"}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, o, _, f, home := fixture(t)
			s.options.HostState = func(*cooperative.Scope) (Host, error) { return c.host, nil }
			s.options.Environment = func(k string) string { return c.env[k] }
			run(t, o, func(scope *cooperative.Scope) error {
				_, e := s.Call(context.Background(), input("guarded", "x"), scope)
				return expectCapability(e, c.boundary)
			})
			_, e := os.Stat(filepath.Join(home, "guarded"))
			if !errors.Is(e, fs.ErrNotExist) || len(f.events) != 0 {
				t.Fatalf("cap write %v %v", e, f.events)
			}
		})
	}
}
func TestWriteDisabledHistoryAndLSPSourceEnvBranches(t *testing.T) {
	s, o, _, _, home := fixture(t)
	s.options.HostState = func(*cooperative.Scope) (Host, error) { return Host{NonInteractive: true, LSPEnabled: true}, nil }
	env := map[string]string{"CLAUDE_CODE_SIMPLE": "yes", "CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING": "1", "CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING": "on"}
	s.options.Environment = func(k string) string { return env[k] }
	run(t, o, func(scope *cooperative.Scope) error {
		_, e := s.Call(context.Background(), input("simple-direct-private-engine", "x"), scope)
		return e
	})
	if b, e := os.ReadFile(filepath.Join(home, "simple-direct-private-engine")); e != nil || string(b) != "x" {
		t.Fatalf("env branch %q %v", b, e)
	} // direct private engine does not register Write in Bare
}
func TestWritePermissionLiveDenyAndCatalogBareGate(t *testing.T) {
	s, o, _, f, home := fixture(t)
	ctx := protocol.NewJSONObject(protocol.JSONField{Name: "mode", Value: quoted("bypassPermissions")}, protocol.JSONField{Name: "alwaysDenyRules", Value: json.RawMessage(`{}`)})
	s.options.PermissionContext = func(*cooperative.Scope) (protocol.JSONObject, error) { return ctx.Clone(), nil }
	run(t, o, func(scope *cooperative.Scope) error {
		defs, tools, e := s.Catalog(scope)
		if e != nil {
			return e
		}
		if len(defs) != 1 || len(tools) != 1 || tools[0].Name != "Write" {
			return errors.New("nonbare Write contribution missing")
		}
		schema, e := toolschema.OriginalInputJSONSchema(defs[0])
		if e != nil || !json.Valid(schema) {
			return fmt.Errorf("actual native schema %s %v", schema, e)
		}
		ctx.Set("alwaysDenyRules", json.RawMessage(`{"session":["Write"]}`))
		decision, e := s.Permission(scope, input("file", "x"))
		if e != nil || decision != "deny" {
			return fmt.Errorf("live whole deny %s %v", decision, e)
		}
		defs, tools, e = s.Catalog(scope)
		if e != nil || len(defs) != 0 || len(tools) != 0 {
			return fmt.Errorf("whole denied registration %v", e)
		}
		ctx.Set("alwaysDenyRules", json.RawMessage(`{"session":["Edit(//`+strings.TrimPrefix(home, "/")+`/blocked/**)"]}`))
		decision, e = s.Permission(scope, input("blocked/file", "x"))
		if e != nil || decision != "deny" {
			return fmt.Errorf("Edit path deny %s %v", decision, e)
		}
		e = s.Validate(context.Background(), input("blocked/file", "x"), scope)
		var v *ValidationError
		if !errors.As(e, &v) || v.ErrorCode != 1 {
			return fmt.Errorf("Edit validate %v", e)
		}
		ctx.Set("alwaysDenyRules", json.RawMessage(`{}`))
		s.options.Environment = func(k string) string {
			if k == "CLAUDE_CODE_SIMPLE" {
				return "1"
			}
			return ""
		}
		defs, tools, e = s.Catalog(scope)
		if e != nil || len(defs) != 0 || len(tools) != 0 {
			return fmt.Errorf("bare admits Write %v", e)
		}
		return nil
	})
	if len(f.events) != 0 {
		t.Fatal("permission wrote", f.events)
	}
}
func TestWritePermissionAliasDenyAndComplexRefusal(t *testing.T) {
	s, o, _, _, home := fixture(t)
	outside := filepath.Join(home, "outside")
	alias := filepath.Join(home, "alias")
	if e := os.Mkdir(outside, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, alias); e != nil {
		t.Fatal(e)
	}
	ctx := protocol.NewJSONObject(protocol.JSONField{Name: "mode", Value: quoted("bypassPermissions")}, protocol.JSONField{Name: "alwaysDenyRules", Value: json.RawMessage(`{"session":["Edit(//` + strings.TrimPrefix(outside, "/") + `/**)"]}`)})
	s.options.PermissionContext = func(*cooperative.Scope) (protocol.JSONObject, error) { return ctx.Clone(), nil }
	run(t, o, func(scope *cooperative.Scope) error {
		decision, e := s.Permission(scope, input(filepath.Join(alias, "not-yet-existing"), "x"))
		if e != nil || decision != "deny" {
			return fmt.Errorf("resolved parent deny %s %v", decision, e)
		}
		ctx.Set("alwaysDenyRules", json.RawMessage(`{"session":["Edit(**/*.env)"]}`))
		_, e = s.Permission(scope, input("file", "x"))
		if e = expectCapability(e, "ignore-pattern"); e != nil {
			return e
		}
		ctx.Set("alwaysDenyRules", json.RawMessage(`{}`))
		ctx.Set("mode", quoted("default"))
		_, e = s.Permission(scope, input("file", "x"))
		return expectCapability(e, "ask resolver")
	})
}
func TestWriteAbsolutePathTrailingSlashAndNULNeverOverwrite(t *testing.T) {
	s, o, _, f, home := fixture(t)
	path := filepath.Join(home, "file")
	if e := os.WriteFile(path, []byte("keep"), 0600); e != nil {
		t.Fatal(e)
	}
	run(t, o, func(scope *cooperative.Scope) error {
		for _, path := range []string{path + "/", path + "\x00"} {
			_, e := s.Call(context.Background(), input(path, "bad"), scope)
			if e == nil {
				return fmt.Errorf("invalid path allowed %q", path)
			}
		}
		expanded, e := s.expand(quoted("\ufeff ~/decomposed-e\u0301/ \ufeff"))
		if e != nil {
			return e
		}
		if expanded != filepath.Join(home, "decomposed-é")+"/" {
			return fmt.Errorf("NFC/home/trailing %q", expanded)
		}
		return nil
	})
	b, e := os.ReadFile(path)
	if e != nil || string(b) != "keep" || len(f.events) != 0 {
		t.Fatalf("path wrote %q %v %v", b, e, f.events)
	}
}
func TestWriteCancelledAndCrossOwnerDoNotWrite(t *testing.T) {
	s, o, _, f, _ := fixture(t)
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("caller aborted")
	cancel(cause)
	run(t, o, func(scope *cooperative.Scope) error {
		_, e := s.Call(ctx, input("file", "x"), scope)
		if e != cause {
			return fmt.Errorf("abort %v", e)
		}
		return nil
	})
	other := cooperative.New()
	defer other.Close()
	run(t, other, func(scope *cooperative.Scope) error {
		_, e := s.Call(context.Background(), input("file", "x"), scope)
		if !errors.Is(e, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign owner %v", e)
		}
		return nil
	})
	if len(f.events) != 0 {
		t.Fatal("cross owner wrote", f.events)
	}
}
func TestWriteOwnerCacheLRUBytesAndTrailingNormalization(t *testing.T) {
	s, o, _, _, _ := fixture(t)
	cache, e := NewFileStateCache(o, 2, 5)
	if e != nil {
		t.Fatal(e)
	}
	run(t, o, func(scope *cooperative.Scope) error {
		for _, key := range []string{"a", "b"} {
			if e := cache.Set(scope, key, FileState{Content: quoted("ab")}); e != nil {
				return e
			}
		}
		if _, ok, e := cache.Get(scope, "a"); e != nil || !ok {
			return fmt.Errorf("LRU get %v", e)
		}
		if e := cache.Set(scope, "c", FileState{Content: quoted("cd")}); e != nil {
			return e
		}
		if _, ok, e := cache.Get(scope, "b"); e != nil || ok {
			return errors.New("LRU eviction failed")
		}
		if e := cache.Set(scope, "a", FileState{Content: quoted("oversized")}); e != nil {
			return e
		}
		if _, ok, e := cache.Get(scope, "a"); e != nil || ok {
			return errors.New("oversized replacement not removed")
		}
		if e := cache.Set(scope, "/dir/", FileState{Content: quoted("")}); e != nil {
			return e
		}
		if _, ok, e := cache.Get(scope, "/dir"); e != nil || ok {
			return errors.New("Source trailing cache key collapsed")
		}
		n, size, e := cache.Snapshot(scope)
		if e != nil || n != 2 || size != 3 {
			return fmt.Errorf("bounded cache n%d bytes%d %v", n, size, e)
		}
		_, _, e = s.options.Cache.Get(scope, "unrelated")
		return e
	})
}
