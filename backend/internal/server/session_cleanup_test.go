package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestCleanupPreviewAndMutationPreserveOtherSessionDataAndSymlinks(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	options.Store = storage.New(storage.Options{})
	var err error
	options.Settings, err = settings.New(settings.Options{Store: options.Store, HomeDir: options.HomeDir, ConfigDir: options.ConfigDir, Environment: func() map[string]string { return map[string]string{} }, EnsureMigrated: func(context.Context, string) error { return nil }, ShellEnvironment: func(context.Context) (map[string]string, error) { return map[string]string{}, nil }, GitExecutable: "git"})
	if err != nil {
		t.Fatal(err)
	}
	_, host := testHost(t, options)
	project := filepath.Join(options.ConfigDir, "projects", "old-fixture")
	old := []string{"old.jsonl", "old.cast", "session/subagents/deep/agent.jsonl", "session/tool-results/flat.txt", "session/tool-results/call/result.json"}
	protected := []string{"current.jsonl", "index.json", "session/workflows/old.json", "session/remote-agents/old.json", "session/session-memory/old.md"}
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, relative := range append(append([]string{}, old...), protected...) {
		path := filepath.Join(project, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("source fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		stamp := cutoff.Add(-time.Hour)
		if relative == "current.jsonl" {
			stamp = cutoff.Add(12 * time.Hour)
		}
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	external := filepath.Join(options.HomeDir, "protected-external.jsonl")
	if err := os.WriteFile(external, []byte("private fixture outside projects"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(project, "linked.jsonl")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for _, body := range []string{`{}`, `{"days":null}`, `{"days":"1"}`, `{"days":true}`, `{"days":1.5}`, `{"days":-1}`, `{"days":3651}`} {
		requestJSON(t, host, "POST", "/api/settings/session-cleanup", body, 400)
	}
	requestJSON(t, host, "GET", "/api/settings/session-cleanup", "", 405)
	for _, dryRun := range []bool{true, false} {
		body, _ := json.Marshal(map[string]any{"days": 1, "dryRun": dryRun})
		raw := requestJSON(t, host, "POST", "/api/settings/session-cleanup", string(body), 200)
		var result struct {
			OK     bool
			Days   int
			DryRun bool
			Files  int
			Errors int
		}
		if json.Unmarshal(raw, &result) != nil || !result.OK || result.Days != 1 || result.DryRun != dryRun || result.Files != len(old) || result.Errors != 0 {
			t.Fatalf("unexpected cleanup result %s", raw)
		}
		for _, relative := range old {
			_, err := os.Stat(filepath.Join(project, relative))
			if dryRun && err != nil || !dryRun && !os.IsNotExist(err) {
				t.Fatalf("preview/mutation mismatch for %s: %v", relative, err)
			}
		}
		for _, relative := range protected {
			if _, err := os.Stat(filepath.Join(project, relative)); err != nil {
				t.Fatalf("protected data deleted %s: %v", relative, err)
			}
		}
	}
	if value, err := os.ReadFile(external); err != nil || string(value) != "private fixture outside projects" {
		t.Fatal("cleanup followed symlink", err)
	}
	if _, err := os.Lstat(filepath.Join(project, "linked.jsonl")); err != nil {
		t.Fatal("cleanup removed the symlink", err)
	}
}

func TestCleanupStrictMtimeBoundaryAndAbsentCatalog(t *testing.T) {
	root := t.TempDir()
	cutoff := time.UnixMilli(1_800_000_000_000)
	if got := cleanupSessionFiles(filepath.Join(root, "missing"), cutoff, false); got.files != 0 || got.errors != 0 {
		t.Fatal(got)
	}
	path := filepath.Join(root, "projects", "project", "equal.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("boundary"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, cutoff, cutoff); err != nil {
		t.Fatal(err)
	}
	if got := cleanupSessionFiles(filepath.Join(root, "projects"), cutoff, false); got.files != 0 || got.errors != 0 {
		t.Fatal("mtime equal to cutoff must survive", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
