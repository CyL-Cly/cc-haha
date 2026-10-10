package sessionrewind

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type faultFiles struct {
	NativeFiles
	write    func(context.Context, string, FileState) error
	writable func(context.Context, string, FileState, FileState) error
}

func (f faultFiles) WriteState(ctx context.Context, path string, state FileState) error {
	if f.write != nil {
		return f.write(ctx, path, state)
	}
	return f.NativeFiles.WriteState(ctx, path, state)
}
func (f faultFiles) AssertWritable(ctx context.Context, path string, old, new FileState) error {
	if f.writable != nil {
		return f.writable(ctx, path, old, new)
	}
	return f.NativeFiles.AssertWritable(ctx, path, old, new)
}

func restoreFixture(t *testing.T) (*Service, NativeFiles, []string) {
	t.Helper()
	home := t.TempDir()
	project := filepath.Join(home, "project")
	files := NativeFiles{ConfigHome: filepath.Join(home, ".claude")}
	paths := []string{filepath.Join(project, "a"), filepath.Join(project, "b")}
	for _, name := range []string{"a", "b"} {
		writePrivate(t, filepath.Join(project, name), "new "+name)
		writePrivate(t, filepath.Join(files.ConfigHome, "file-history", "fixture-session", "before-"+name), "old "+name)
		writePrivate(t, filepath.Join(files.ConfigHome, "file-history", "fixture-session", "after-"+name), "new "+name)
	}
	input := rewindFixture{Home: home, Complete: true, Messages: []protocol.JSONObject{fixtureMessage("u", "user", project, "prompt", ""), fixtureMessage("a", "assistant", project, "done", "")}, Rows: []json.RawMessage{fixtureRow("u", map[string]any{"a": fixtureBackup("before-a"), "b": fixtureBackup("before-b")}, map[string]any{"a": fixtureBackup("after-a"), "b": fixtureBackup("after-b")}, true)}}
	return fixtureService(t, input), files, paths
}

func TestExecutePreflightAndRollbackUseActualByteStates(t *testing.T) {
	for _, name := range []string{"unwritable", "partial-write", "trim-failure", "canceled-write", "rollback-failure"} {
		t.Run(name, func(t *testing.T) {
			service, files, paths := restoreFixture(t)
			initial := observedNative(t, paths)
			trimmed := false
			stopped := false
			failure := errors.New("fixture write failure")
			trimFailure := errors.New("fixture trim failure")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			service.options.StopSessionAndWait = func(context.Context, string) error { stopped = true; return nil }
			service.options.TrimSessionMessagesFrom = func(context.Context, string, string) (TrimResult, error) {
				trimmed = true
				return TrimResult{}, trimFailure
			}
			fault := faultFiles{NativeFiles: files}
			if name == "unwritable" {
				fault.writable = func(ctx context.Context, path string, old, new FileState) error {
					if path == paths[1] {
						return errors.New("fixture unwritable")
					}
					return files.AssertWritable(ctx, path, old, new)
				}
			}
			writes := 0
			if name != "trim-failure" && name != "unwritable" {
				fault.write = func(ctx context.Context, path string, state FileState) error {
					writes++
					if writes == 2 {
						if err := files.WriteState(ctx, path, FileState{Exists: true, Content: []byte("partially written"), Mode: 0600}); err != nil {
							t.Fatal(err)
						}
						if name == "canceled-write" {
							cancel()
							return context.Canceled
						}
						return failure
					}
					if name == "rollback-failure" && writes == 3 {
						return errors.New("fixture rollback failure")
					}
					return files.WriteState(ctx, path, state)
				}
			}
			service.options.Files = fault
			_, err := service.Execute(ctx, "fixture-session", Selector{TargetUserMessageID: "u"}, Both)
			if err == nil || !stopped {
				t.Fatalf("expected safe failure after stop: %v stopped=%v", err, stopped)
			}
			if name == "trim-failure" {
				if !trimmed || !errors.Is(err, trimFailure) {
					t.Fatalf("trim result %v/%v", trimmed, err)
				}
			} else if trimmed {
				t.Fatal("transcript trimmed after failed file preflight/restore")
			}
			if name == "rollback-failure" {
				var incomplete *Failure
				if !errors.As(err, &incomplete) || !errors.Is(err, failure) || err.Error() != "Restore failed and rollback was incomplete: fixture rollback failure" {
					t.Fatalf("incomplete rollback hidden %v", err)
				}
				content, _ := os.ReadFile(paths[0])
				if string(content) != "new a" {
					t.Fatalf("first file not rolled back %q", content)
				}
				content, _ = os.ReadFile(paths[1])
				if string(content) != "partially written" {
					t.Fatalf("partial second file not reported accurately %q", content)
				}
			} else if got := observedNative(t, paths); !reflect.DeepEqual(got, initial) {
				t.Fatalf("native byte/mode rollback changed files %#v vs %#v", got, initial)
			}
		})
	}
}

func TestNativeRestoreRejectsLinksAndPreservesBinaryModes(t *testing.T) {
	_, files, paths := restoreFixture(t)
	ctx := context.Background()
	if err := os.Link(paths[0], paths[0]+"-alias"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(paths[0])
	for _, state := range []FileState{{Exists: false}, {Exists: true, Content: []byte("unsafe"), Mode: 0600}} {
		if err := files.WriteState(ctx, paths[0], state); err == nil {
			t.Fatal("hardlink modified by restore")
		}
	}
	if got, _ := os.ReadFile(paths[0]); !reflect.DeepEqual(got, before) {
		t.Fatal("hardlinked bytes changed")
	}
	if err := os.Remove(paths[0] + "-alias"); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		link := paths[0] + "-link"
		if err := os.Symlink(paths[0], link); err != nil {
			t.Fatal(err)
		}
		if err := files.WriteState(ctx, link, FileState{Exists: true, Content: []byte("unsafe"), Mode: 0600}); err == nil {
			t.Fatal("leaf symlink modified by restore")
		}
	}
	state := FileState{Exists: true, Content: []byte{0, 255, 10, 13, 1}, Mode: 0640}
	if err := files.WriteState(ctx, paths[0], state); err != nil {
		t.Fatal(err)
	}
	got, err := files.ReadCurrentState(ctx, paths[0])
	if err != nil || !stateMatches(got, state) || (runtime.GOOS != "windows" && got.Mode.Perm() != 0640) {
		t.Fatalf("binary mode state %#v/%v", got, err)
	}
	if err = files.WriteState(ctx, paths[0], FileState{}); err != nil {
		t.Fatal(err)
	}
	got, err = files.ReadCurrentState(ctx, paths[0])
	if err != nil || got.Exists {
		t.Fatalf("delete %#v/%v", got, err)
	}
}

func TestRawNullAndNumberTruthinessAndIncompleteMutations(t *testing.T) {
	for _, raw := range []string{" null ", " false ", " 0.0 ", " -0e10 ", ` "" `} {
		if truthy(json.RawMessage(raw)) {
			t.Fatalf("false JS value %s", raw)
		}
	}
	for _, raw := range []string{" true ", " [] ", " {} ", ` "\u0000" `, "1e-4", "1e999"} {
		if !truthy(json.RawMessage(raw)) {
			t.Fatalf("true JS value %s", raw)
		}
	}
	service, _, paths := restoreFixture(t)
	initial := observedNative(t, paths)
	service.options.Snapshots = func(context.Context, string) (SnapshotEvidence, error) { return SnapshotEvidence{Complete: false}, nil }
	service.options.StopSessionAndWait = func(context.Context, string) error { return nil }
	service.options.TrimSessionMessagesFrom = func(context.Context, string, string) (TrimResult, error) {
		t.Fatal("truncated backup evidence trimmed")
		return TrimResult{}, nil
	}
	if _, err := service.Execute(context.Background(), "fixture-session", Selector{TargetUserMessageID: "u"}, Both); !errors.Is(err, ErrSnapshotEvidenceIncomplete) {
		t.Fatalf("incomplete evidence %v", err)
	}
	if got := observedNative(t, paths); !reflect.DeepEqual(got, initial) {
		t.Fatal("files changed on incomplete evidence")
	}
}
