package clibootstrap

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestWindowsNativeProcessIdentityUsesProfileConfigAndPhysicalCWD(t *testing.T) {
	root := t.TempDir()
	home, ignored, cwd, config := filepath.Join(root, "profile"), filepath.Join(root, "ignored"), filepath.Join(root, "workspace"), filepath.Join(root, "relocated")
	for _, path := range []string{home, ignored, cwd, config} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	o := NativeProcessOptions{Platform: "windows", CWD: cwd, Environment: map[string]string{"HOME": ignored, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": config, "ANTHROPIC_BASE_URL": "http://127.0.0.1:1"}, ManagedRoot: filepath.Join(root, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }}
	n, err := NewProcessNativeBoundary(o)
	if err != nil {
		t.Fatal(err)
	}
	physical, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if n.Home != home || n.CWD != physical || n.Environment["HOME"] != ignored || n.Environment["CLAUDE_CONFIG_DIR"] != config || !n.ConfigDirExplicit {
		t.Fatalf("native identity: home=%q cwd=%q config=%q", n.Home, n.CWD, n.Environment["CLAUDE_CONFIG_DIR"])
	}
	if _, err := os.Stat(filepath.Join(ignored, ".claude")); !os.IsNotExist(err) {
		t.Fatalf("touched ignored HOME: %v", err)
	}
	o.CWD = filepath.Join(root, "missing")
	if _, err := NewProcessNativeBoundary(o); !os.IsNotExist(err) {
		t.Fatalf("missing cwd must fail: %v", err)
	}
}

func TestWindowsNativePermissionRootsFollowSourceConfigAndFlagSources(t *testing.T) {
	m := &MainPrint{options: MainPrintOptions{Platform: "windows", Native: &NativeBoundary{Home: `C:\Users\worker`, CWD: `C:\Project`, ManagedRoot: `C:\Program Files\ClaudeCode`, Environment: map[string]string{"CLAUDE_CONFIG_DIR": `D:\Data\..\Config`}}}}
	flag := `D:\Flag\folder\..\settings.json`
	m.options.Arguments.Sources.FlagSettingsPath = &flag
	want := map[string]string{"userSettings": `D:\Config`, "flagSettings": `D:\Flag`}
	for _, source := range []string{"projectSettings", "localSettings", "policySettings", "cliArg", "command", "session"} {
		want[source] = `C:\Project`
	}
	if actual := m.nativeFilePermissionRoots(); !reflect.DeepEqual(actual, want) {
		t.Fatalf("Source roots: %+v want %+v", actual, want)
	}
	// A worktree child retains the parent bootstrap's original source roots.
	m.options.Native.CWD = `C:\Project\.claude\worktrees\child`
	m.options.DetachedAgent = &NativeDetachedAgentOptions{}
	m.options.DetachedAgent.Bootstrap.OriginalCWD = `C:\Project`
	flag = `flags\settings.json`
	want["flagSettings"] = `C:\Project\flags`
	if actual := m.nativeFilePermissionRoots(); !reflect.DeepEqual(actual, want) {
		t.Fatalf("detached original Source roots changed: %+v want %+v", actual, want)
	}
	m.options.Platform = "darwin"
	want = map[string]string{"userSettings": m.options.Native.Home, "projectSettings": m.options.Native.CWD, "localSettings": m.options.Native.CWD, "policySettings": m.options.Native.ManagedRoot}
	if actual := m.nativeFilePermissionRoots(); !reflect.DeepEqual(actual, want) {
		t.Fatalf("existing non-Windows roots changed: %+v", actual)
	}
}
