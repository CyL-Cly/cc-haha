package builtinwrite

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteBorrowsActualPendingWriterBindingAndScope(t *testing.T) {
	s, o, _, _, home := fixture(t)
	settings, e := clisettings.NewState(func() float64 { return 0 })
	if e != nil {
		t.Fatal(e)
	}
	empty := protocol.NewJSONObject()
	settings.SetSessionSettingsCache(&clisettings.ParsedSettings{Settings: &empty})
	writer, e := sessioncontroller.NewWriter(sessioncontroller.WriterOptions{ConfigDir: filepath.Join(home, "config"), CWD: home, SessionID: "own-finite", Version: "test", Environment: map[string]string{}, SessionPersistenceDisabled: true, SettingsState: settings, Files: sessioncontroller.OSFiles{}, Branch: func() (string, error) { return "", nil }, OnError: func(e error) { t.Error(e) }, Schedule: sessioncontroller.RealSchedule, Now: time.Now})
	if e != nil {
		t.Fatal(e)
	}
	pending, e := sessioncontroller.NewPendingWriter(writer, o)
	if e != nil {
		t.Fatal(e)
	}
	options := s.options
	options.NativeBinding = pending.NativeCopyBinding()
	actual, e := New(options)
	if e != nil {
		t.Fatal(e)
	}
	if !actual.options.NativeBinding.Same(pending.NativeCopyBinding()) {
		t.Fatal("second native ledger created")
	}
	run(t, o, func(scope *cooperative.Scope) error {
		in := input("actual-writer-binding", "shared")
		if e := actual.Validate(context.Background(), in, scope); e != nil {
			return e
		}
		_, e := actual.Call(context.Background(), in, scope)
		return e
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e = pending.WaitRuntimeNative(ctx); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(home, "actual-writer-binding"))
	if e != nil || string(b) != "shared" {
		t.Fatalf("borrowed actual file %q %v", b, e)
	}
}
