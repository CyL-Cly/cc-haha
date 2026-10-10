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
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestSkillModelInheritanceAndActualSupportsGetterMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	f, _, options := computerUseMainFixture(t)
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	f.write(t, filepath.Join(f.config, "settings.json"), `{"agentTeamsEnabled":false,"alwaysThinkingEnabled":false,"modelOverrides":{"claude-sonnet-4":"own-sonnet","claude-haiku-4-5":"own-haiku"}}`)
	f.n.Environment["ANTHROPIC_BASE_URL"] = "http://127.0.0.1:1"
	rows := []map[string]string{}
	for _, disabled := range []string{"", "true", "\ufeff YES \ufeff", "off"} {
		for _, model := range []string{"sonnet", "opus", "haiku", "fable", "claude-sonnet-4-20250514", "claude-opus-4-6", "claude-haiku-4-5", "claude-fable-5", "claude-sonnet-5", "claude-opus-5", "own-sonnet", "own-haiku", "deepseek-flash", "sonnet[1m]", "opus:1m"} {
			rows = append(rows, map[string]string{"model": model, "current": "opus[1m]", "disabled": disabled})
		}
	}
	rows = append(rows, map[string]string{"model": "sonnet", "current": "opus[1M]"}, map[string]string{"model": "opus", "current": "sonnet:1M"}, map[string]string{"model": "haiku", "current": "sonnet"})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-skill-tool.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = f.home
	cmd.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_NATIVE_SKILL_TOOL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=fake-native-key", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": f.home, "models": rows})
	cmd.Stdin = bytes.NewReader(data)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.3000s", e, output)
	}
	var source struct {
		Models []struct {
			Result   string
			Supports bool
		}
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(output, &source); e != nil || len(source.Models) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source %v rows=%d blocked=%v %.1000s", e, len(source.Models), source.Capabilities.Blocked, output)
	}
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{ComputerUse: options, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "no model in skill resolver test", Tools: []string{"Read"}, MaxTurns: 1, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	for i, row := range rows {
		if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			f.n.Environment["CLAUDE_CODE_DISABLE_1M_CONTEXT"] = row["disabled"]
			got, e := m.nativeSkillModel(scope, row["model"], row["current"])
			if e != nil {
				return e
			}
			supports, e := (modelaccounting.RestoredModelResolver{Environment: f.n.Environment, Settings: m.Startup.GetInitialSettings}).Supports1M(row["model"])
			if e != nil {
				return e
			}
			if got != source.Models[i].Result || supports != source.Models[i].Supports {
				return fmt.Errorf("model%d %v Go=%q/%t Source=%q/%t", i, row, got, supports, source.Models[i].Result, source.Models[i].Supports)
			}
			return nil
		}); e != nil {
			t.Error(e)
		}
	}
}
