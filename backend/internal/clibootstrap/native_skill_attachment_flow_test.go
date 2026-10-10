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

func TestActualSkillAttachmentFlowDefaultCommandsDynamicDirectoriesAndAgentState(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual pinned Source required")
	}
	f, _, options := computerUseMainFixture(t)
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	delete(f.n.Environment, "CLAUDE_CODE_DISABLE_ATTACHMENTS")
	f.n.Environment["NODE_ENV"] = "production"
	f.write(t, filepath.Join(f.cwd, ".git", "HEAD"), "ref: refs/heads/main\n")
	f.write(t, filepath.Join(f.cwd, ".claude", "skills", "plain", "SKILL.md"), "---\ndescription: Local skill from the same real loader\n---\nUse local guidance.")
	f.write(t, filepath.Join(f.cwd, ".claude", "skills", "hidden", "SKILL.md"), "---\ndescription: Hidden listing\ndisable-model-invocation: true\n---\nHidden guidance.")
	dir := filepath.Join(f.n.CWD, "discovered", ".claude", "skills")
	f.write(t, filepath.Join(dir, "alpha", "SKILL.md"), "Alpha body")
	f.write(t, filepath.Join(dir, "zeta", "SKILL.md"), "Zeta body")
	f.write(t, filepath.Join(dir, "ordinary-file"), "not a directory")
	if e := os.MkdirAll(filepath.Join(dir, "empty"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(filepath.Join(dir, "alpha"), filepath.Join(dir, "linked")); e != nil {
		t.Fatal(e)
	}
	other := filepath.Join(f.n.CWD, "other-skills")
	f.write(t, filepath.Join(other, "only", "SKILL.md"), "Another directory")
	rows := []protocol.JSONObject{}
	for _, raw := range []string{
		`{"dirs":[],"skill":false}`,
		`{"dirs":[]}`,
		`{"dirs":[]}`,
		`{"dirs":[],"agent":"child-a"}`,
		`{"dirs":[],"agent":"child-a"}`,
		`{"dirs":[],"agent":"child-b"}`,
		`{"dirs":[],"nodeEnv":"test","reset":true}`,
		`{"dirs":[]}`,
		`{"dirs":[],"reset":true}`,
	} {
		var row protocol.JSONObject
		_ = row.UnmarshalJSON([]byte(raw))
		rows = append(rows, row)
	}
	rows[0].Set("dirs", rawMain([]string{dir, filepath.Join(f.n.CWD, "missing"), other}))
	rows[6].Set("dirs", rawMain([]string{dir}))
	for _, key := range []string{"simple", "disabled"} {
		for _, value := range []string{"0", "false", " no ", "off", "1", "TRUE", " yes ", "on"} {
			rows = append(rows, protocol.NewJSONObject(protocol.JSONField{Name: "dirs", Value: rawMain([]string{dir})}, protocol.JSONField{Name: "reset", Value: []byte("true")}, protocol.JSONField{Name: key, Value: quoteMain(value)}))
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-skill-tool.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = f.home
	cmd.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_NATIVE_SKILL_TOOL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=fake-native-key", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "CC_HAHA_AGENT_TEAMS_ENABLED=false", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": f.home, "cwd": f.cwd, "flow": rows})
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source %v %.3000s", e, stderr.String())
	}
	var source struct {
		Flow         []protocol.JSONObject
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(output, &source); e != nil || len(source.Flow) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source %v rows=%d blocked=%v %.1500s", e, len(source.Flow), source.Capabilities.Blocked, output)
	}
	disabled := false
	makeMain := func(names string) *MainPrint {
		t.Helper()
		m, e := NewMainPrint(MainPrintOptions{ComputerUse: options, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "no models in original attachment consumer comparison", Tools: []string{names}, MaxTurns: 1, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
		if e = m.StartSession(ctx); e != nil {
			t.Fatal(e)
		}
		return m
	}
	m := makeMain("Skill,Read")
	withoutSkill := makeMain("Read")
	for i, row := range rows {
		active := m
		if string(row.Get("skill")) == "false" {
			active = withoutSkill
		}
		if e = active.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			if string(row.Get("reset")) == "true" {
				if e := active.resetNativeSkillListing(scope); e != nil {
					return e
				}
			}
			active.options.AgentID = row.String("agent")
			active.options.Native.Environment["CLAUDE_CODE_SIMPLE"] = row.String("simple")
			active.options.Native.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = row.String("disabled")
			active.options.Native.Environment["NODE_ENV"] = "production"
			if row.String("nodeEnv") != "" {
				active.options.Native.Environment["NODE_ENV"] = row.String("nodeEnv")
			}
			_ = json.Unmarshal(row.Get("dirs"), &active.dynamicSkillDirTriggers)
			attachments, e := active.nativeAttachments(scope, nil)
			if e != nil {
				return e
			}
			projected := []json.RawMessage{}
			for _, attachment := range attachments {
				projected = append(projected, attachment.Record.Get("attachment"))
			}
			triggers := append([]string{}, active.dynamicSkillDirTriggers...)
			actual := protocol.NewJSONObject(protocol.JSONField{Name: "attachments", Value: rawMain(projected)}, protocol.JSONField{Name: "triggers", Value: rawMain(triggers)})
			if !planSourcesEqual(rawMain(actual), rawMain(source.Flow[i])) {
				return fmt.Errorf("flow%d Go=%.2200s Source=%.2200s", i, rawMain(actual), rawMain(source.Flow[i]))
			}
			return nil
		}); e != nil {
			t.Error(e)
		}
	}
}

func TestActualExternalSkillTranscriptFilteringAndLegacyListingResumeMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	f, _, options := computerUseMainFixture(t)
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	f.n.Environment["NODE_ENV"] = "production"
	f.write(t, filepath.Join(f.cwd, ".git", "HEAD"), "ref: refs/heads/main\n")
	f.write(t, filepath.Join(f.cwd, ".claude", "skills", "local", "SKILL.md"), "---\ndescription: Survives a real restart\n---\nUse this local skill.")
	var requests atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"skill-restore\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture-model\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Skill listing persisted\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer model.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	disabled := false
	mainOptions := MainPrintOptions{ComputerUse: options, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual skill listing persistence and restart", Tools: []string{"Skill,Read"}, MaxTurns: 2, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard}
	m, e := NewMainPrint(mainOptions)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if e = m.StartSession(ctx); e != nil {
		t.Fatal(e)
	}
	sid := m.Controller.SessionID()
	if e = m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "session_id", Value: quoteMain(sid)}, protocol.JSONField{Name: "message", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoteMain("user")}, protocol.JSONField{Name: "content", Value: quoteMain("Persist the ordinary conversation")}))})); e != nil {
		t.Fatal(e)
	}
	var listing protocol.JSONObject
	if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		history, e := m.Host.OriginalHistoryOnOwner(scope)
		if e != nil {
			return e
		}
		for _, message := range history {
			attachment, _ := message.Record.Object("attachment")
			if message.Type() == "attachment" && attachment.String("type") == "skill_listing" {
				listing = message.Record.Clone()
			}
		}
		if !listing.IsObject() {
			return fmt.Errorf("actual accepted ASK did not retain the listing")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e = m.ShutdownSession(ctx); e != nil {
		t.Fatal(e)
	}
	files, e := filepath.Glob(filepath.Join(f.config, "projects", "*", "*.jsonl"))
	if e != nil || len(files) != 1 {
		t.Fatal("physical transcript missing", files, e)
	}
	disk, e := os.ReadFile(files[0])
	if e != nil || bytes.Contains(disk, []byte(`"type":"skill_listing"`)) {
		t.Fatal("external transcript did not apply original attachment filtering", e)
	}
	// An old transcript can contain this supported attachment (e.g. it was
	// authored by the internal profile). Build that physical fixture from the
	// real production user/assistant records and actual retained attachment.
	// Preserve the freshly produced external transcript unchanged.
	legacySID := "11111111-1111-4111-8111-111111111111"
	legacyPath := filepath.Join(filepath.Dir(files[0]), legacySID+".jsonl")
	var legacy bytes.Buffer
	inserted := false
	for _, line := range bytes.Split(bytes.TrimSpace(disk), []byte("\n")) {
		var record protocol.JSONObject
		if e := record.UnmarshalJSON(line); e != nil {
			t.Fatal(e)
		}
		if record.Get("sessionId") != nil {
			record.Set("sessionId", quoteMain(legacySID))
		}
		if !inserted && record.String("type") == "assistant" {
			old := record.Clone()
			for _, field := range listing.OwnProperties() {
				var name string
				_ = json.Unmarshal(field.NameJSON, &name)
				old.Set(name, field.Value)
			}
			old.Delete("message")
			old.Set("parentUuid", record.Get("parentUuid"))
			old.Set("sessionId", quoteMain(legacySID))
			legacy.Write(rawMain(old))
			legacy.WriteByte('\n')
			record.Set("parentUuid", listing.Get("uuid"))
			inserted = true
		}
		legacy.Write(rawMain(record))
		legacy.WriteByte('\n')
	}
	if !inserted {
		t.Fatal("production assistant record missing")
	}
	if e := os.WriteFile(legacyPath, legacy.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	rows := []protocol.JSONObject{}
	for _, raw := range []string{`{"dirs":[],"simple":"TRUE"}`, `{"dirs":[],"disabled":" yes "}`, `{"dirs":[],"nodeEnv":"test"}`, `{"dirs":[],"simple":"0","disabled":"false","agent":"child-a"}`, `{"dirs":[]}`, `{"dirs":[],"agent":"child-a"}`, `{"dirs":[],"agent":"child-b"}`, `{"dirs":[],"reset":true}`} {
		var row protocol.JSONObject
		_ = row.UnmarshalJSON([]byte(raw))
		rows = append(rows, row)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-skill-tool.fixture.ts")
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = f.home
	cmd.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_NATIVE_SKILL_TOOL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=fake-native-key", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "CC_HAHA_AGENT_TEAMS_ENABLED=false", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": f.home, "cwd": f.cwd, "flow": rows, "resumePath": legacyPath})
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source restore %v %.2600s", e, stderr.String())
	}
	var source struct {
		Flow         []protocol.JSONObject
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(output, &source); e != nil || len(source.Flow) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source restore %v rows=%d blocked=%v %.1600s", e, len(source.Flow), source.Capabilities.Blocked, output)
	}
	mainOptions.Arguments.Resume = legacySID
	resumed, e := NewMainPrint(mainOptions)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = resumed.ShutdownSession(context.Background()) })
	if e = resumed.StartSession(ctx); e != nil {
		t.Fatal(e)
	}
	if resumed.Controller.SessionID() != legacySID {
		t.Fatal("actual restart did not recover the SID")
	}
	for i, row := range rows {
		if e = resumed.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			resumed.options.AgentID = row.String("agent")
			resumed.options.Native.Environment["CLAUDE_CODE_SIMPLE"] = row.String("simple")
			resumed.options.Native.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = row.String("disabled")
			resumed.options.Native.Environment["NODE_ENV"] = "production"
			if row.String("nodeEnv") != "" {
				resumed.options.Native.Environment["NODE_ENV"] = row.String("nodeEnv")
			}
			if string(row.Get("reset")) == "true" {
				if e := resumed.resetNativeSkillListing(scope); e != nil {
					return e
				}
			}
			attachments, e := resumed.nativeAttachments(scope, nil)
			if e != nil {
				return e
			}
			projected := []json.RawMessage{}
			for _, attachment := range attachments {
				projected = append(projected, attachment.Record.Get("attachment"))
			}
			actual := protocol.NewJSONObject(protocol.JSONField{Name: "attachments", Value: rawMain(projected)}, protocol.JSONField{Name: "triggers", Value: []byte("[]")})
			if !planSourcesEqual(rawMain(actual), rawMain(source.Flow[i])) {
				return fmt.Errorf("restore%d Go=%.1800s Source=%.1800s", i, rawMain(actual), rawMain(source.Flow[i]))
			}
			return nil
		}); e != nil {
			t.Error(e)
		}
	}
	if requests.Load() != 1 {
		t.Fatal("attachment/recovery observation unexpectedly made model requests", requests.Load())
	}
}
