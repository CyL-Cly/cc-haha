package filehistorycopy

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type testSink struct {
	binding *ownerasync.NativeBinding
	owner   *cooperative.Owner
	records []*protocol.JSONObject
	held    bool
	future  *ownerasync.OwnerFuture[struct{}]
	fault   error
}

func (s *testSink) NativeCopyBinding() *ownerasync.NativeBinding { return s.binding }
func (s *testSink) BeginFileHistorySnapshot(scope *cooperative.Scope, _ json.RawMessage, v *protocol.JSONObject, _ bool) (*ownerasync.OwnerFuture[struct{}], error) {
	s.records = append(s.records, v)
	f, _ := ownerasync.NewOwnerFuture[struct{}](s.owner)
	s.future = f
	if !s.held {
		if s.fault != nil {
			_ = f.Reject(scope, s.fault)
		} else {
			_ = f.Resolve(scope, struct{}{})
		}
	}
	return f, nil
}

type ioGate struct {
	OSFiles
	mkdirEntered, mkdirRelease chan struct{}
	once                       sync.Once
	mkdirError, linkError      error
}

func (f *ioGate) MkdirAll(p string) error {
	if f.mkdirEntered != nil {
		f.once.Do(func() { close(f.mkdirEntered); <-f.mkdirRelease })
	}
	if f.mkdirError != nil {
		return f.mkdirError
	}
	return f.OSFiles.MkdirAll(p)
}
func (f *ioGate) Link(a, b string) error {
	if f.linkError != nil {
		return f.linkError
	}
	return f.OSFiles.Link(a, b)
}
func obj(raw string) *protocol.JSONObject {
	var v protocol.JSONObject
	if e := json.Unmarshal([]byte(raw), &v); e != nil {
		panic(e)
	}
	return &v
}
func input(backups string) *Log {
	return &Log{Messages: []*protocol.JSONObject{obj(`{"sessionId":"A"}`)}, Snapshots: &SnapshotArray{Items: []*protocol.JSONObject{obj(`{"messageId":"original","trackedFileBackups":` + backups + `}`)}}}
}
func setup(t *testing.T, files Files) (*cooperative.Owner, *Service, *testSink, *ownerasync.NativeReferences, string, *[]string, *[]protocol.JSONObject) {
	t.Helper()
	owner := cooperative.New()
	t.Cleanup(owner.Close)
	refs := &ownerasync.NativeReferences{}
	binding, _ := refs.Bind(owner)
	sink := &testSink{binding: binding, owner: owner}
	home := t.TempDir()
	errs := []string{}
	events := []protocol.JSONObject{}
	s, e := New(Options{Owner: owner, Binding: binding, Sink: sink, Files: files, Environment: func(k string) string {
		if k == "CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING" {
			return "1"
		}
		return ""
	}, NonInteractive: func() bool { return true }, ConfigHome: func() string { return home }, SessionID: func() string { return "B" }, OnError: func(e error) { errs = append(errs, e.Error()) }, Debug: func(string) {}, Event: func(_ string, fields protocol.JSONObject) { events = append(events, fields) }})
	if e != nil {
		t.Fatal(e)
	}
	return owner, s, sink, refs, home, &errs, &events
}
func run(t *testing.T, o *cooperative.Owner, s *Service, l *Log) error {
	t.Helper()
	return o.Run(context.Background(), func(scope *cooperative.Scope) error {
		f, e := s.Begin(scope, l)
		if e != nil {
			return e
		}
		_, e = f.Await(scope)
		return e
	})
}

// This accepted unit double is a counterexample to generic provenance claims:
// a public newly bound same-owner ledger and matching Sink pass New. The actual
// Controller separately supplies its real pre-activation PendingWriter factory.
func TestFileHistorySameOwnerIdentityDoesNotCertifyFactoryProvenance(t *testing.T) {
	owner, service, sink, _, _, _, _ := setup(t, OSFiles{})
	if !service.options.Binding.Same(sink.NativeCopyBinding()) || !service.options.Binding.Owns(owner) {
		t.Fatal("supplied same-owner identity not retained")
	}
}
func TestFileHistoryRealLinkAndExistingDestination(t *testing.T) {
	o, s, sink, _, home, errs, _ := setup(t, OSFiles{})
	a := filepath.Join(home, "file-history", "A")
	_ = os.MkdirAll(a, 0700)
	_ = os.WriteFile(filepath.Join(a, "one"), []byte("original\x00bytes"), 0640)
	if e := run(t, o, s, input(`{"x":{"backupFileName":"one"},"new":{"backupFileName":null}}`)); e != nil {
		t.Fatal(e)
	}
	b := filepath.Join(home, "file-history", "B", "one")
	original, _ := os.Stat(filepath.Join(a, "one"))
	copied, _ := os.Stat(b)
	if !os.SameFile(original, copied) || copied.Mode().Perm() != 0640 || len(sink.records) != 1 || len(*errs) != 0 {
		t.Fatal("actual link/sink/error differs")
	}
	_ = os.Remove(b)
	_ = os.WriteFile(b, []byte("existing"), 0700)
	if e := run(t, o, s, input(`{"x":{"backupFileName":"one"}}`)); e != nil {
		t.Fatal(e)
	}
	bytes, _ := os.ReadFile(b)
	st, _ := os.Stat(b)
	if string(bytes) != "existing" || st.Mode().Perm() != 0700 {
		t.Fatal("EEXIST overwritten")
	}
}
func TestFileHistoryParallelAllSettledMissingUnsafeAndFallback(t *testing.T) {
	for _, kind := range []string{"missing", "unsafe", "directory", "fallback"} {
		t.Run(kind, func(t *testing.T) {
			files := &ioGate{}
			if kind == "fallback" {
				files.linkError = os.ErrPermission
			}
			o, s, sink, _, home, errs, events := setup(t, files)
			a := filepath.Join(home, "file-history", "A")
			_ = os.MkdirAll(a, 0700)
			_ = os.WriteFile(filepath.Join(a, "one"), []byte("one"), 0600)
			name := "missing"
			if kind == "unsafe" {
				name = "../escape"
			}
			if kind == "directory" {
				name = "dir"
				_ = os.Mkdir(filepath.Join(a, name), 0700)
			}
			if kind == "fallback" {
				name = "one"
			}
			l := input(`{"x":{"backupFileName":` + string(quoted(name)) + `}}`)
			l.Snapshots.Items = append(l.Snapshots.Items, obj(`{"messageId":"second","trackedFileBackups":{"x":{"backupFileName":"one"}}}`))
			if e := run(t, o, s, l); e != nil {
				t.Fatal(e)
			}
			wanted := 1
			if kind == "fallback" {
				wanted = 2
			}
			if len(sink.records) != wanted {
				t.Fatal("failed snapshot leaked or allSettled skipped successful one", len(sink.records))
			}
			if kind != "fallback" && (len(*events) != 1 || string((*events)[0].Get("failedSnapshots")) != "1") {
				t.Fatal("allSettled failure count", *events)
			}
			if kind == "unsafe" && len(*errs) != 0 {
				t.Fatal("unsafe inner rejection logged unexpectedly")
			}
			if kind == "directory" && len(*errs) != 2 {
				t.Fatal("link/copy fallback diagnostics", *errs)
			}
			if kind == "fallback" {
				b, _ := os.ReadFile(filepath.Join(home, "file-history", "B", "one"))
				if string(b) != "one" {
					t.Fatal("actual fallback copy not performed")
				}
			}
		})
	}
}
func TestFileHistoryCapturedArrayAndLiveSnapshotAfterMkdir(t *testing.T) {
	files := &ioGate{mkdirEntered: make(chan struct{}), mkdirRelease: make(chan struct{})}
	o, s, sink, _, home, _, _ := setup(t, files)
	a := filepath.Join(home, "file-history", "A")
	_ = os.MkdirAll(a, 0700)
	_ = os.WriteFile(filepath.Join(a, "one"), []byte("one"), 0600)
	l := input(`{}`)
	captured := l.Snapshots
	var f *ownerasync.OwnerFuture[struct{}]
	r, e := o.Submit(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error { var e error; f, e = s.Begin(scope, l); return e })
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Wait(); e != nil {
		t.Fatal(e)
	}
	<-files.mkdirEntered
	r, e = o.Submit(context.Background(), cooperative.Read, func(*cooperative.Scope) error {
		captured.Items[0].Set("trackedFileBackups", json.RawMessage(`{"added":{"backupFileName":"one"}}`))
		l.Snapshots = &SnapshotArray{Items: []*protocol.JSONObject{obj(`{"messageId":"replacement","trackedFileBackups":{}}`)}}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Wait(); e != nil {
		t.Fatal(e)
	}
	close(files.mkdirRelease)
	if e = o.Run(context.Background(), func(scope *cooperative.Scope) error { _, e := f.Await(scope); return e }); e != nil {
		t.Fatal(e)
	}
	if len(sink.records) != 1 || sink.records[0] != captured.Items[0] {
		t.Fatal("snapshot cloned/replaced before actual mkdir await")
	}
	if _, e := os.Stat(filepath.Join(home, "file-history", "B", "one")); e != nil {
		t.Fatal(e)
	}
}
func TestFileHistoryCopyDoesNotAwaitDetachedRecord(t *testing.T) {
	o, s, sink, refs, _, errs, _ := setup(t, OSFiles{})
	sink.held = true
	if e := run(t, o, s, input(`{}`)); e != nil {
		t.Fatal(e)
	}
	select {
	case <-sink.future.Done():
		t.Fatal("copy settled detached record")
	default:
	}
	if n, _ := refs.Snapshot(); n != 0 {
		t.Fatal("pure record kept native ref", n)
	}
	failure := errors.New("original record rejection")
	if e := o.Run(context.Background(), func(scope *cooperative.Scope) error { return sink.future.Reject(scope, failure) }); e != nil {
		t.Fatal(e)
	}
	r, e := o.Submit(context.Background(), cooperative.Read, func(*cooperative.Scope) error {
		if len(*errs) != 1 || (*errs)[0] != "FileHistory: Failed to record copy backup snapshot" {
			t.Error(*errs)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	_ = r.Wait()
}
func TestFileHistoryEarlyReturnsAndNativeMkdirCatch(t *testing.T) {
	cases := []string{"disabled", "absent", "empty", "same", "missingSID", "mkdir"}
	for _, kind := range cases {
		t.Run(kind, func(t *testing.T) {
			files := &ioGate{}
			if kind == "mkdir" {
				files.mkdirError = os.ErrPermission
			}
			o, s, sink, _, _, errs, _ := setup(t, files)
			l := input(`{}`)
			switch kind {
			case "disabled":
				s.options.Environment = func(string) string { return "0" }
			case "absent":
				l.Snapshots = nil
			case "empty":
				l.Messages = nil
			case "same":
				l.Messages[0].Set("sessionId", quoted("B"))
			case "missingSID":
				l.Messages[0].Delete("sessionId")
			}
			if e := run(t, o, s, l); e != nil {
				t.Fatal(e)
			}
			if len(sink.records) != 0 {
				t.Fatal("early-return effects")
			}
			want := 0
			if kind == "mkdir" || kind == "missingSID" {
				want = 1
			}
			if len(*errs) != want {
				t.Fatal("catch diagnostic", *errs)
			}
		})
	}
}
func TestFileHistoryNativeOwnerCloseKeepsNoBusinessWorker(t *testing.T) {
	files := &ioGate{mkdirEntered: make(chan struct{}), mkdirRelease: make(chan struct{})}
	o, s, sink, refs, _, errs, _ := setup(t, files)
	var f *ownerasync.OwnerFuture[struct{}]
	r, e := o.Submit(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error { var e error; f, e = s.Begin(scope, input(`{}`)); return e })
	if e != nil {
		t.Fatal(e)
	}
	_ = r.Wait()
	<-files.mkdirEntered
	o.Close()
	if _, e := f.Wait(context.Background()); !errors.Is(e, cooperative.ErrClosed) {
		t.Fatal(e)
	}
	close(files.mkdirRelease)
	// Wait the exact acquired physical receipt; no timeout/sleep/event phase input.
	_, idle := refs.Snapshot()
	if idle != nil {
		<-idle
	}
	if len(sink.records) != 0 || len(*errs) != 0 {
		t.Fatal("business callback escaped closed owner")
	}
}
func TestFileHistoryTypedCapsAndConstructorBinding(t *testing.T) {
	o, s, sink, _, _, _, _ := setup(t, OSFiles{})
	foreign := cooperative.New()
	defer foreign.Close()
	other := &ownerasync.NativeReferences{}
	binding, _ := other.Bind(o)
	for _, edit := range []func(*Options){func(v *Options) { v.Binding = &ownerasync.NativeBinding{} }, func(v *Options) { v.Binding = binding }, func(v *Options) { v.Owner = foreign }, func(v *Options) { v.Files = nil }} {
		opts := s.options
		edit(&opts)
		if _, e := New(opts); e == nil {
			t.Fatal("invalid constructor admitted")
		}
	}
	_ = sink
	for _, raw := range []string{`{}`, `{"messages":123}`, `{"messages":[null]}`, `{"messages":[],"fileHistorySnapshots":1}`, `{"messages":[],"fileHistorySnapshots":[null]}`} {
		if _, e := DecodeLog(*obj(raw)); e == nil {
			t.Fatal("unknown raw admitted", raw)
		}
	}
	for _, l := range []*Log{nil, input(`{"x":{"backupFileName":3}}`), input(`{"x":{"backupFileName":"\ud800"}}`), {Messages: []*protocol.JSONObject{obj(`{"sessionId":"A"}`)}, Snapshots: &SnapshotArray{Items: []*protocol.JSONObject{obj(`{"trackedFileBackups":null}`)}}}} {
		e := run(t, o, s, l)
		var cap *CapabilityError
		if !errors.As(e, &cap) {
			t.Fatal("explicit cap missing", e)
		}
	}
	s.options.NonInteractive = func() bool { return false }
	e := run(t, o, s, input(`{}`))
	var cap *CapabilityError
	if !errors.As(e, &cap) || !strings.Contains(cap.Boundary, "interactive") {
		t.Fatal(e)
	}
	if e = foreign.Run(context.Background(), func(scope *cooperative.Scope) error { _, e := s.Begin(scope, input(`{}`)); return e }); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if _, e = pathString([]byte(`"a\\b"`), "backup file name"); e == nil {
		t.Fatal("safe path missing")
	}
	var _ fs.FileInfo
}

type heldFailedLink struct {
	OSFiles
	entered, release chan struct{}
	once             sync.Once
}

func (f *heldFailedLink) Link(_, _ string) error {
	f.once.Do(func() { close(f.entered) })
	<-f.release
	return os.ErrNotExist
}
func TestFileHistoryFailureEventReadsCapturedArrayLiveLength(t *testing.T) {
	files := &heldFailedLink{entered: make(chan struct{}), release: make(chan struct{})}
	owner, s, _, _, _, _, events := setup(t, files)
	log := input(`{"x":{"backupFileName":"missing"}}`)
	captured := log.Snapshots
	var result *ownerasync.OwnerFuture[struct{}]
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error { var e error; result, e = s.Begin(scope, log); return e }); e != nil {
		t.Fatal(e)
	}
	<-files.entered
	if e := owner.Run(context.Background(), func(*cooperative.Scope) error {
		captured.Items = append(captured.Items, obj(`{"messageId":"late","trackedFileBackups":{}}`))
		log.Snapshots = &SnapshotArray{}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	close(files.release)
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error { _, e := result.Await(scope); return e }); e != nil {
		t.Fatal(e)
	}
	if len(*events) != 1 || string((*events)[0].Get("numSnapshots")) != "2" {
		t.Fatalf("event froze mkdir-time length instead of captured live array: %v", *events)
	}
}
