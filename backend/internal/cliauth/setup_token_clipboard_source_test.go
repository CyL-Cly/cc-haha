package cliauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
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
	if os.Getenv("GOOS") == "windows" {
		t.Skip("unix private process fixture")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	os.Mkdir(bin, 0700)
	marker := filepath.Join(root, "started")
	finished := filepath.Join(root, "finished")
	script := "#!/bin/sh\nprintf '%s' \"$$\" > \"$MARKER\"\nexec /bin/sleep 30\n"
	if e := os.WriteFile(filepath.Join(bin, "pbcopy"), []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	c := NewSetupTokenClipboard(context.Background(), "darwin", root, map[string]string{"PATH": bin, "HOME": root, "MARKER": marker, "FINISHED": finished})
	if raw := c.Copy(context.Background(), "owned-url"); !strings.HasPrefix(raw, "\x1b]52;c;") {
		t.Fatal(raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for {
		if _, e := os.Stat(marker); e == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("actual native child not admitted")
		case <-time.After(time.Millisecond):
		}
	}
	start := time.Now()
	c.Close()
	if time.Since(start) > time.Second {
		t.Fatal("native clipboard shutdown did not cancel/join")
	}
	if raw := c.Copy(context.Background(), "late"); raw != "" {
		t.Fatal("copy admitted after Close")
	}
}
