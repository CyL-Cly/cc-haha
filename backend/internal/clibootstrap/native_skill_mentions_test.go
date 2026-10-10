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
)

func TestSkillFileMentionsUseActualSharedReadCacheRangesAndDirectoryEnumeration(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	f, _, options := computerUseMainFixture(t)
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	f.write(t, filepath.Join(f.cwd, ".git", "HEAD"), "ref: refs/heads/main\n")
	f.write(t, filepath.Join(f.cwd, ".claude", "settings.json"), `{"permissions":{"deny":["Read(secret.txt)"]}}`)
	f.write(t, filepath.Join(f.cwd, "plain.txt"), "alpha\nbeta\ngamma\n")
	f.write(t, filepath.Join(f.cwd, "secret.txt"), "must not attach")
	f.write(t, filepath.Join(f.cwd, "my file.txt"), "spaces")
	f.write(t, filepath.Join(f.cwd, "directory", "zeta"), "last")
	f.write(t, filepath.Join(f.cwd, "directory", "alpha"), "first")
	inputs := []string{"ordinary mail@local", "@plain.txt#L2-3", "@plain.txt", "@plain.txt", "@secret.txt @missing.txt", `@"my file.txt" @directory`, "@plain.txt#L0-0"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-skill-tool.fixture.ts")
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = f.home
	cmd.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_NATIVE_SKILL_TOOL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=fake-native-key", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "CC_HAHA_AGENT_TEAMS_ENABLED=false", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": f.home, "cwd": f.cwd, "fileMentions": inputs})
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source %v %.2500s", e, stderr.String())
	}
	var source struct {
		FileMentions []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(output, &source); e != nil || len(source.FileMentions) != len(inputs) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source %v rows%d blocked%v %.2000s", e, len(source.FileMentions), source.Capabilities.Blocked, output)
	}
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{ComputerUse: options, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "zero models in real mention producer test", Tools: []string{"Skill,Read"}, MaxTurns: 1, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	if e = m.StartSession(ctx); e != nil {
		t.Fatal(e)
	}
	for i, text := range inputs {
		if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			got, e := m.nativeSkillMentions(ctx, scope, text)
			if e != nil {
				return e
			}
			payloads := []json.RawMessage{}
			for _, value := range got {
				payloads = append(payloads, value.Record.Get("attachment"))
			}
			if !planSourcesEqual(rawMain(payloads), source.FileMentions[i]) {
				return fmt.Errorf("mention%d Go=%.2000s Source=%.2000s", i, rawMain(payloads), source.FileMentions[i])
			}
			return nil
		}); e != nil {
			t.Error(e)
		}
	}
	// Validate that the accepted attachment read updates the very cache used by
	// the real Write tool rather than a separate prompt-only authorization map.
	if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		return m.nativeTools.Validate(ctx, "Write", protocol.NewJSONObject(protocol.JSONField{Name: "file_path", Value: quoteMain(filepath.Join(f.n.CWD, "my file.txt"))}, protocol.JSONField{Name: "content", Value: quoteMain("changed")}), scope)
	}); e != nil {
		t.Fatal("actual shared Write validation did not observe attachment Read", e)
	}
}
