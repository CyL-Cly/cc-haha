package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

func TestNativeMCPOrdinaryReviewPromptActualSourceBytes(t *testing.T) {
	home, env := ordinaryFixture(t)
	args := []string{"", "42", "中文\n${args}`$()"}
	fixture, _ := filepath.Abs("../../testdata/native-mcp-review.fixture.ts")
	profile, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	command := exec.Command("/usr/bin/sandbox-exec", "-f", profile, bun, "--no-env-file", fixture)
	command.Dir = home
	for k, v := range env {
		command.Env = append(command.Env, k+"="+v)
	}
	command.Env = append(command.Env, "BUN_OPTIONS=--no-env-file", "CC_HAHA_MCP_REVIEW_FIXTURE=1", "CC_HAHA_FIXTURE_TEMP_ROOT="+filepath.Dir(home))
	command.Stdin = bytes.NewReader(rawMain(args))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	raw, e := command.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var rows [][]struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if e = json.Unmarshal(raw, &rows); e != nil || len(rows) != len(args) {
		t.Fatal(e, string(raw))
	}
	host, _ := ordinaryGoContext(t, home, home, env)
	e = host.runRequest(context.Background(), func(scope *cooperative.Scope) error {
		definition, e := ordinaryReviewCommand()
		if e != nil {
			return e
		}
		for i, arg := range args {
			got, e := definition.Prompt(context.Background(), &arg, skillcommands.Invocation{NativeScope: scope, SessionID: host.state.Snapshot().SessionID, Platform: "darwin"})
			if e != nil {
				return e
			}
			if len(rows[i]) != 1 || rows[i][0].Type != "text" || got != rows[i][0].Text {
				t.Errorf("actual review prompt bytes differ case%d: Go length=%d Source=%d", i, len(got), len(rows[i][0].Text))
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
