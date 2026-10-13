package filehistory_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/filehistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionrewind"
)

func TestNativeFileHistoryRewindOriginalCheckpointAndMissingNewFile(t *testing.T) {
	f := newLifetimeFixture(t, nil)
	ctx := context.Background()
	created := filepath.Join(f.work, "created-after.txt")
	if e := f.owner.Run(ctx, func(scope *cooperative.Scope) error {
		if e := f.service.Make(scope, raw("original-user")); e != nil {
			return e
		}
		if e := f.service.Track(scope, f.path, raw("assistant-write")); e != nil {
			return e
		}
		if e := os.WriteFile(f.path, []byte("edited physical bytes"), 0600); e != nil {
			return e
		}
		if e := f.service.Complete(scope, raw("original-user")); e != nil {
			return e
		}
		if e := f.service.Make(scope, raw("second-user")); e != nil {
			return e
		}
		if e := f.service.Track(scope, created, raw("assistant-new-write")); e != nil {
			return e
		}
		if e := os.WriteFile(created, []byte("new physical bytes"), 0600); e != nil {
			return e
		}
		if e := f.service.Complete(scope, raw("second-user")); e != nil {
			return e
		}
		before, _ := f.state.MarshalJSON()
		preview, e := f.service.PreviewRestore(scope, "original-user")
		if e != nil || !preview.Available {
			t.Fatal(preview, e)
		}
		if e := f.service.Rewind(ctx, scope, "original-user"); e != nil {
			return e
		}
		after, _ := f.state.MarshalJSON()
		if string(before) != string(after) {
			t.Fatal("rewind mutated canonical checkpoint history")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	bytes, e := os.ReadFile(f.path)
	if e != nil || string(bytes) != "real before" {
		t.Fatal(string(bytes), e)
	}
	if _, e := os.Stat(created); !os.IsNotExist(e) {
		t.Fatal("created-after file not removed", e)
	}
	if e := f.owner.Run(ctx, func(scope *cooperative.Scope) error { return f.service.Rewind(ctx, scope, "unknown") }); e == nil {
		t.Fatal("unknown checkpoint accepted")
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if e := foreign.Run(ctx, func(scope *cooperative.Scope) error { return f.service.Rewind(ctx, scope, "original-user") }); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal("foreign rewind accepted", e)
	}
}

type failPartialWrite struct {
	sessionrewind.NativeFiles
	failed bool
}

func (f *failPartialWrite) WriteState(ctx context.Context, path string, state sessionrewind.FileState) error {
	if !f.failed {
		f.failed = true
		if e := os.WriteFile(path, []byte("partial IO"), 0600); e != nil {
			return e
		}
		return errors.New("native write failed after truncation")
	}
	return f.NativeFiles.WriteState(ctx, path, state)
}
func TestNativeFileHistoryRewindRollsBackPartiallyWrittenFile(t *testing.T) {
	f := newLifetimeFixture(t, func(files sessionrewind.NativeFiles) filehistory.Files { return &failPartialWrite{NativeFiles: files} })
	ctx := context.Background()
	if e := f.owner.Run(ctx, func(scope *cooperative.Scope) error {
		if e := f.service.Make(scope, raw("original")); e != nil {
			return e
		}
		if e := f.service.Track(scope, f.path, raw("assistant")); e != nil {
			return e
		}
		if e := os.WriteFile(f.path, []byte("keep current after failed rewind"), 0600); e != nil {
			return e
		}
		return f.service.Rewind(ctx, scope, "original")
	}); e == nil {
		t.Fatal("expected native IO failure")
	}
	data, e := os.ReadFile(f.path)
	if e != nil || string(data) != "keep current after failed rewind" {
		t.Fatal("partially mutated file survived rollback", string(data), e)
	}
}
