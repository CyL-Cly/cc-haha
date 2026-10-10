package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func contextGit(t *testing.T, f nativePluginFixture, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = f.cwd
	command.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + filepath.Join(f.home, "no-global"), "GIT_AUTHOR_DATE=2026-10-07T00:00:00Z", "GIT_COMMITTER_DATE=2026-10-07T00:00:00Z", "XDG_CONFIG_HOME=" + filepath.Join(f.home, ".config")}
	data, err := command.CombinedOutput()
	if err != nil {
		t.Fatal(args, err, string(data))
	}
	return strings.TrimSpace(string(data))
}

func TestNativeSystemContextAgainstActualSourcePhysicalGit(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Source Bun not configured")
	}
	for _, profile := range []string{"clean", "dirty", "symref", "packed-master", "unsafe-symref", "detached", "unborn", "no-git", "disabled-env", "enabled-env-over-settings", "disabled-settings", "remote", "node-test", "long-status", "empty-name"} {
		t.Run(profile, func(t *testing.T) {
			f := newNativePluginFixture(t)
			f.write(t, filepath.Join(f.config, "settings.json"), `{}`)
			f.n.Environment["ANTHROPIC_API_KEY"] = "offline-system-context-key"
			f.n.Environment["PATH"] = "/usr/bin:/bin"
			f.n.Environment["GIT_CONFIG_NOSYSTEM"] = "1"
			f.n.Environment["GIT_CONFIG_GLOBAL"] = filepath.Join(f.home, "no-global")
			f.n.Environment["XDG_CONFIG_HOME"] = filepath.Join(f.home, ".config")
			f.n.Environment["NODE_ENV"] = "production"
			f.n.Environment["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "true"
			f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "true"
			if profile != "no-git" {
				contextGit(t, f, "init", "-q", "--initial-branch=feature/native-context")
				contextGit(t, f, "config", "user.name", "Private Native Context")
				contextGit(t, f, "config", "user.email", "fixture@example.invalid")
				contextGit(t, f, "config", "core.quotePath", "false")
				if profile != "unborn" {
					f.write(t, filepath.Join(f.cwd, "tracked.txt"), "original\n")
					contextGit(t, f, "add", ".")
					contextGit(t, f, "commit", "-qm", "private context fixture")
				}
			}
			switch profile {
			case "dirty":
				f.write(t, filepath.Join(f.cwd, "tracked.txt"), "actual new content\n")
			case "symref":
				f.write(t, filepath.Join(f.cwd, ".git/refs/remotes/origin/HEAD"), "ref: refs/remotes/origin/release/native\n")
			case "packed-master":
				f.write(t, filepath.Join(f.cwd, ".git/packed-refs"), contextGit(t, f, "rev-parse", "HEAD")+" refs/remotes/origin/master\n")
			case "unsafe-symref":
				f.write(t, filepath.Join(f.cwd, ".git/refs/remotes/origin/HEAD"), "ref: refs/remotes/origin/../escape\n")
			case "detached":
				contextGit(t, f, "checkout", "-q", "--detach")
			case "disabled-env":
				f.n.Environment["CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS"] = " YES "
			case "enabled-env-over-settings":
				f.write(t, filepath.Join(f.config, "settings.json"), `{"includeGitInstructions":false}`)
				f.n.Environment["CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS"] = " off "
			case "disabled-settings":
				f.write(t, filepath.Join(f.config, "settings.json"), `{"includeGitInstructions":false}`)
			case "remote":

			case "node-test":
				f.n.Environment["NODE_ENV"] = "test"
			case "long-status":
				for i := 0; i < 20; i++ {
					f.write(t, filepath.Join(f.cwd, strings.Repeat(string(rune('A'+i)), 110)+"😀.txt"), "actual status")
				}
			case "empty-name":
				contextGit(t, f, "config", "--unset", "user.name")
			}
			m, err := NewMainPrint(MainPrintOptions{Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: runtime.GOOS, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{Model: "deepseek-flash", Tools: []string{}, MaxTurns: 1, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err = m.StartSession(ctx); err != nil {
				t.Fatal(err)
			}
			if profile == "remote" {
				f.n.Environment["CLAUDE_CODE_REMOTE"] = "true"
			}
			var first, repeated protocol.JSONObject
			var main, branch string
			err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				var e error
				first, e = m.nativeSystemContext(scope)
				if e != nil {
					return e
				}
				repeated, e = m.nativeSystemContext(scope)
				if e != nil {
					return e
				}
				mf, e := m.Git.DefaultBranch(scope)
				if e != nil {
					return e
				}
				main, e = mf.Await(scope)
				if e != nil {
					return e
				}
				bf, e := m.Git.Branch(scope)
				if e != nil {
					return e
				}
				branch, e = bf.Await(scope)
				return e
			})
			if err != nil {
				t.Fatal(err)
			}
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-system-context.fixture.ts")
			command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			command.Dir = f.home
			command.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "PATH=/usr/bin:/bin", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CC_HAHA_SYSTEM_CONTEXT_FIXTURE=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home)}
			input, _ := json.Marshal(map[string]any{"home": f.home, "cwd": f.cwd, "environment": f.n.Environment})
			command.Stdin = bytes.NewReader(input)
			var stderr bytes.Buffer
			command.Stderr = &stderr
			data, err := command.Output()
			if err != nil {
				t.Fatal(err, stderr.String())
			}
			var want struct {
				First, Repeated protocol.JSONObject
				Main, Branch    string
				RawStatus       json.RawMessage
			}
			if err = json.Unmarshal(data, &want); err != nil {
				t.Fatal(err, string(data))
			}
			if !reflect.DeepEqual(first.OwnProperties(), want.First.OwnProperties()) || !reflect.DeepEqual(repeated.OwnProperties(), want.Repeated.OwnProperties()) || main != want.Main || branch != want.Branch {
				t.Fatalf("Source mismatch\nfirst=%s\nwant=%s\nmain=%q/%q branch=%q/%q", rawMain(first), rawMain(want.First), main, want.Main, branch, want.Branch)
			}
		})
	}
}
