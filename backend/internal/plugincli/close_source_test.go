package plugincli_test

import (
	"bytes"
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/plugincli"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCLIPluginCloseJoinsActualLocalGitProducer(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	config := filepath.Join(home, ".claude")
	bin := filepath.Join(home, "bin")
	for _, dir := range []string{config, bin, filepath.Join(home, ".git")} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(home, "child.pid")
	fifo := filepath.Join(home, "hold.fifo")
	if err = syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nif [ \"$1\" = check-ignore ]; then\n  printf '%s' \"$$\" > '" + marker + "'\n  exec /bin/cat '" + fifo + "'\nfi\nexec /usr/bin/git \"$@\"\n"
	if err = os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"future":{"keep":true},"enabledPlugins":{"alpha@fixture":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": home, "USERPROFILE": home, "USER": "owned", "CLAUDE_CONFIG_DIR": config, "TMPDIR": home, "PATH": bin + ":/usr/bin:/bin", "USER_TYPE": "external", "NODE_ENV": "production", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"}
	native, err := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: home, Platform: "darwin", ManagedRoot: filepath.Join(root, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	command, err := clibootstrap.NewMainCommand(clibootstrap.MainCommandOptions{Arguments: []string{"plugin", "disable", "alpha@fixture", "--scope", "local"}, Native: native, Platform: "darwin", PID: os.Getpid(), Output: &bytes.Buffer{}, Error: &bytes.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	defer command.Close()
	done := make(chan error, 1)
	go func() {
		done <- command.Run(context.Background(), func(scope *cooperative.Scope) error {
			_, err := command.PluginCommand(context.Background(), scope, plugincli.Command{Action: "disable", Argument: "alpha@fixture", Scope: "local"})
			return err
		})
	}()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	var pid int
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for pid == 0 {
		select {
		case <-deadline.C:
			t.Fatal("actual owned GitIgnore child did not start")
		case err := <-done:
			t.Fatal("producer ended before held child", err)
		case <-ticker.C:
			data, e := os.ReadFile(marker)
			if e == nil {
				pid, _ = strconv.Atoi(string(data))
			}
		}
	}
	// Source starts its GitIgnore only after committing the local settings file.
	data, err := os.ReadFile(filepath.Join(config, "settings.local.json"))
	if err != nil || !strings.Contains(string(data), `"alpha@fixture": false`) {
		t.Fatal("held child preceded physical settings commit", err, string(data))
	}
	closed := make(chan struct{})
	go func() { command.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatal("Close failed to join actual held child")
	}
	select {
	case err := <-done:
		if !errors.Is(err, cooperative.ErrClosed) {
			t.Fatalf("owner close result %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("owner command did not settle")
	}
	if err = syscall.Kill(pid, 0); err == nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatal("actual GitIgnore child survived Close")
	}
	if err = command.Run(context.Background(), func(*cooperative.Scope) error { return nil }); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal("closed owner readmitted CLI command", err)
	}
}
