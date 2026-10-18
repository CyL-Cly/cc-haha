package sessioncontroller

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestActualSourceSidechainRecordKeepsParentPromptOutOfAgentMetadata(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	main := plainMessage("parent-input", "actual parent work")
	child := plainMessage("child-input", "actual child work")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("../goalstate/testdata/goal-command.fixture.ts")
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_GOAL_COMMAND_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "NODE_ENV=production"}
	command.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "recordSidechainProbe": true, "sid": "11111111-1111-4111-8111-111111111111", "agentID": "actual-child", "mainMessages": []protocol.JSONObject{*main}, "childMessages": []protocol.JSONObject{*child}}))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal("actual Source sidechain", err, string(output))
	}
	var source struct{ Main, Child []protocol.JSONObject }
	if err := json.Unmarshal(output, &source); err != nil {
		t.Fatal(err, string(output))
	}
	owner, writer, pending, loader := recordHost(t, OSFiles{}, nil)
	if _, err := startRecord(t, owner, pending, loader, []LiveRecord{{Record: main}}, RecordOptions{PromptID: func() json.RawMessage { return nil }}).Wait(ctx); err != nil {
		t.Fatal(err)
	}
	var childWriter *Writer
	var childPending *PendingWriter
	pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error {
		options := writer.options
		options.AgentID = "actual-child"
		childWriter, err = NewWriter(options)
		if err == nil {
			childPending, err = NewPendingWriter(childWriter, owner)
		}
		return err
	})
	t.Cleanup(func() { _ = childPending.Close(context.Background()) })
	var future *RecordFuture
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		future, err = childPending.BeginRecordLive(scope, loader, LiveRecordProfile{QuerySource: "agent:builtin:general-purpose"}, []LiveRecord{{Record: child}}, RecordOptions{PromptID: func() json.RawMessage { return nil }})
		return err
	})
	if _, err := future.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	for _, target := range []*Writer{writer, childWriter} {
		pendingCall(t, owner, cooperative.Flush, func(*cooperative.Scope) error { return target.RefreshMetadata() })
	}
	for _, target := range []*PendingWriter{pending, childPending} {
		if err := flushRecord(t, owner, target).Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	prompts := func(rows []protocol.JSONObject) []string {
		result := []string{}
		for _, row := range rows {
			if row.String("type") == "last-prompt" {
				result = append(result, row.String("lastPrompt"))
			}
		}
		return result
	}
	physical := func(writer *Writer) []protocol.JSONObject {
		rows := []protocol.JSONObject{}
		for _, line := range strings.Split(strings.TrimSpace(recordBytes(t, writer)), "\n") {
			var row protocol.JSONObject
			if err := row.UnmarshalJSON([]byte(line)); err != nil {
				t.Fatal(err)
			}
			rows = append(rows, row)
		}
		return rows
	}
	if got, want := prompts(physical(writer)), prompts(source.Main); !reflect.DeepEqual(got, want) {
		t.Errorf("parent physical last-prompt: Go %q Source %q", got, want)
	}
	if got, want := prompts(physical(childWriter)), prompts(source.Child); !reflect.DeepEqual(got, want) {
		t.Errorf("agent physical last-prompt: Go %q Source %q", got, want)
	}
	if childWriter.lastPrompt != nil {
		t.Errorf("sidechain retained main-picker prompt memo: %s", childWriter.lastPrompt)
	}
}
