package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

func TestNativeCurrentSkillDirectoryPreservesWarmCacheAgainstWholeSource(t *testing.T) {
	for _, profile := range []string{"absolute", "empty", "relative", "parent", "unicode", "home", "settings-disabled"} {
		t.Run(profile, func(t *testing.T) { testNativeCurrentSkillDirectoryAgainstSource(t, profile, false) })
	}
}

func TestNativeSkillsCaptureRootsBeforeHeldFilesystemCompletionAgainstWholeSource(t *testing.T) {
	testNativeCurrentSkillDirectoryAgainstSource(t, "absolute", true)
}

type heldNativeSkillDirectory struct {
	nativeSkillFiles
	path             string
	once             sync.Once
	entered, release chan struct{}
	ctx              context.Context
}

func (f *heldNativeSkillDirectory) ReadDir(path string) ([]fs.DirEntry, error) {
	entries, err := f.nativeSkillFiles.ReadDir(path)
	if err == nil && path == f.path {
		f.once.Do(func() {
			close(f.entered)
			select {
			case <-f.release:
			case <-f.ctx.Done():
			}
		})
	}
	return entries, err
}

func testNativeCurrentSkillDirectoryAgainstSource(t *testing.T, profile string, held bool) {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned whole Source Bun required")
	}
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	home, _ = filepath.EvalSymlinks(home)
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-config-dir-skills.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--feature=TRANSCRIPT_CLASSIFIER", "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CONFIG_DIR_SKILLS_SOURCE=1", "CLAUDE_CODE_USE_NATIVE_FILE_SEARCH=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home, "profile": profile, "held": held}))
	output, err := command.CombinedOutput()
	type observation struct {
		Commands []protocol.JSONObject `json:"commands"`
		Watch    []string              `json:"watch"`
	}
	var source struct {
		Initial, Next, NextPhysical, CWD string
		NextHome                         *string
		Before, Warm, Cleared            observation
		Held                             *observation
		Caps                             struct{ Blocked, SecureReads []string }
	}
	expectedCommands := 2
	if profile == "home" {
		expectedCommands = 3
	}
	if err != nil || json.Unmarshal(output, &source) != nil || len(source.Before.Commands) != expectedCommands || len(source.Cleared.Commands) != expectedCommands || len(source.Caps.Blocked) != 0 || len(source.Caps.SecureReads) != 0 {
		t.Fatalf("whole skills directory Source: %v %.6000s", err, output)
	}
	n, err := NewProcessNativeBoundary(NativeProcessOptions{Environment: map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": source.Initial, "ANTHROPIC_BASE_URL": "http://127.0.0.1:1", "CLAUDE_CODE_USE_NATIVE_FILE_SEARCH": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "PATH": "/usr/bin:/bin"}, CWD: source.CWD, Platform: runtime.GOOS, ManagedRoot: filepath.Join(home, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		t.Error("skills invoked Keychain")
		return nil, os.ErrPermission
	}
	p, err := NewNativeProcessSettings(n, runtime.GOOS, DefaultState())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(context.Background())
	n.bindProcessEnvironment(p.environment)
	service, err := p.SkillCommands(NativeSkillOptions{})
	if err != nil {
		t.Fatal(err)
	}
	observe := func(service *skillcommands.Service) observation {
		commands, err := service.Local(ctx, n.CWD)
		if err != nil {
			t.Fatal(err)
		}
		value := observation{Commands: []protocol.JSONObject{}, Watch: []string{}}
		for _, command := range commands {
			value.Commands = append(value.Commands, command.Metadata())
		}
		for _, path := range service.WatchPaths(ctx, filepath.Join(n.ConfigHomeDirectory(), "cc-haha", "computer-use-config.json")) {
			if _, err := os.Stat(n.nativePhysicalPath(path)); err == nil {
				value.Watch = append(value.Watch, path)
			}
		}
		return value
	}
	compare := func(phase string, actual, expected observation) {
		actualJSON, expectedJSON := rawMain(actual), rawMain(expected)
		var got, want any
		if json.Unmarshal(actualJSON, &got) != nil || json.Unmarshal(expectedJSON, &want) != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s same skills service current-directory/cache result\nGo %s\nSource %s", phase, actualJSON, expectedJSON)
		}
	}
	compare("before", observe(service), source.Before)
	if err := p.environment.SetProperty(quoteMain("CLAUDE_CONFIG_DIR"), quoteMain(source.Next)); err != nil {
		t.Fatal(err)
	}
	if source.NextHome != nil {
		if err := p.environment.SetProperty(quoteMain("HOME"), quoteMain(*source.NextHome)); err != nil {
			t.Fatal(err)
		}
	}
	if n.Environment["CLAUDE_CONFIG_DIR"] != source.Initial {
		t.Fatal("test rewrote the launch map")
	}
	compare("warm", observe(service), source.Warm)
	service.Clear()
	compare("cleared", observe(service), source.Cleared)
	if _, err := p.InitialSettings(); err == nil || !strings.Contains(err.Error(), "changed filesystem/schema host") {
		t.Fatalf("unproved ordinary ConfigDir admission changed: %v", err)
	}
	if held {
		if source.Held == nil {
			t.Fatal("Source held filesystem observation missing")
		}
		if err := p.environment.SetProperty(quoteMain("CLAUDE_CONFIG_DIR"), quoteMain(source.Initial)); err != nil {
			t.Fatal(err)
		}
		files := &heldNativeSkillDirectory{nativeSkillFiles: nativeSkillFiles{n: n}, path: filepath.Join(source.Initial, "skills"), entered: make(chan struct{}), release: make(chan struct{}), ctx: ctx}
		heldService, err := p.skillCommandsWithFiles(NativeSkillOptions{}, files)
		if err != nil {
			t.Fatal(err)
		}
		type result struct {
			commands []skillcommands.Definition
			err      error
		}
		done := make(chan result, 1)
		go func() { commands, err := heldService.Local(ctx, n.CWD); done <- result{commands, err} }()
		select {
		case <-files.entered:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		if err := p.environment.SetProperty(quoteMain("CLAUDE_CONFIG_DIR"), quoteMain(source.Next)); err != nil {
			t.Fatal(err)
		}
		close(files.release)
		select {
		case got := <-done:
			if got.err != nil {
				t.Fatal(got.err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		compare("held cold and later warm", observe(heldService), *source.Held)
	}
}
