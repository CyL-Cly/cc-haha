package cliauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestSetupTokenClipboardActualSourcePlatformMultiplexerAndFailurePaths(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Source Bun unavailable")
	}
	fixture, _ := filepath.Abs("../../testdata/native-setup-token-input.fixture.ts")
	for _, row := range []struct {
		platform string
		env      map[string]string
		codes    []int
	}{
		{"darwin", map[string]string{}, nil},
		{"darwin", map[string]string{"SSH_CONNECTION": "owned"}, nil},
		{"darwin", map[string]string{"SSH_CONNECTION": "owned", "TERM": "xterm-kitty"}, nil},
		{"darwin", map[string]string{"SSH_CONNECTION": "owned", "TERM": "xterm-kitty", "TERM_PROGRAM": "vscode"}, nil},
		{"darwin", map[string]string{"SSH_CONNECTION": "owned", "TMUX": "owned"}, nil},
		{"darwin", map[string]string{"TMUX": "owned"}, nil},
		{"darwin", map[string]string{"TMUX": "owned"}, []int{1, 0}},
		{"darwin", map[string]string{"SSH_CONNECTION": "owned", "TMUX": "owned", "LC_TERMINAL": "iTerm2"}, nil},
		{"darwin", map[string]string{"SSH_CONNECTION": "owned", "TMUX": "owned"}, []int{1}},
		{"linux", map[string]string{}, []int{0}},
		{"linux", map[string]string{}, []int{1, 0}},
		{"linux", map[string]string{}, []int{1, 1, 0}},
		{"linux", map[string]string{}, []int{1, 1, 1}},
		{"windows", map[string]string{}, nil},
	} {
		t.Run(fmt.Sprintf("%s/%v/%v", row.platform, row.env, row.codes), func(t *testing.T) {
			root, e := filepath.EvalSymlinks(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			home := filepath.Join(root, "home")
			os.Mkdir(home, 0700)
			os.Mkdir(filepath.Join(home, ".claude"), 0700)
			input, _ := json.Marshal(map[string]any{"platform": map[string]string{"windows": "win32"}[row.platform], "env": row.env, "codes": row.codes, "text": "owned-url-中文"})
			// Non-Windows names are the actual Node platform string.
			if row.platform != "windows" {
				input, _ = json.Marshal(map[string]any{"platform": row.platform, "env": row.env, "codes": row.codes, "text": "owned-url-中文"})
			}
			command := exec.Command(bun, "--no-env-file", fixture)
			command.Dir = home
			command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + root, "PATH=/usr/bin:/bin", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "NODE_ENV=test", "BUN_OPTIONS=--no-env-file"}
			command.Stdin = bytes.NewReader(input)
			out, e := command.CombinedOutput()
			if e != nil {
				t.Fatal(e, string(out))
			}
			var want struct {
				Raw   string
				Calls []map[string]any
			}
			if e = json.Unmarshal(out, &want); e != nil {
				t.Fatal(e, string(out))
			}
			c := NewSetupTokenClipboard(context.Background(), row.platform, home, row.env)
			var mu sync.Mutex
			calls := []map[string]any{}
			c.Run = func(ctx context.Context, name string, args []string, text string) int {
				mu.Lock()
				defer mu.Unlock()
				i := len(calls)
				if args == nil {
					args = []string{}
				}
				calls = append(calls, map[string]any{"name": name, "args": args, "input": text, "useCwd": false, "timeout": float64(2000)})
				if i < len(row.codes) {
					return row.codes[i]
				}
				return 0
			}
			got := c.Copy(context.Background(), "owned-url-中文")
			c.Close()
			// JSON normalizes only Go's []string representation, not values or order.
			encoded, _ := json.Marshal(calls)
			var normalized []map[string]any
			json.Unmarshal(encoded, &normalized)
			if got != want.Raw || !reflect.DeepEqual(normalized, want.Calls) {
				t.Fatalf("Source clipboard\ngot %q %#v\nwant %q %#v", got, normalized, want.Raw, want.Calls)
			}
		})
	}
}
func TestSetupTokenClipboardCloseJoinsActualNativeProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix private process fixture")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if e := os.Mkdir(bin, 0700); e != nil {
		t.Fatal(e)
	}
	marker := filepath.Join(root, "started")
	finished := filepath.Join(root, "finished")
	script := "#!/bin/sh\nprintf '%s' \"$$\" > \"$MARKER\"\nexec /bin/sleep 30\n"
	if e := os.WriteFile(filepath.Join(bin, "pbcopy"), []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	c := NewSetupTokenClipboard(context.Background(), "darwin", root, map[string]string{"PATH": bin, "HOME": root, "MARKER": marker, "FINISHED": finished})
	defer c.Close()
	observed := make(chan setupTokenClipboardActualStart, 1)
	c.afterActualStart = func(command *exec.Cmd, e error) {
		observed <- setupTokenClipboardActualStart{command: command, err: e}
	}
	if raw := c.Copy(context.Background(), "owned-url"); !strings.HasPrefix(raw, "\x1b]52;c;") {
		t.Fatal(raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var actual setupTokenClipboardActualStart
	select {
	case actual = <-observed:
	case <-ctx.Done():
		t.Fatal("actual native child not admitted: no actual Start observation")
	}
	command := setupTokenClipboardRequireLiveStart(t, actual)
	pid := command.Process.Pid
	start := time.Now()
	c.Close()
	if time.Since(start) > time.Second {
		t.Fatal("native clipboard shutdown did not cancel/join")
	}
	setupTokenClipboardRequireJoinedPID(t, command, pid)
	if raw := c.Copy(context.Background(), "late"); raw != "" {
		t.Fatal("copy admitted after Close")
	}
	// This is only a shell-progress diagnostic, not the OS Start oracle.
	data, e := os.ReadFile(marker)
	t.Logf("actual native clipboard pid=%d waited=%v marker=%q markerError=%v", pid, command.ProcessState, data, e)
}

type setupTokenClipboardActualStart struct {
	command *exec.Cmd
	err     error
}

func setupTokenClipboardRequireLiveStart(t *testing.T, actual setupTokenClipboardActualStart) *exec.Cmd {
	t.Helper()
	if actual.err != nil {
		t.Fatalf("actual native clipboard Start failed: %v", actual.err)
	}
	command := actual.command
	if command == nil || command.Process == nil || command.Process.Pid <= 0 {
		t.Fatal("actual native clipboard Start did not return a real process")
	}
	if e := command.Process.Signal(syscall.Signal(0)); e != nil {
		t.Fatalf("actual native clipboard pid=%d not alive before Close: %v", command.Process.Pid, e)
	}
	return command
}

func setupTokenClipboardRequireJoinedPID(t *testing.T, command *exec.Cmd, pid int) {
	t.Helper()
	// Call only after c.Close has joined its worker; Wait writes ProcessState.
	if command.ProcessState == nil || command.ProcessState.Pid() != pid {
		t.Fatalf("actual native clipboard pid=%d has no joined Wait state: %v", pid, command.ProcessState)
	}
	if e := command.Process.Signal(syscall.Signal(0)); !errors.Is(e, os.ErrProcessDone) {
		t.Fatalf("actual native clipboard pid=%d original process not done after Close/join: %v", pid, e)
	}
}

func TestSetupTokenClipboardActualStartObserverDoesNotUseShellMarker(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix private process fixture")
	}
	for _, name := range []string{"real-start-before-marker", "actual-start-error"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			if e := os.Mkdir(bin, 0700); e != nil {
				t.Fatal(e)
			}
			marker := filepath.Join(root, "started")
			// The real shell cannot reach the marker until continued. Close must
			// kill/join this same process while it is still before that write.
			script := "#!/bin/sh\nkill -STOP \"$$\"\nprintf '%s' \"$$\" > \"$MARKER\"\nexec /bin/sleep 30\n"
			if name == "actual-start-error" {
				script = "#!" + filepath.Join(root, "absent-interpreter") + "\n"
			}
			if e := os.WriteFile(filepath.Join(bin, "pbcopy"), []byte(script), 0700); e != nil {
				t.Fatal(e)
			}
			c := NewSetupTokenClipboard(context.Background(), "darwin", root, map[string]string{"PATH": bin, "HOME": root, "MARKER": marker})
			defer c.Close()
			observed := make(chan setupTokenClipboardActualStart, 1)
			c.afterActualStart = func(command *exec.Cmd, e error) {
				observed <- setupTokenClipboardActualStart{command: command, err: e}
			}
			if raw := c.Copy(context.Background(), "owned-url"); !strings.HasPrefix(raw, "\x1b]52;c;") {
				t.Fatal(raw)
			}
			var actual setupTokenClipboardActualStart
			select {
			case actual = <-observed:
			default:
				t.Fatal("Copy returned without observing the real Start result")
			}
			if name == "actual-start-error" {
				if !errors.Is(actual.err, os.ErrNotExist) || actual.command == nil || actual.command.Process != nil {
					t.Fatalf("real Start failure was not captured: command=%v error=%v", actual.command, actual.err)
				}
			} else {
				command := setupTokenClipboardRequireLiveStart(t, actual)
				if _, e := os.Stat(marker); !errors.Is(e, os.ErrNotExist) {
					t.Fatalf("shell marker unexpectedly exists before Close: %v", e)
				}
				start := time.Now()
				c.Close()
				if time.Since(start) > time.Second {
					t.Fatal("native clipboard shutdown did not cancel/join")
				}
				setupTokenClipboardRequireJoinedPID(t, command, command.Process.Pid)
			}
			c.Close()
			if raw := c.Copy(context.Background(), "late"); raw != "" {
				t.Fatal("copy admitted after Close")
			}
		})
	}
}
