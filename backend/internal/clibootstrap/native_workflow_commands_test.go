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
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceWorkflowCommandsUseSharedNativePromptRegistry(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	m := backgroundAgentMainWithSetup(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		_ = os.MkdirAll(filepath.Join(n.CWD, ".git"), 0700)
		dir := filepath.Join(n.CWD, ".claude", "workflows")
		_ = os.MkdirAll(dir, 0700)
		if e := os.WriteFile(filepath.Join(dir, "native-command.js"), []byte(`export const meta={name:'native-command',description:'实际 saved command'};return args`), 0600); e != nil {
			t.Fatal(e)
		}
	})
	args := []string{"", "  issues 1024, 1025  ", " 中文\nnext ", `quoted " argument`}
	home := m.options.Native.Home
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-workflow.fixture.ts")
	data, _ := json.Marshal(map[string]any{"homeDir": home, "workflowPromptCommands": map[string]any{"args": args}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_WORKFLOW_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(data)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if e := command.Run(); e != nil {
		t.Fatalf("actual Source commands %v %.1600s %.1600s", e, stdout.String(), stderr.String())
	}
	var source struct {
		WorkflowPromptCommands []protocol.JSONObject
		Capabilities           struct{ Blocked []string }
	}
	if e := json.Unmarshal(stdout.Bytes(), &source); e != nil || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, source.Capabilities.Blocked, stderr.String())
	}
	actual := []protocol.JSONObject{}
	if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		definitions, e := m.nativeWorkflowCommandDefinitions(ctx, scope)
		if e != nil {
			return e
		}
		inv, e := m.nativeSkillInvocation(scope)
		if e != nil {
			return e
		}
		for _, definition := range definitions {
			prompts := [][]protocol.JSONObject{}
			for _, arg := range args {
				text, e := definition.Prompt(ctx, &arg, inv)
				if e != nil {
					return e
				}
				prompts = append(prompts, []protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("text")}, protocol.JSONField{Name: "text", Value: quoteMain(text)})})
			}
			actual = append(actual, protocol.NewJSONObject(protocol.JSONField{Name: "metadata", Value: rawMain(definition.Metadata())}, protocol.JSONField{Name: "prompts", Value: rawMain(prompts)}))
		}
		present := false
		for _, cmd := range m.Registry.ControlCommands() {
			present = present || cmd.Name == "native-command"
		}
		if !present {
			return io.ErrUnexpectedEOF
		}
		processed, e := m.nativeProcessSkillPrompt(ctx, scope, "native-command", "issues 1, 2", nil, nil)
		if e != nil {
			return e
		}
		if !strings.Contains(string(rawMain(processed.Messages)), `Run the saved workflow`) || len(processed.CommandRules) != 0 || processed.Model != "" {
			return io.ErrUnexpectedEOF
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	canonical := func(value any) any {
		raw, _ := json.Marshal(value)
		var out any
		_ = json.Unmarshal(raw, &out)
		return out
	}
	if !reflect.DeepEqual(canonical(actual), canonical(source.WorkflowPromptCommands)) {
		t.Fatalf("Source actual command adapter differs\nGo%s\nSource%s", rawMain(actual), rawMain(source.WorkflowPromptCommands))
	}
}
