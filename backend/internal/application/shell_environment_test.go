package application

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestNativeShellCaptureRetainsMarkerValuesAndProcessPrecedence(t *testing.T) {
	if filepath.Separator == '\\' {
		t.Skip("native POSIX shell fixture")
	}
	root := t.TempDir()
	shell := filepath.Join(root, "fixture-bash")
	count := filepath.Join(root, "count")
	// Use the actual signed login shell: macOS can spend more than Source's
	// five-second deadline assessing newly generated executable script fixtures.
	if err := os.Symlink("/bin/bash", shell); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".bash_profile"), []byte("printf x >> \"$HOME/count\"\nprintf 'login greeting\\n'\nexport PATH=/shell/bin:/shared/bin SHELL_VALUE=a=b KEEP=shell\nenv() { /usr/bin/env \"$@\"; }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	environment := newTerminalEnvironment(map[string]string{"HOME": root, "SHELL": shell, "PATH": "/shared/bin:/process/bin", "KEEP": "process", "BASH_ENV": "should-be-removed", "ENV": "should-be-removed"})
	environment.tty = false
	var waits sync.WaitGroup
	waits.Add(8)
	for i := 0; i < 8; i++ {
		go func() {
			defer waits.Done()
			values, err := environment.Get(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			if values["PATH"] != "/shell/bin:/shared/bin:/process/bin" || values["SHELL_VALUE"] != "a=b" || values["KEEP"] != "process" {
				t.Error(values)
			}
		}()
	}
	waits.Wait()
	data, err := os.ReadFile(count)
	if err != nil || string(data) != "x" {
		t.Fatal("shell producer did not memoize", string(data), err)
	}
}

func TestNativeShellCaptureDisableAndWindowsPreserveFrozenEnv(t *testing.T) {
	for _, platform := range []string{"windows", "darwin"} {
		environment := newTerminalEnvironment(map[string]string{"HOME": t.TempDir(), "KEEP": "frozen", "CC_HAHA_DISABLE_TERMINAL_SHELL_ENV": "true"})
		environment.platform = platform
		values, err := environment.Get(context.Background())
		if err != nil || !reflect.DeepEqual(values, environment.base) {
			t.Fatal(values, err)
		}
	}
	if got := mergeShellPaths(";", " C:\\one ;C:\\two;C:\\one", "C:\\two;C:\\three"); got != "C:\\one;C:\\two;C:\\three" {
		t.Fatal(got)
	}
}
