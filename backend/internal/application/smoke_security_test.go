package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestPrivateSmokeOSCredentialsRequireRealContainedPaths(t *testing.T) {
	if callback, err := SmokeSecurity(map[string]string{}); err != nil || callback != nil {
		t.Fatal("ordinary production credential primitive changed", err)
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	environment := map[string]string{"CC_HAHA_ELECTRON_SMOKE_STATE_ROOT": root, "HOME": filepath.Join(root, "home"), "USERPROFILE": filepath.Join(root, "home"), "CLAUDE_CONFIG_DIR": filepath.Join(root, "home", ".claude"), "CC_HAHA_ELECTRON_WINDOW_SMOKE_LOG": filepath.Join(root, "window.ndjson")}
	callback, err := SmokeSecurity(environment)
	if err != nil || callback == nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"find-generic-password", "add-generic-password", "delete-generic-password"} {
		if _, err = callback(context.Background(), securestorage.Command{Args: []string{operation}, Input: "fake-only"}); !errors.Is(err, os.ErrPermission) {
			t.Fatal("native credential process was admitted", operation, err)
		}
	}
	outside := t.TempDir()
	homeKey := "HOME"
	if runtime.GOOS == "windows" {
		homeKey = "USERPROFILE"
	}
	for _, key := range []string{homeKey, "CLAUDE_CONFIG_DIR", "CC_HAHA_ELECTRON_WINDOW_SMOKE_LOG"} {
		copy := map[string]string{}
		for k, v := range environment {
			copy[k] = v
		}
		copy[key] = filepath.Join(outside, "escape")
		if _, err := SmokeSecurity(copy); err == nil {
			t.Fatal("external credential path admitted", key)
		}
	}
	link := filepath.Join(root, "escaped-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	environment["CLAUDE_CONFIG_DIR"] = filepath.Join(link, ".claude")
	if _, err := SmokeSecurity(environment); err == nil {
		t.Fatal("symlink escaped private credential root")
	}
}
