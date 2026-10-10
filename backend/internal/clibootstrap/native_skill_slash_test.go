package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestSharedSkillSlashProducerMatchesActualSourceIncludingNoQuery(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	f, _, options := computerUseMainFixture(t)
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	f.write(t, filepath.Join(f.cwd, ".git", "HEAD"), "ref: refs/heads/main\n")
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	for name, body := range map[string]string{"plain": "---\ndescription: Plain\n---\nUse $ARGUMENTS.", "grant": "---\ndescription: Grant\nallowed-tools: Read Write,Read\nmodel: sonnet\neffort: low\n---\nUse allowed tools.", "modelonly": "---\ndescription: Model-only\nuser-invocable: false\n---\nPrivate model prompt."} {
		f.write(t, filepath.Join(f.cwd, ".claude", "skills", name, "SKILL.md"), body)
	}
	slashes := []string{"/plain literal task", "/grant", "/modelonly", "/agent Explore Inspect this repository\nthen report the result.", "/agent  general-purpose\t preserve quoted 'text' and $ARGUMENTS ", "/agent", "/agent general-purpose", "/agent \ufeff", "/init ignored argument $ARGUMENTS", "/pr-comments", "/pr-comments Extra user detail\nquoted text."}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-skill-tool.fixture.ts")
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = f.home
	cmd.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_NATIVE_SKILL_TOOL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=fake-native-key", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "CC_HAHA_AGENT_TEAMS_ENABLED=false", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": f.home, "cwd": f.cwd, "slashes": slashes})
	cmd.Stdin = bytes.NewReader(data)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.3000s", e, output)
	}
	var source struct {
		Slashes      []protocol.JSONObject
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(output, &source); e != nil || len(source.Slashes) != len(slashes) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source %v rows=%d blocked=%v %.1400s", e, len(source.Slashes), source.Capabilities.Blocked, output)
	}
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{ComputerUse: options, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "no model in slash Source producer test", Tools: []string{"Skill,Read,Write"}, MaxTurns: 1, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	if e = m.StartSession(ctx); e != nil {
		t.Fatal(e)
	}
	for i, input := range slashes {
		parts := strings.SplitN(strings.TrimSpace(input)[1:], " ", 2)
		args := ""
		if len(parts) > 1 {
			args = parts[1]
		}
		if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			got, e := m.nativeProcessSkillPrompt(ctx, scope, parts[0], args, nil, nil)
			if e != nil {
				return e
			}
			projected := []protocol.JSONObject{}
			for _, message := range got.Messages {
				message = message.Clone()
				message.Delete("uuid")
				message.Delete("timestamp")
				projected = append(projected, message)
			}
			actual := protocol.NewJSONObject(protocol.JSONField{Name: "messages", Value: rawMain(projected)}, protocol.JSONField{Name: "shouldQuery", Value: rawMain(!got.NoQuery)})
			if !got.NoQuery {
				if got.Model != "" {
					actual.Set("model", quoteMain(got.Model))
				}
				actual.Set("effort", got.Effort)
				actual.Set("allowedTools", rawMain(got.CommandRules))
			}
			if !planSourcesEqual(rawMain(actual), rawMain(source.Slashes[i])) {
				return fmt.Errorf("slash%d Go=%.1600s Source=%.1600s", i, rawMain(actual), rawMain(source.Slashes[i]))
			}
			return nil
		}); e != nil {
			t.Error(e)
		}
	}
}

func TestModelOnlySlashRecordsActualHistoryWithoutPOSTAndNextHumanStillRuns(t *testing.T) {
	f, _, options := computerUseMainFixture(t)
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	f.write(t, filepath.Join(f.cwd, ".claude", "skills", "modelonly", "SKILL.md"), "---\ndescription: Model only\nuser-invocable: false\n---\nThis must never reach a model from a direct slash.")
	var requests atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"after-local\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture-model\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Continued after local command\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer model.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{ComputerUse: options, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual shared local slash and next-human regression", Tools: []string{"Skill,Read"}, MaxTurns: 2, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if e = m.StartSession(ctx); e != nil {
		t.Fatal(e)
	}
	user := func(content string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "session_id", Value: quoteMain(m.Controller.SessionID())}, protocol.JSONField{Name: "message", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoteMain("user")}, protocol.JSONField{Name: "content", Value: quoteMain(content)}))})
	}
	if e = m.RunTurn(ctx, user("/modelonly")); e != nil {
		t.Fatal(e)
	}
	if requests.Load() != 0 {
		t.Fatal("model-only slash posted to actual upstream", requests.Load())
	}
	if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		history, e := m.Host.OriginalHistoryOnOwner(scope)
		if e != nil {
			return e
		}
		users := 0
		for _, message := range history {
			if message.Type() == "user" {
				users++
			}
			if message.Type() == "assistant" {
				return fmt.Errorf("local guard invented an assistant")
			}
		}
		if users != 3 {
			return fmt.Errorf("source local user/caveat records missing: %d", users)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e = m.RunTurn(ctx, user("Continue normal conversation")); e != nil {
		t.Fatal(e)
	}
	if requests.Load() != 1 {
		t.Fatal("next human did not run exactly once", requests.Load())
	}
	if e = m.ShutdownSession(ctx); e != nil {
		t.Fatal(e)
	}
	files, e := filepath.Glob(filepath.Join(f.config, "projects", "*", "*.jsonl"))
	if e != nil || len(files) != 1 {
		t.Fatal("actual transcript missing", files, e)
	}
	disk, e := os.ReadFile(files[0])
	if e != nil || !bytes.Contains(disk, []byte("local-command-caveat")) || !bytes.Contains(disk, []byte("Continued after local command")) {
		t.Fatal("actual local/next-human records missing", e)
	}
}
