//go:build windows

package settings

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestActualWindowsBatchGitWrapperPreservesArgumentsAndIgnoreResult(t *testing.T) {
	adapter, repo, environment := gitFixture(t)
	initGit(t, adapter, repo, environment)
	seed(t, filepath.Join(repo, ".gitignore"), ".claude/settings.local.json\n")
	seed(t, filepath.Join(repo, ".claude", "settings.local.json"), `{}`)
	batch := filepath.Join(adapter.options.HomeDir, "wrapper with spaces", "git.cmd")
	seed(t, batch, "@echo off\r\n\""+adapter.options.Executable+"\" %*\r\n")
	adapter.options.Executable = batch
	if !adapter.ignored(context.Background(), ".claude/settings.local.json", repo) {
		t.Fatal("Windows batch wrapper lost git check-ignore arguments")
	}
	if err := adapter.AddFileGlobRule(context.Background(), ".claude/settings.local.json", repo); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(adapter.options.HomeDir, ".config", "git", "ignore")
	if _, err := os.Stat(global); !os.IsNotExist(err) {
		t.Fatal("batch wrapper changed already-ignored registration")
	}
}
