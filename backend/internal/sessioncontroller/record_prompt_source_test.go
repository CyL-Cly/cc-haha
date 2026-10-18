package sessioncontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

func TestActualRecordMetadataUsesOriginalFirstMeaningfulUserSelector(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	user := func(content json.RawMessage) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("user")}, protocol.JSONField{Name: "message", Value: raw(map[string]json.RawMessage{"role": quote("user"), "content": content})})
	}
	sets := [][]protocol.JSONObject{
		{user(quote("real\n human"))},
		{user(quote("<command-name>/settings</command-name><command-args>x</command-args>")), user(quote("meaningful"))},
		{user(quote("<command-name>/custom</command-name><command-args>\ufeff args \u3000</command-args>"))},
		{user(quote("<command-name>/custom</command-name>")), user(quote("after empty custom"))},
		{user(raw([]map[string]string{{"type": "text", "text": "<ide_opened_file>metadata</ide_opened_file>"}, {"type": "text", "text": "actual VS Code text"}}))},
		{user(quote("<bash-input>echo &amp; verify</bash-input>"))},
		{user(quote("[Request interrupted by user]")), user(quote("after interruption"))},
		{user(quote("[request interrupted by user] lowercase"))},
		{user(quote(strings.Repeat("a", 199) + "😀 rest"))},
		{user(quote("<COMMAND-NAME attr='x'>/custom</COMMAND-NAME><command-args>task</command-args>"))},
		{user(json.RawMessage(`"real\ud800text"`))},
		{user(quote("<future-notification>ignored</future-notification>"))},
		{user(quote("No prompt"))},
		{user(quote(" \n\t "))},
	}
	for i, records := range sets {
		for j := range records {
			records[j].Set("uuid", quote(fmt.Sprintf("prompt-%d-%d", i, j)))
			records[j].Set("timestamp", quote("2026-01-01T00:00:00.000Z"))
		}
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../goalstate/testdata/goal-command.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_GOAL_COMMAND_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "NODE_ENV=production"}
	command.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "firstPrompts": sets}))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal("original meaningful selector", err, string(output))
	}
	var source struct {
		Values []struct {
			Value, Meaningful json.RawMessage
			Error             any
		}
	}
	if err := json.Unmarshal(output, &source); err != nil || len(source.Values) != len(sets) {
		t.Fatal(err, string(output))
	}
	for index, records := range sets {
		for _, independent := range []bool{true, false} {
			t.Run(fmt.Sprintf("%d-independent-%v", index, independent), func(t *testing.T) {
				if source.Values[index].Error != nil {
					t.Fatal("unexpected original error", source.Values[index].Error)
				}
				owner, writer, pending, loader := recordHost(t, OSFiles{}, nil)
				pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error { writer.lastPrompt = quote("prior meaningful"); return nil })
				live := make([]LiveRecord, len(records))
				for i := range records {
					live[i] = LiveRecord{Record: &records[i]}
				}
				if independent {
					future := startRecord(t, owner, pending, loader, live, RecordOptions{PromptID: func() json.RawMessage { return nil }})
					if err := future.future.Wait(ctx); err != nil {
						t.Fatal(err)
					}
				} else {
					pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error {
						pointers := make([]*protocol.JSONObject, len(records))
						for i := range records {
							pointers[i] = &records[i]
						}
						_, err := writer.Record(pointers, nil)
						return err
					})
				}
				pendingCall(t, owner, cooperative.Flush, func(*cooperative.Scope) error { return writer.RefreshMetadata() })
				if err := flushRecord(t, owner, pending).Wait(ctx); err != nil {
					t.Fatal(err)
				}
				got := json.RawMessage(`""`)
				for _, line := range strings.Split(recordBytes(t, writer), "\n") {
					var row protocol.JSONObject
					if row.UnmarshalJSON([]byte(line)) == nil && row.String("type") == "last-prompt" {
						got = row.Get("lastPrompt")
					}
				}
				want := source.Values[index].Value
				if bytes.Equal(source.Values[index].Meaningful, []byte("null")) {
					want = quote("prior meaningful")
				}
				cached, _ := protocol.DecodeJavaScriptJSONString(writer.lastPrompt)
				wanted, _ := protocol.DecodeJavaScriptJSONString(want)
				if !reflect.DeepEqual(cached, wanted) {
					t.Errorf("actual Source prompt memo differs\nGo %s\nSource %s", writer.lastPrompt, want)
				}
				// Materialization wrote the previous memo before this record. Source
				// omits an empty memo at re-append, so that prior physical row remains.
				if len(wanted) == 0 {
					want = quote("prior meaningful")
				}
				a, _ := protocol.DecodeJavaScriptJSONString(got)
				b, _ := protocol.DecodeJavaScriptJSONString(want)
				if !reflect.DeepEqual(a, b) {
					t.Errorf("actual persisted metadata differs\nGo %s\nSource %s", got, want)
				}
			})
		}
	}
}
