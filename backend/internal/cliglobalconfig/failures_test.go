package cliglobalconfig

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type persistenceCapabilities struct {
	leaseCapabilities
	mkdirAll func(string) error
	readDir  func(string) ([]string, error)
}

func (capabilities persistenceCapabilities) MkdirAll(path string) error {
	if capabilities.mkdirAll != nil {
		return capabilities.mkdirAll(path)
	}
	return capabilities.OSFileSystem.MkdirAll(path)
}
func (capabilities persistenceCapabilities) ReadDir(path string) ([]string, error) {
	if capabilities.readDir != nil {
		return capabilities.readDir(path)
	}
	return capabilities.OSFileSystem.ReadDir(path)
}

func TestBackupFailureLeavesWriterFunctionalAndRetainsOriginalCopy(t *testing.T) {
	for _, boundary := range []string{"mkdir", "initial-list", "cleanup-list"} {
		t.Run(boundary, func(t *testing.T) {
			capabilities := persistenceCapabilities{}
			if boundary == "mkdir" {
				capabilities.mkdirAll = func(path string) error {
					if strings.HasSuffix(path, "backups") {
						return fs.ErrPermission
					}
					return (OSFileSystem{}).MkdirAll(path)
				}
			} else {
				calls := 0
				capabilities.readDir = func(path string) ([]string, error) {
					calls++
					if calls == 1 && boundary == "initial-list" || calls == 2 && boundary == "cleanup-list" {
						return nil, fs.ErrPermission
					}
					return (OSFileSystem{}).ReadDir(path)
				}
			}
			var diagnostics []string
			service := enabledService(t, Options{FS: capabilities, Diagnostic: func(kind string, _ map[string]any) { diagnostics = append(diagnostics, kind) }}, `{"protected":true}`)
			if err := service.Save(patchFixture); err != nil {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(service.Path())
			if !strings.Contains(string(raw), `"native": true`) || !strings.Contains(string(raw), `"protected": true`) {
				t.Fatal("backup failure altered the independent write", string(raw))
			}
			if len(diagnostics) != 1 || diagnostics[0] != "backup_failed" {
				t.Fatal(diagnostics)
			}
			if boundary == "cleanup-list" {
				entries, _ := os.ReadDir(filepath.Join(service.configHome, "backups"))
				if len(entries) != 1 {
					t.Fatal("successful backup deleted after failed enumeration")
				}
				copy, _ := os.ReadFile(filepath.Join(service.configHome, "backups", entries[0].Name()))
				if string(copy) != `{"protected":true}` {
					t.Fatal("original backup lost", string(copy))
				}
			}
		})
	}
}

func TestLockedStatFailureRetriesFreshReadAndReleaseFailureReinvokesUpdater(t *testing.T) {
	for _, boundary := range []string{"stat", "release"} {
		t.Run(boundary, func(t *testing.T) {
			var armed atomic.Bool
			capabilities := persistenceCapabilities{}
			if boundary == "stat" {
				capabilities.stat = func(path string) (FileStat, error) {
					if !strings.HasSuffix(path, ".lock") && armed.Swap(false) {
						return FileStat{}, fs.ErrPermission
					}
					return (OSFileSystem{}).Stat(path)
				}
			} else {
				capabilities.remove = func(path string) error {
					if strings.HasSuffix(path, ".lock") && armed.Swap(false) {
						return fs.ErrPermission
					}
					return os.Remove(path)
				}
			}
			service := enabledService(t, Options{FS: capabilities}, `{"protected":true}`)
			_, _ = service.Get()
			armed.Store(true)
			calls := 0
			if err := service.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
				calls++
				return patchFixture(current)
			}); err != nil {
				t.Fatal(err)
			}
			want := 1
			if boundary == "release" {
				want = 2
			}
			if calls != want {
				t.Fatalf("%s updater calls=%d want=%d", boundary, calls, want)
			}
			value, _ := service.Get()
			if string(value.Get("protected")) != "true" || string(value.Get("native")) != "true" {
				t.Fatal("latest state/cache was not retained")
			}
		})
	}
}

func TestPersistentDirectoryAndCorruptBackupErrorsNeverCallUnsafeUpdater(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "directory", true: "corrupt-backup"}[corrupt], func(t *testing.T) {
			var failing atomic.Bool
			capabilities := persistenceCapabilities{mkdirAll: func(path string) error {
				if failing.Load() {
					return fs.ErrPermission
				}
				return (OSFileSystem{}).MkdirAll(path)
			}}
			service := enabledService(t, Options{FS: capabilities}, `{"protected":true}`)
			_, _ = service.Get()
			initial := `{"protected":true}`
			if corrupt {
				initial = "{"
				_ = os.WriteFile(service.Path(), []byte(initial), 0600)
			}
			failing.Store(true)
			calls := 0
			err := service.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
				calls++
				return patchFixture(current)
			})
			if !errors.Is(err, fs.ErrPermission) {
				t.Fatal(err)
			}
			want := 1
			if corrupt {
				want = 0
			}
			if calls != want {
				t.Fatalf("unsafe updater ordering=%d want=%d", calls, want)
			}
			raw, _ := os.ReadFile(service.Path())
			if string(raw) != initial {
				t.Fatal("failed persistence changed protected source", string(raw))
			}
		})
	}
}

func TestFallbackSameReferenceAndContentionSignal(t *testing.T) {
	var now atomic.Int64
	now.Store(946684800000)
	var events []string
	capabilities := persistenceCapabilities{leaseCapabilities: leaseCapabilities{chtimes: func(path string, stamp time.Time) error {
		now.Add(150)
		return (OSFileSystem{}).Chtimes(path, stamp)
	}}}
	service := enabledService(t, Options{FS: capabilities, Now: func() time.Time { return time.UnixMilli(now.Load()) }, Diagnostic: func(kind string, _ map[string]any) { events = append(events, kind) }}, "{}")
	if err := service.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) { return current, nil }); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0] != "lock_contention" {
		t.Fatal(events)
	}
	_ = os.Mkdir(service.Path()+".lock", 0700)
	if err := service.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) { return current, nil }); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(service.Path())
	if string(raw) != "{}" {
		t.Fatal("same-reference fallback wrote", string(raw))
	}
}

func TestProjectTypedBoundaryRejectsUnrepresentedShapesAndInvalidRawUpdate(t *testing.T) {
	beforeEnabled := privateService(t, Options{})
	if _, err := beforeEnabled.GetProject("/fixture"); err == nil {
		t.Fatal("project read bypassed enable boundary")
	}
	for _, raw := range []string{`{"projects":1}`, `{"projects":{"/fixture":1}}`, `{"projects":{"/fixture":null}}`, `{"projects":{"/other":{}}}`} {
		service := enabledService(t, Options{}, raw)
		value, err := service.GetProject("/fixture")
		if strings.Contains(raw, `:1`) {
			if !errors.Is(err, ErrProjectShape) {
				t.Fatal("typed object boundary silently coerced primitive project", err)
			}
			if err := service.SaveProject("/fixture", patchFixture); !errors.Is(err, ErrProjectShape) {
				t.Fatal(err)
			}
		} else if err != nil || string(value.Get("allowedTools")) != "[]" {
			t.Fatal(value, err)
		}
	}
	service := enabledService(t, Options{}, "{}")
	if err := service.SaveProject("/fixture", func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		next := current.Clone()
		next.Set("malformed", []byte("{"))
		return &next, nil
	}); err == nil {
		t.Fatal("invalid raw project JSON persisted")
	}
}

func TestWriterDoesNotFallbackPastPermissionStat(t *testing.T) {
	capabilities := leaseCapabilities{stat: func(string) (FileStat, error) { return FileStat{}, fs.ErrPermission }}
	service := enabledService(t, Options{FS: capabilities}, `{"protected":true}`)
	if err := service.writeFile(service.Path(), []byte("bad"), nil); !errors.Is(err, fs.ErrPermission) {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(service.Path())
	if string(raw) != `{"protected":true}` {
		t.Fatal("stat error allowed an unsafe direct write")
	}
}
