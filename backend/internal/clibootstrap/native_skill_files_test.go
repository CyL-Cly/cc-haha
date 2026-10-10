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
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

func TestFileOperationsConsumeActualConditionalAndNestedSkillsLikeSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	f, _, options := computerUseMainFixture(t)
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	f.write(t, filepath.Join(f.cwd, ".git", "HEAD"), "ref: refs/heads/main\n")
	f.write(t, filepath.Join(f.cwd, ".claude", "skills", "conditional", "SKILL.md"), "---\ndescription: Conditional file guidance\npaths: src/**\n---\nFollow conditional guidance.")
	f.write(t, filepath.Join(f.cwd, "src", ".claude", "skills", "nested", "SKILL.md"), "---\ndescription: Nested guidance\n---\nFollow nested guidance.")
	file := filepath.Join(f.cwd, "src", "data.txt")
	f.write(t, file, "original\n")
	canonical := filepath.Join(f.n.CWD, "src", "data.txt")
	readInput := func(path string) string {
		return string(marshalPlan(protocol.NewJSONObject(protocol.JSONField{Name: "file_path", Value: quoteMain(path)})))
	}
	steps := []struct{ Tool, Input string }{
		{"Read", readInput(file)},
		{"Read", readInput(canonical)},
		{"Read", readInput(canonical)},
		{"Edit", string(marshalPlan(protocol.NewJSONObject(protocol.JSONField{Name: "file_path", Value: quoteMain(canonical)}, protocol.JSONField{Name: "old_string", Value: quoteMain("original")}, protocol.JSONField{Name: "new_string", Value: quoteMain("edited")})))},
		{"Write", string(marshalPlan(protocol.NewJSONObject(protocol.JSONField{Name: "file_path", Value: quoteMain(filepath.Join(f.n.CWD, "src", "new.txt"))}, protocol.JSONField{Name: "content", Value: quoteMain("written")})))},
	}
	rows := []map[string]string{}
	for _, step := range steps {
		rows = append(rows, map[string]string{"tool": step.Tool, "input": step.Input})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-skill-files.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = f.home
	cmd.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_NATIVE_SKILL_FILES_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CC_HAHA_AGENT_TEAMS_ENABLED=false", "ANTHROPIC_API_KEY=fake-loopback-key", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": f.home, "cwd": f.cwd, "steps": rows, "waitFor": "nested"})
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source %v %.3000s", e, stderr.String())
	}
	var source struct {
		Observations []struct {
			Data     json.RawMessage
			Pending  int
			Triggers []string
		}
		Dynamic      []string
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(output, &source); e != nil || len(source.Observations) != len(steps) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source %v %.3000s", e, output)
	}
	f.write(t, file, "original\n")
	_ = os.Remove(filepath.Join(f.cwd, "src", "new.txt"))
	disabled := false
	changed := make(chan []sdkhost.ControlCommand, 16)
	m, e := NewMainPrint(MainPrintOptions{ComputerUse: options, OnCommandsChanged: func(commands []sdkhost.ControlCommand) error { changed <- commands; return nil }, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "no model in actual skill file consumer test", Tools: []string{"Read,Edit,Write"}, SessionID: "11111111-1111-4111-8111-111111111111", MaxTurns: 4, PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	if e := m.StartSession(ctx); e != nil {
		t.Fatal(e)
	}
	for index, step := range steps {
		if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			var input protocol.JSONObject
			_ = input.UnmarshalJSON([]byte(step.Input))
			_, registered, e := m.nativeTools.Catalog(scope)
			if e != nil {
				return e
			}
			var tool queryloop.RegisteredTool
			for _, candidate := range registered {
				if candidate.Name == step.Tool {
					tool = candidate
					break
				}
			}
			if tool.Name == "" {
				return fmt.Errorf("actual tool %s missing", step.Tool)
			}
			parsed, e := tool.ParseInput(input)
			if e != nil {
				return e
			}
			callContext := queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}}
			if e := tool.ValidateInput(ctx, parsed, callContext.Opaque); e != nil {
				return e
			}
			callResult, e := tool.CallJSON(ctx, parsed, callContext)
			if e != nil {
				return e
			}
			result := callResult.Data
			if !planSourcesEqual(result, source.Observations[index].Data) {
				return fmt.Errorf("step%d actual tool result differs Go=%s Source=%s", index, result, source.Observations[index].Data)
			}
			if m.skillCommands.ConditionalCount() != source.Observations[index].Pending || !slices.Equal(m.dynamicSkillDirTriggers, source.Observations[index].Triggers) {
				return fmt.Errorf("step%d skill activation/triggers differ Go pending=%d dirs=%v Source pending=%d dirs=%v", index, m.skillCommands.ConditionalCount(), m.dynamicSkillDirTriggers, source.Observations[index].Pending, source.Observations[index].Triggers)
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
	}
	for {
		select {
		case commands := <-changed:
			found := false
			for _, command := range commands {
				if command.Name == "nested" {
					found = true
				}
			}
			if found {
				actual := []string{}
				for _, definition := range m.skillCommands.Dynamic() {
					actual = append(actual, definition.Name())
				}
				if !reflect.DeepEqual(actual, source.Dynamic) {
					t.Fatal("actual shared inventory differs", actual, source.Dynamic)
				}
				return
			}
		case <-ctx.Done():
			t.Fatal("shared command menu did not update for native discovered skill")
		}
	}
}
