package builtinwrite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/nativepaths"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Only the OS path primitive is adapted. The real Windows Service expansion,
// validation, owner/cache, native futures and flushed writer execute unchanged.
// This proves platform semantics with native disposable bytes on this host,
// separately from Windows-kernel tests and cross compilation.
type windowsPhysicalFiles struct {
	OSFiles
	root                                string
	operations                          []string
	renameError, chmodError, flushError error
}

func (f *windowsPhysicalFiles) mapPath(p string) string {
	f.operations = append(f.operations, p)
	if p == "." {
		return f.root
	}
	p = nativepaths.WindowsToPOSIX(p)
	return filepath.Join(f.root, filepath.FromSlash(strings.TrimLeft(p, "/")))
}
func (f *windowsPhysicalFiles) Stat(p string) (fs.FileInfo, error)  { return os.Stat(f.mapPath(p)) }
func (f *windowsPhysicalFiles) Lstat(p string) (fs.FileInfo, error) { return os.Lstat(f.mapPath(p)) }
func (f *windowsPhysicalFiles) Realpath(p string) (string, error) {
	_, err := filepath.EvalSymlinks(f.mapPath(p))
	return p, err
}
func (f *windowsPhysicalFiles) Readlink(p string) (string, error)     { return os.Readlink(f.mapPath(p)) }
func (f *windowsPhysicalFiles) OpenRead(p string) (ReadHandle, error) { return os.Open(f.mapPath(p)) }
func (f *windowsPhysicalFiles) ReadFile(p string) ([]byte, error)     { return os.ReadFile(f.mapPath(p)) }
func (f *windowsPhysicalFiles) Mkdir(p string) error                  { return os.MkdirAll(f.mapPath(p), 0700) }
func (f *windowsPhysicalFiles) WriteFlush(p string, b []byte) error {
	path := f.mapPath(p)
	if f.flushError != nil && !strings.Contains(p, ".tmp.") {
		return f.flushError
	}
	return f.OSFiles.WriteFlush(path, b)
}
func (f *windowsPhysicalFiles) Chmod(p string, m fs.FileMode) error {
	path := f.mapPath(p)
	if f.chmodError != nil {
		return f.chmodError
	}
	return os.Chmod(path, m)
}
func (f *windowsPhysicalFiles) Rename(p, q string) error {
	left, right := f.mapPath(p), f.mapPath(q)
	if f.renameError != nil {
		return f.renameError
	}
	return os.Rename(left, right)
}
func (f *windowsPhysicalFiles) Unlink(p string) error { return os.Remove(f.mapPath(p)) }

func windowsWriteFixture(t *testing.T) (*Service, *cooperative.Owner, *windowsPhysicalFiles) {
	s, owner, _, _, home := fixture(t)
	s.options.Host.Platform = "windows"
	s.options.Home = `C:\User`
	s.options.CWD = `C:\Work`
	s.options.PermissionRoots = map[string]string{"userSettings": `C:\User\.claude`, "projectSettings": `C:\Work`}
	s.options.HostState = func(*cooperative.Scope) (Host, error) { return Host{Platform: "windows", NonInteractive: true}, nil }
	files := &windowsPhysicalFiles{root: home}
	s.options.Files = files
	return s, owner, files
}

func TestWindowsWriteNativeBytesReadCacheAndReplacementFailureFallback(t *testing.T) {
	for _, stage := range []string{"success", "rename", "chmod", "fallback"} {
		t.Run(stage, func(t *testing.T) {
			s, owner, files := windowsWriteFixture(t)
			path := `C:\Work\file.txt`
			actual := files.mapPath(path)
			if err := os.MkdirAll(filepath.Dir(actual), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(actual, []byte("before\r\n"), 0640); err != nil {
				t.Fatal(err)
			}
			in := input("/c/Work/file.txt", "after 世界\r\n")
			run(t, owner, func(scope *cooperative.Scope) error {
				if err := s.Backfill(&in); err != nil {
					return err
				}
				if in.String("file_path") != path {
					return fmt.Errorf("actual Windows backfill: %q", in.String("file_path"))
				}
				if err := s.ReadForCache(scope, in.Get("file_path")); err != nil {
					return err
				}
				if err := s.Validate(context.Background(), in, scope); err != nil {
					return err
				}
				switch stage {
				case "rename":
					files.renameError = fs.ErrPermission
				case "chmod":
					files.chmodError = fs.ErrPermission
				case "fallback":
					files.renameError = fs.ErrPermission
					files.flushError = errors.New("actual fallback flush failed")
				}
				result, err := s.Call(context.Background(), in, scope)
				state, exists, cacheErr := s.options.Cache.Get(scope, path)
				if cacheErr != nil {
					return cacheErr
				}
				if stage == "fallback" {
					if err == nil || err.Error() != "actual fallback flush failed" {
						return fmt.Errorf("fallback priority %v", err)
					}
					if !exists || string(state.Content) != string(quoted("before\n")) {
						return fmt.Errorf("failure mutated cache %s", state.Content)
					}
					return nil
				}
				if err != nil {
					return err
				}
				var object protocol.JSONObject
				if err := object.UnmarshalJSON(result); err != nil {
					return err
				}
				if object.String("type") != "update" || !exists || string(state.Content) != string(quoted("after 世界\r\n")) {
					return fmt.Errorf("result/cache %s %s", result, state.Content)
				}
				return nil
			})
			data, err := os.ReadFile(actual)
			want := "after 世界\r\n"
			if stage == "fallback" {
				want = "before\r\n"
			}
			if err != nil || string(data) != want {
				t.Fatalf("physical bytes %q %v", data, err)
			}
			matches, _ := filepath.Glob(actual + ".tmp.*")
			if len(matches) != 0 {
				t.Fatal("temporary replacement leaked", matches)
			}
		})
	}
}

func TestWindowsUNCValidationAndPendingPermissionPerformNoFilesystemAccess(t *testing.T) {
	s, owner, files := windowsWriteFixture(t)
	s.options.PermissionContext = func(*cooperative.Scope) (protocol.JSONObject, error) {
		return protocol.NewJSONObject(protocol.JSONField{Name: "mode", Value: quoted("default")}, protocol.JSONField{Name: "alwaysDenyRules", Value: []byte(`{}`)}), nil
	}
	in := input(`\\server\share\secret.txt`, "after")
	run(t, owner, func(scope *cooperative.Scope) error {
		if err := s.Backfill(&in); err != nil {
			return err
		}
		if err := s.Validate(context.Background(), in, scope); err != nil {
			return err
		}
		decision, err := s.Permission(scope, in)
		if err != nil {
			return err
		}
		if decision != "ask" {
			return fmt.Errorf("unapproved UNC decision %s", decision)
		}
		return nil
	})
	if len(files.operations) != 0 {
		t.Fatal("UNC I/O before permission", files.operations)
	}
	actual := files.mapPath(`\\server\share\secret.txt`)
	if err := os.MkdirAll(filepath.Dir(actual), 0700); err != nil {
		t.Fatal(err)
	}
	// The existing tool resolver is the admission boundary. Explicitly granting
	// this disposable target then reaches the same physical writer, not an ACK.
	run(t, owner, func(scope *cooperative.Scope) error { _, err := s.Call(context.Background(), in, scope); return err })
	data, err := os.ReadFile(actual)
	if err != nil || string(data) != "after" {
		t.Fatalf("approved UNC actual bytes %q %v", data, err)
	}
}

func TestWindowsWriteCatalogUsesActualRuleParserWholeToolIdentity(t *testing.T) {
	s, owner, _, _, _ := fixture(t)
	s.options.Host.Platform = "windows"
	for _, row := range []struct {
		rule     string
		filtered bool
	}{{"Write", true}, {"Write(*)", true}, {"Write()", true}, {"Write(file.txt)", false}, {"Write(file.txt", false}, {"Edit(*)", false}} {
		s.options.PermissionContext = func(*cooperative.Scope) (protocol.JSONObject, error) {
			return protocol.NewJSONObject(protocol.JSONField{Name: "alwaysDenyRules", Value: rawWindowsRules(row.rule)}), nil
		}
		if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			definitions, tools, err := s.Catalog(scope)
			if err != nil {
				return err
			}
			if (len(definitions) == 0) != row.filtered || (len(tools) == 0) != row.filtered {
				return fmt.Errorf("actual Write catalog rule %q definitions=%d tools=%d", row.rule, len(definitions), len(tools))
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func rawWindowsRules(rule string) json.RawMessage {
	value, _ := json.Marshal(map[string][]string{"session": {rule}})
	return value
}
