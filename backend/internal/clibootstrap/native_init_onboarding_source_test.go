package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

func TestOriginalInitProjectOnboardingUsesActualSharedConfigAndFiles(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	cases := []struct{ name, path, completed string }{{"nonempty-missing", "", `false`}, {"file", "file", `false`}, {"directory", "directory", `false`}, {"cached-false-missing", "", `null`}, {"cached-truthy-skip", "", `"already"`}, {"cached-object-skip", "", `{}`}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := worktreeMainFixture(t)
			f.write(t, filepath.Join(f.config, "settings.json"), `{"agentTeamsEnabled":false,"alwaysThinkingEnabled":false,"enabledPlugins":{"sample@mkt":false}}`)
			project := protocol.NewJSONObject(protocol.JSONField{Name: "futureProject", Value: quoteMain("preserved")}, protocol.JSONField{Name: "hasCompletedProjectOnboarding", Value: json.RawMessage(tc.completed)})
			f.write(t, filepath.Join(f.config, ".claude.json"), string(rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "hasCompletedOnboarding", Value: json.RawMessage("true")}, protocol.JSONField{Name: "futureGlobal", Value: quoteMain("preserved")}, protocol.JSONField{Name: "projects", Value: rawMain(map[string]any{f.cwd: project})}))))
			if tc.path == "file" {
				f.write(t, filepath.Join(f.cwd, "CLAUDE.md"), "existing")
			}
			if tc.path == "directory" {
				if e := os.MkdirAll(filepath.Join(f.cwd, "CLAUDE.md"), 0700); e != nil {
					t.Fatal(e)
				}
			}
			disabled := false
			m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "no model for actual init onboarding", Tools: []string{"Read"}, MaxTurns: 1, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			if e = m.StartSession(ctx); e != nil {
				t.Fatal(e)
			}
			var got *protocol.JSONObject
			if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				if e := m.nativeMaybeMarkProjectOnboardingComplete(ctx, skillcommands.Invocation{NativeScope: scope}); e != nil {
					return e
				}
				key, e := m.originalProjectConfigKey(scope)
				if e != nil {
					return e
				}
				got, e = m.Global.GetProject(key)
				return e
			}); e != nil {
				t.Fatal(e)
			}
			if e = m.ShutdownSession(ctx); e != nil {
				t.Fatal(e)
			}
			sourceHome := t.TempDir()
			sourceCWD := filepath.Join(sourceHome, "repo")
			if e = os.MkdirAll(filepath.Join(sourceCWD, ".git"), 0700); e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(sourceCWD, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0600); e != nil {
				t.Fatal(e)
			}
			if tc.path == "file" {
				if e = os.WriteFile(filepath.Join(sourceCWD, "CLAUDE.md"), []byte("existing"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			if tc.path == "directory" {
				if e = os.MkdirAll(filepath.Join(sourceCWD, "CLAUDE.md"), 0700); e != nil {
					t.Fatal(e)
				}
			}
			fixture, _ := filepath.Abs("../skillcommands/testdata/builtin_init.fixture.ts")
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = sourceHome
			cmd.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + sourceHome, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(sourceHome), "CC_HAHA_BUILTIN_INIT_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
			input, _ := json.Marshal(map[string]any{"home": sourceHome, "cwd": sourceCWD, "project": project, "rows": []map[string]string{{"args": "", "userType": "external", "flag": ""}}})
			cmd.Stdin = bytes.NewReader(input)
			out, e := cmd.CombinedOutput()
			if e != nil {
				t.Fatalf("Source project %v %.4000s", e, out)
			}
			var source struct{ Project protocol.JSONObject }
			if e = json.Unmarshal(out, &source); e != nil {
				t.Fatal(e, string(out))
			}
			if !planSourcesEqual(rawMain(got), rawMain(source.Project)) {
				t.Fatal(fmt.Errorf("actual Go shared project%s Source%s", rawMain(got), rawMain(source.Project)))
			}
			physical, e := os.ReadFile(filepath.Join(f.config, ".claude.json"))
			if e != nil {
				t.Fatal(e)
			}
			var global protocol.JSONObject
			_ = global.UnmarshalJSON(physical)
			if global.String("futureGlobal") != "preserved" || got.String("futureProject") != "preserved" {
				t.Fatalf("old unknown config was overwritten config=%s home=%s global=%s project=%s", f.config, f.home, physical, rawMain(got))
			}
		})
	}
}
