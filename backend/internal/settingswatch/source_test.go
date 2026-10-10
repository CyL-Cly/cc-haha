package settingswatch

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func sourceWatch(t *testing.T, root string, input any) []byte {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-settings-watch.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = root
	command.Env = []string{"HOME=" + root, "USERPROFILE=" + root, "CLAUDE_CONFIG_DIR=" + filepath.Join(root, ".claude"), "TMPDIR=" + root, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(root), "CC_HAHA_SETTINGS_WATCH_FIXTURE=1", "BUN_OPTIONS=--no-env-file"}
	payload, _ := json.Marshal(input)
	command.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		t.Fatalf("actual settings watcher Source %v %s", err, stderr.String())
	}
	return out
}
func watchSourceFiles(root string) []SourceFile {
	return []SourceFile{{"userSettings", filepath.Join(root, "config", "settings.json")}, {"projectSettings", filepath.Join(root, "project", ".claude", "settings.json")}, {"localSettings", filepath.Join(root, "project", ".claude", "settings.local.json")}, {"flagSettings", filepath.Join(root, "flag", "settings.json")}, {"policySettings", filepath.Join(root, "managed", "managed-settings.json")}}
}
func sourceFilesInput(files []SourceFile) []any {
	rows := []any{}
	for _, file := range files {
		rows = append(rows, map[string]string{"source": file.Source, "path": file.Path})
	}
	return rows
}

func TestNativeSettingsWatchTargetsAndTimingMatchActualSource(t *testing.T) {
	for _, profile := range []string{"none", "user", "project", "policy-drop", "all"} {
		t.Run(profile, func(t *testing.T) {
			root := t.TempDir()
			files := watchSourceFiles(root)
			drop := filepath.Join(root, "managed", "managed-settings.d")
			if profile == "all" {
				for _, file := range files {
					writeSettings(t, file.Path, `{}`)
				}
			} else {
				for _, file := range files {
					if profile == "user" && file.Source == "userSettings" || profile == "project" && file.Source == "projectSettings" {
						writeSettings(t, file.Path, `{}`)
					}
				}
			}
			if profile == "policy-drop" || profile == "all" {
				if err := os.MkdirAll(drop, 0700); err != nil {
					t.Fatal(err)
				}
			}
			var source struct {
				Targets struct {
					Directories, Files []string
					DropInDirectory    *string
				}
				Constants map[string]int64
			}
			if err := json.Unmarshal(sourceWatch(t, root, map[string]any{"root": root, "files": sourceFilesInput(files), "dropIn": drop}), &source); err != nil {
				t.Fatal(err)
			}
			got, err := Collect(files, drop)
			if err != nil {
				t.Fatal(err)
			}
			paths := []string{}
			for _, file := range got.Files {
				paths = append(paths, file.Path)
			}
			dropGot := (*string)(nil)
			if got.DropInDirectory != "" {
				dropGot = &got.DropInDirectory
			}
			if !reflect.DeepEqual(got.Directories, source.Targets.Directories) || !reflect.DeepEqual(paths, source.Targets.Files) || !reflect.DeepEqual(dropGot, source.Targets.DropInDirectory) {
				t.Fatalf("targets %#v Source%#v", got, source.Targets)
			}
			constants := map[string]int64{"stability": DefaultTiming.Stability.Milliseconds(), "poll": DefaultTiming.Poll.Milliseconds(), "deletionGrace": DefaultTiming.DeletionGrace.Milliseconds(), "mdmPoll": DefaultTiming.MDMPoll.Milliseconds(), "internalWriteWindow": 5000}
			if !reflect.DeepEqual(constants, source.Constants) {
				t.Fatal("Source original timing changed")
			}
		})
	}
}

func TestNativePhysicalSettingsLifecycleMatchesActualChokidarSource(t *testing.T) {
	actions := []map[string]any{{"wait": 100}, {"path": "config/settings.json", "write": "external-one", "wait": 200}, {"path": "config/settings.json", "mark": true, "write": "internal-echo", "wait": 200}, {"path": "config/settings.json", "write": "external-two", "wait": 200}, {"path": "project/.claude/settings.local.json", "write": "new-local", "wait": 200}, {"path": "managed/managed-settings.d/fragment.json", "write": "new-policy", "wait": 200}, {"path": "project/.claude/unknown.json", "write": "ignored", "wait": 200}, {"path": "config/settings.json", "remove": true, "wait": 25}, {"path": "config/settings.json", "write": "atomic-new", "wait": 400}, {"path": "project/.claude/settings.local.json", "remove": true, "wait": 500}}
	seed := func(root string) []SourceFile {
		files := watchSourceFiles(root)
		writeSettings(t, files[0].Path, `{}`)
		writeSettings(t, files[1].Path, `{}`)
		if err := os.MkdirAll(filepath.Join(root, "managed", "managed-settings.d"), 0700); err != nil {
			t.Fatal(err)
		}
		return files
	}
	sourceRoot := t.TempDir()
	sourceFiles := seed(sourceRoot)
	var source struct{ Trace []map[string]string }
	if err := json.Unmarshal(sourceWatch(t, sourceRoot, map[string]any{"root": sourceRoot, "files": sourceFilesInput(sourceFiles), "dropIn": filepath.Join(sourceRoot, "managed", "managed-settings.d"), "actions": actions}), &source); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	files := seed(root)
	drop := filepath.Join(root, "managed", "managed-settings.d")
	var mu sync.Mutex
	trace := []map[string]string{}
	marks := map[string]time.Time{}
	watcher, err := Start(Options{Files: files, DropInDirectory: drop, Timing: &physicalTiming, Change: func(ctx context.Context, source, path string) error {
		mu.Lock()
		defer mu.Unlock()
		relative, _ := filepath.Rel(root, path)
		trace = append(trace, map[string]string{"stage": "hook", "source": ConfigSource(source), "path": relative}, map[string]string{"stage": "reset"}, map[string]string{"stage": "apply", "source": source})
		return nil
	}, ConsumeInternalWrite: func(path string) bool {
		mu.Lock()
		defer mu.Unlock()
		if marked, ok := marks[path]; ok && time.Since(marked) < 5*time.Second {
			delete(marks, path)
			return true
		}
		return false
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	for _, action := range actions {
		relative, _ := action["path"].(string)
		path := filepath.Join(root, relative)
		if action["mark"] == true {
			mu.Lock()
			marks[path] = time.Now()
			mu.Unlock()
		}
		if value, present := action["write"]; present {
			writeSettings(t, path, value.(string))
		}
		if action["remove"] == true {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}
		time.Sleep(time.Duration(action["wait"].(int)) * time.Millisecond)
	}
	watcher.Close()
	mu.Lock()
	got := append([]map[string]string{}, trace...)
	mu.Unlock()
	if !reflect.DeepEqual(got, source.Trace) {
		t.Errorf("actual physical traces %#v want Source%#v", got, source.Trace)
	}
}
