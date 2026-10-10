package clibootstrap

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func processNativeForTest(t *testing.T) *NativeBoundary {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	work := filepath.Join(root, "work")
	managed := filepath.Join(root, "managed")
	for _, p := range []string{home, work, managed} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	n, err := NewProcessNativeBoundary(NativeProcessOptions{Environment: map[string]string{"HOME": home, "ANTHROPIC_BASE_URL": "https://api.deepseek.com/anthropic", "ANTHROPIC_API_KEY": "fake-no-quota"}, CWD: work, Platform: "darwin", ManagedRoot: managed, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	return n
}
func TestProcessNativeReadsRealPathsAndUsesRealWallClock(t *testing.T) {
	n := processNativeForTest(t)
	outside := filepath.Join(filepath.Dir(n.Home), "actual.txt")
	if err := os.WriteFile(outside, []byte("actual"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := n.readFile(outside)
	if err != nil || string(data) != "actual" {
		t.Fatal(string(data), err)
	}
	if !n.NativeProcess || n.ExternalAbsent || n.Now().UnixMilli() < 1750000000000 {
		t.Fatal("fixture state leaked into native host")
	}
	if err = n.TransportOrigin(); err != nil {
		t.Fatal(err)
	}
	n.Environment["ANTHROPIC_BASE_URL"] = "file:///tmp/no"
	if n.TransportOrigin() == nil {
		t.Fatal("invalid transport accepted")
	}
}

func TestProcessNativeCWDMatchesSpawnedSourceProcessThroughAlias(t *testing.T) {
	n := processNativeForTest(t)
	physical, err := filepath.EvalSymlinks(n.CWD)
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(filepath.Dir(n.Home), "workspace-alias")
	if err := os.Symlink(physical, alias); err != nil {
		t.Skipf("workspace symlink unavailable on this host: %v", err)
	}
	native, err := NewProcessNativeBoundary(NativeProcessOptions{Environment: n.Environment, CWD: alias, Platform: "darwin", ManagedRoot: n.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if native.CWD != physical {
		t.Fatalf("native CLI retained a launch alias instead of process cwd: %q want %q", native.CWD, physical)
	}
}
func TestProcessPolicyActualFileDropInPrecedenceAndUnknownFields(t *testing.T) {
	n := processNativeForTest(t)
	drop := filepath.Join(n.ManagedRoot, "managed-settings.d")
	if err := os.MkdirAll(drop, 0700); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"managed-settings.json": `{"env":{"ANTHROPIC_MODEL":"base"},"future":{"base":true}}`, "managed-settings.d/20-last.json": `{"env":{"ANTHROPIC_MODEL":"last"},"future":{"last":true}}`, "managed-settings.d/10-first.json": `{"env":{"ANTHROPIC_MODEL":"first"}}`, "managed-settings.d/.hidden.json": `{"env":{"ANTHROPIC_MODEL":"wrong"}}`} {
		if err := os.WriteFile(filepath.Join(n.ManagedRoot, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	policy, err := n.ResolvePolicy()
	if err != nil {
		t.Fatal(err)
	}
	var env, future protocol.JSONObject
	_ = env.UnmarshalJSON(policy.Get("env"))
	_ = future.UnmarshalJSON(policy.Get("future"))
	if env.String("ANTHROPIC_MODEL") != "last" || string(future.Get("base")) != "true" || string(future.Get("last")) != "true" {
		t.Fatal(policy)
	}
}
func TestProcessPolicyPreservesMDMFailureAndDoesNotReadFirstPartyCacheForDeepSeek(t *testing.T) {
	n := processNativeForTest(t)
	config := n.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "remote-settings.json"), []byte(`{"env":{"ANTHROPIC_MODEL":"wrong-org"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	policy, err := n.ResolvePolicy()
	if err != nil || policy != nil {
		t.Fatal("custom origin inherited first-party cache", policy, err)
	}
	sentinel := errors.New("MDM failure")
	if _, err = n.resolveNativePolicy(func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, sentinel }); !errors.Is(err, sentinel) {
		t.Fatal("native failure swallowed", err)
	}
}
