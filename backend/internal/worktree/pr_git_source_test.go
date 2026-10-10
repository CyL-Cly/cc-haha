package worktree

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestActualSourceLargePRPhysicalLocalFetchAndValidation(t *testing.T) {
	for _, reference := range []string{"#9007199254740993", "#9223372036854775807", "#18446744073709551615", "#1000000000000000000000", "#" + strings.Repeat("9", 309)} {
		t.Run(reference[:min(len(reference), 25)], func(t *testing.T) {
			home := t.TempDir()
			if e := os.MkdirAll(filepath.Join(home, ".claude"), 0700); e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			fixture, _ := filepath.Abs("testdata/pr-git.fixture.ts")
			data, _ := json.Marshal(map[string]any{"home": home, "reference": reference, "refNumber": strings.TrimPrefix(reference, "#")})
			cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
			cmd.Stdin = bytes.NewReader(data)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
			out, e := cmd.CombinedOutput()
			if e != nil {
				t.Fatal(e, string(out))
			}
			type call struct {
				Args     []string
				NoPrompt bool
			}
			var source struct {
				Number, Slug string
				Error, Bytes *string
				Calls        []call
				Capabilities struct{ Blocked []string }
			}
			if e = json.Unmarshal(out, &source); e != nil || len(source.Capabilities.Blocked) > 0 {
				t.Fatal(e, string(out))
			}
			service, root, env := privateGit(t)
			number, e := ParsePRReference(reference)
			if e != nil || number == nil {
				t.Fatal(e)
			}
			slug := "pr-" + PRNumberString(*number)
			run := func(args ...string) {
				t.Helper()
				r, e := service.run(ctx, root, args, true)
				if e != nil || r.Code != 0 {
					t.Fatal(args, r, e)
				}
			}
			if source.Error == nil {
				run("update-ref", "refs/pull/"+PRNumberString(*number)+"/head", "HEAD")
			}
			origin := filepath.Join(filepath.Dir(root), "origin.git")
			run("clone", "--bare", root, origin)
			if source.Error == nil {
				run("--git-dir", origin, "update-ref", "refs/pull/"+PRNumberString(*number)+"/head", "refs/heads/main")
			}
			run("remote", "add", "origin", origin)
			actualCalls := []call{}
			service.options.Run = func(ctx context.Context, r ProcessRequest) (ProcessResult, error) {
				actualCalls = append(actualCalls, call{r.Arguments, r.IgnoreStdin && r.Environment["GIT_TERMINAL_PROMPT"] == "0" && r.Environment["GIT_ASKPASS"] == ""})
				return NativeRun(ctx, r)
			}
			result, e := service.Create(ctx, Request{CWD: root, SessionID: "11111111-1111-4111-8111-111111111111", Slug: slug, PRNumber: number})
			if source.Number != PRNumberString(*number) || source.Slug != slug {
				t.Fatal("caller number or slug mismatch", source.Number, slug)
			}
			if (source.Error == nil) != (e == nil) {
				t.Fatalf("Source error=%v;Go=%v", source.Error, e)
			}
			if source.Error != nil {
				if *source.Error != e.Error() {
					t.Fatalf("original failure Source%q Go%q", *source.Error, e.Error())
				}
			} else {
				b, err := os.ReadFile(filepath.Join(result.Path, "tracked.txt"))
				if err != nil || source.Bytes == nil || string(b) != *source.Bytes {
					t.Fatal("real git byte mismatch", err, string(b), source.Bytes)
				}
			}
			if reference == "#9007199254740993" || reference == "#9223372036854775807" || reference == "#18446744073709551615" {
				if source.Error != nil || source.Bytes == nil {
					t.Fatal("valid Source-rounded local PR did not physically create", source.Error)
				}
			}
			onlyFetch := func(calls []call) []call {
				result := []call{}
				for _, c := range calls {
					if len(c.Args) > 0 && c.Args[0] == "fetch" {
						result = append(result, c)
					}
				}
				return result
			}
			if !reflect.DeepEqual(onlyFetch(source.Calls), onlyFetch(actualCalls)) {
				t.Fatalf("actual original fetch mismatch Source%v Go%v", source.Calls, actualCalls)
			}
			_ = env
		})
	}
}
