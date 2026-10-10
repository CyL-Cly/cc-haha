package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfiguredAgentMemoryPromptMatchesActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	for _, tc := range []struct{ scope, text string }{{"user", ""}, {"project", "# Fixture notes\nRemember the real project.\n"}, {"local", " \n\t"}, {"local", "\uFEFF"}, {"user", strings.Repeat("Long memory fixture line.\n", 220)}} {
		t.Run(tc.scope+tc.text[:min(4, len(tc.text))], func(t *testing.T) {
			f := newNativePluginFixture(t)
			f.n.CWD = f.home
			_ = os.MkdirAll(f.n.Temp, 0700)
			f.n.Environment["USERPROFILE"] = f.home
			f.n.Environment["ANTHROPIC_API_KEY"] = "fake-agent-memory-fixture"
			f.write(t, filepath.Join(f.config, "settings.json"), `{}`)
			directory := filepath.Join(f.config, "agent-memory", "fixture-memory")
			if tc.scope == "project" {
				directory = filepath.Join(f.home, ".claude", "agent-memory", "fixture-memory")
			}
			if tc.scope == "local" {
				directory = filepath.Join(f.home, ".claude", "agent-memory-local", "fixture-memory")
			}
			f.write(t, filepath.Join(directory, "MEMORY.md"), tc.text)
			producer, err := NewNativeProcessSettings(f.n, "darwin", DefaultState())
			if err != nil {
				t.Fatal(err)
			}
			defer producer.Close(context.Background())
			if _, err = producer.InitialSettings(); err != nil {
				t.Fatal(err)
			}
			owner := cooperative.New()
			defer owner.Close()
			var prompt string
			err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				var e error
				prompt, e = nativeAgentMemoryPrompt(owner, f.n, producer.startup, scope, "fixture-memory", tc.scope)
				return e
			})
			if err != nil {
				t.Fatal(err)
			}
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-agent-definition.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = f.home
			cmd.Env = nativeAgentProcessEnvironment(f.n)
			cmd.Env = append(cmd.Env, "CC_HAHA_FIXTURE_TEMP_ROOT="+filepath.Dir(f.home), "CC_HAHA_NATIVE_AGENT_DEFINITION_FIXTURE=1", "NODE_ENV=production", "TMPDIR="+f.n.Temp, "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin")
			front := `{"name":"fixture-memory","description":"Memory fixture","memory":"` + tc.scope + `"}`
			data, _ := json.Marshal(map[string]any{"homeDir": f.home, "cases": []map[string]any{{"frontmatter": front, "body": "BODY FIXTURE", "filePath": filepath.Join(f.config, "agents", "fixture.md"), "baseDir": filepath.Join(f.config, "agents"), "source": "userSettings", "hash": "fixture", "memoryEnabled": true, "prompt": true}}})
			cmd.Stdin = bytes.NewReader(data)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("Source memory %v %.1800s", err, stderr.String())
			}
			var source struct {
				Definitions  []struct{ Prompt string }
				Capabilities struct{ Blocked []string }
			}
			if json.Unmarshal(out, &source) != nil || len(source.Definitions) != 1 || len(source.Capabilities.Blocked) > 0 {
				t.Fatalf("Source invalid/escaped %.1800s", out)
			}
			want := "BODY FIXTURE\n\n" + prompt
			if source.Definitions[0].Prompt != want {
				t.Errorf("actual memory prompt diff Go %.2200s Source %.2200s", want, source.Definitions[0].Prompt)
			}
		})
	}
}
