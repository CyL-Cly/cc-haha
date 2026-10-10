package sdkpermissions

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeStartupRequiresPreparedOwnerAndProtectsItsResult(t *testing.T) {
	home := t.TempDir()
	owner := cooperative.New()
	foreign := cooperative.New()
	defer owner.Close()
	defer foreign.Close()
	settings := protocol.NewJSONObject(protocol.JSONField{Name: "permissions", Value: []byte(`{"defaultMode":"acceptEdits"}`)})
	global := protocol.NewJSONObject()
	producer, e := NewNativeStartup(owner, NativeStartupOptions{Home: home, CWD: home, OriginalCWD: home, Platform: "darwin", Environment: func(string) string { return "" }, Global: func() (*protocol.JSONObject, error) { return &global, nil }, Stat: os.Stat, Lstat: os.Lstat, Realpath: filepath.EvalSymlinks})
	if e != nil {
		t.Fatal(e)
	}
	if producer.Owns(owner) || ValidateNativeWithBindings(&settings, false, false, false, false, producer.Owns(owner)) == nil {
		t.Fatal("unprepared startup admitted saved mode")
	}
	if e = foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, e := producer.Prepare(scope, &settings, NativeStartupRequest{})
		return e
	}); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatalf("foreign prepare %v", e)
	}
	var stale *cooperative.Scope
	if e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		stale = scope
		r, e := producer.Prepare(scope, &settings, NativeStartupRequest{AdditionalDirectories: []string{""}})
		if e != nil {
			return e
		}
		if r.Mode != "acceptEdits" {
			t.Errorf("saved mode%s", r.Mode)
		}
		r.Mode = "bypassPermissions"
		r.Warnings[0] = "poison"
		r.WorkingDirectories[0] = '['
		r.InstructionDirectories[0] = "poison"
		clean, e := producer.Result(scope)
		if e != nil {
			return e
		}
		if clean.Mode != "acceptEdits" || clean.Warnings[0] != "Please provide a directory path." || clean.WorkingDirectories[0] != '{' || clean.InstructionDirectories[0] != "" {
			t.Fatal("result copy mutated original startup")
		}
		_, e = producer.Prepare(scope, &settings, NativeStartupRequest{})
		if !errors.Is(e, cooperative.ErrRestricted) {
			t.Fatalf("repeated startup replaced original context%v", e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if _, e = producer.Result(stale); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatalf("stale startup result %v", e)
	}
	if ValidateNeutral(&settings) == nil {
		t.Fatal("generic finite permission guard broadened")
	}
}

func TestNativeStartupDirectoryErrorsSkipOriginalErrnosAndRejectUnexpectedFailureBeforePrepared(t *testing.T) {
	for _, failure := range []error{fs.ErrNotExist, syscall.ENOTDIR, syscall.EACCES, syscall.EPERM, syscall.EIO} {
		t.Run(failure.Error(), func(t *testing.T) {
			home := t.TempDir()
			owner := cooperative.New()
			defer owner.Close()
			global := protocol.NewJSONObject()
			producer, e := NewNativeStartup(owner, NativeStartupOptions{Home: home, CWD: home, OriginalCWD: home, Platform: "linux", Environment: func(string) string { return "" }, Global: func() (*protocol.JSONObject, error) { return &global, nil }, Stat: func(string) (fs.FileInfo, error) {
				return nil, &os.PathError{Op: "stat", Path: filepath.Join(home, "candidate"), Err: failure}
			}, Lstat: os.Lstat, Realpath: filepath.EvalSymlinks})
			if e != nil {
				t.Fatal(e)
			}
			settings := protocol.NewJSONObject()
			e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				_, e := producer.Prepare(scope, &settings, NativeStartupRequest{AdditionalDirectories: []string{"candidate"}})
				return e
			})
			if errors.Is(failure, syscall.EIO) {
				if !errors.Is(e, failure) || producer.Owns(owner) {
					t.Fatalf("unexpected FS error silently prepared %v", e)
				}
			} else if e != nil || !producer.Owns(owner) {
				t.Fatalf("original missing/inaccessible directory not skipped %v", e)
			}
		})
	}
}
