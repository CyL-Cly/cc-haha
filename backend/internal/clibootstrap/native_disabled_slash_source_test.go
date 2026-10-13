package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestActualSourceDisabledUserSlashKeepsModelSkillsAcrossRefresh(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	f, _, options := computerUseMainFixture(t)
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	f.write(t, filepath.Join(f.cwd, ".claude", "skills", "private-skill", "SKILL.md"), "---\ndescription: Private preserved model skill\n---\nPRIVATE_MODEL_SKILL_BODY")
	slashes := []string{"/private-skill argument 中文", "/compact ignored", "/cost", "/model sonnet", "/goal list"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-skill-tool.fixture.ts")
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = f.home
	cmd.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_NATIVE_SKILL_TOOL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=fake-native-key", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": f.home, "cwd": f.cwd, "slashes": slashes, "disableSlashCommands": true})
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var source struct {
		Slashes      []protocol.JSONObject
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Slashes) != len(slashes) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(out))
	}
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{ComputerUse: options, Arguments: PrintArguments{DisableSlashCommands: true, ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "private disabled user commands", Tools: []string{"Skill,Read"}, MaxTurns: 2, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer m.ShutdownSession(context.Background())
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	for i, input := range slashes {
		parts := strings.SplitN(input[1:], " ", 2)
		args := ""
		if len(parts) > 1 {
			args = parts[1]
		}
		err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			got, e := m.nativeProcessSkillPrompt(ctx, scope, parts[0], args, nil, nil)
			if e != nil {
				return e
			}
			records := []protocol.JSONObject{}
			for _, record := range got.Messages {
				record = record.Clone()
				record.Delete("uuid")
				record.Delete("timestamp")
				records = append(records, record)
			}
			actual := protocol.NewJSONObject(protocol.JSONField{Name: "messages", Value: rawMain(records)}, protocol.JSONField{Name: "shouldQuery", Value: rawMain(!got.NoQuery)}, protocol.JSONField{Name: "resultText", Value: quoteMain(got.NoQueryResultText)})
			if !planSourcesEqual(rawMain(actual), rawMain(source.Slashes[i])) {
				return fmt.Errorf("Source disabled slash %d differs: Go %.1500s Source %.1500s", i, rawMain(actual), rawMain(source.Slashes[i]))
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	f.write(t, filepath.Join(f.cwd, ".claude", "skills", "private-skill", "SKILL.md"), "---\ndescription: Private preserved model skill updated\n---\nPRIVATE_MODEL_SKILL_BODY_UPDATED")
	if err = m.reloadNativeSkillCommands(ctx, filepath.Join(f.cwd, ".claude", "skills", "private-skill", "SKILL.md"), false); err != nil {
		t.Fatal(err)
	}
	if len(m.Registry.Catalog().Commands) != 0 || len(m.Registry.Catalog().Skills) != 0 || len(m.Registry.ControlCommands()) != 0 {
		t.Fatal("user command inventory is still enabled")
	}
	err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		_, tools, e := m.nativeTools.Catalog(scope)
		if e != nil {
			return e
		}
		var skill queryloop.RegisteredTool
		for _, tool := range tools {
			if tool.Name == "Skill" {
				skill = tool
			}
		}
		input := protocol.NewJSONObject(protocol.JSONField{Name: "skill", Value: quoteMain("private-skill")})
		parsed, e := skill.ParseInput(input)
		if e != nil {
			return e
		}
		call := &builtinwrite.CallContext{Scope: scope, MainLoopModel: "fixture-model"}
		if e = skill.ValidateInput(ctx, parsed, call); e != nil {
			return e
		}
		result, e := skill.CallJSON(ctx, parsed, queryloop.JSONToolCallContext{Opaque: call})
		if e != nil {
			return e
		}
		if !bytes.Contains(rawMain(result.NewMessages), []byte("PRIVATE_MODEL_SKILL_BODY_UPDATED")) {
			return fmt.Errorf("model skill was incorrectly disabled with user commands")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
