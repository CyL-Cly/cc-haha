package builtintools

import (
	"context"
	"fmt"
	"io/fs"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestWindowsFileValidationUNCNeverTouchesFilesystemBeforePermission(t *testing.T) {
	s, owner, _, permissions := fixture(t)
	s.options.Platform = "windows"
	s.options.CWD = `\\server\share\project`
	s.options.Home = `C:\User`
	s.options.Environment["CLAUDE_CONFIG_DIR"] = t.TempDir()
	canonicalCalls := 0
	s.options.PermissionRealpath = func(string) (string, error) { canonicalCalls++; return "", fs.ErrPermission }
	permissions.Set("mode", quote("default"))
	run := func(fn func(*cooperative.Scope) error) {
		t.Helper()
		if err := owner.Run(context.Background(), fn); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"Read", "Edit", "NotebookEdit"} {
		run(func(scope *cooperative.Scope) error {
			in := input(`{"file_path":"\\\\server\\share\\other\\file.exe","notebook_path":"\\\\server\\share\\other\\notebook.bad","old_string":"old","new_string":"new"}`)
			if err := s.Validate(context.Background(), name, in, scope); err != nil {
				return fmt.Errorf("UNC %s validation touched/rejected: %w", name, err)
			}
			decision, err := s.PermissionDecision(scope, name, &in)
			if err != nil {
				return err
			}
			if decision.Behavior != "ask" {
				return fmt.Errorf("outside UNC %s: %s", name, decision.Behavior)
			}
			return nil
		})
	}
	if canonicalCalls != 0 {
		t.Fatalf("UNC called native canonical filesystem %d", canonicalCalls)
	}
	run(func(scope *cooperative.Scope) error {
		in := input(`{"file_path":"\\\\server\\share\\project\\file.txt"}`)
		decision, err := s.Permission(scope, "Read", in)
		if err != nil {
			return err
		}
		if decision != "allow" {
			return fmt.Errorf("trusted UNC workspace Read %s", decision)
		}
		permissions.Set("mode", quote("acceptEdits"))
		decision, err = s.Permission(scope, "Write", in)
		if err != nil {
			return err
		}
		if decision != "allow" {
			return fmt.Errorf("Source acceptEdits trusted UNC %s", decision)
		}
		return nil
	})
	if canonicalCalls != 0 {
		t.Fatal("trusted UNC admission performed premature I/O")
	}
}

func TestWindowsFilePermissionActualSourceDriveRootCaseAndCanonicalEscape(t *testing.T) {
	s, owner, _, permissions := fixture(t)
	s.options.Platform = "windows"
	s.options.CWD = `C:\Work`
	s.options.Home = `C:\User`
	s.options.Environment["CLAUDE_CONFIG_DIR"] = t.TempDir()
	s.options.PermissionRoots = map[string]string{"userSettings": `C:\User\.claude`, "projectSettings": `C:\Work`}
	s.options.PermissionRealpath = func(p string) (string, error) {
		if p == `C:\Work\escape` {
			return `D:\Outside`, nil
		}
		return "", fs.ErrNotExist
	}
	permissions.Set("mode", quote("default"))
	permissions.Set("alwaysDenyRules", raw(map[string][]string{"session": {"Read(//c/Work/secret.txt)"}}))
	for _, r := range []struct{ path, decision string }{{`/c/Work/file.txt`, "allow"}, {`c:\WORK\FILE.txt`, "allow"}, {`C:\Work\secret.txt`, "deny"}, {`D:\Work\file.txt`, "ask"}, {`C:\Work\escape\file.txt`, "ask"}} {
		if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			in := protocol.NewJSONObject(field("file_path", r.path))
			got, err := s.Permission(scope, "Read", in)
			if err != nil {
				return err
			}
			if got != r.decision {
				return fmt.Errorf("%s Go=%s Source=%s", r.path, got, r.decision)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}
