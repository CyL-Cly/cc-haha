package clibootstrap

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPlanAttachmentHumanTurnCadenceAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun unavailable")
	}
	makeRecord := func(v any) queryloop.Message {
		data, _ := json.Marshal(v)
		var record protocol.JSONObject
		_ = record.UnmarshalJSON(data)
		return queryloop.Message{Record: record}
	}
	user := func(meta any, content any) queryloop.Message {
		return makeRecord(map[string]any{"type": "user", "isMeta": meta, "message": map[string]any{"role": "user", "content": content}})
	}
	attachment := func(kind string) queryloop.Message {
		return makeRecord(map[string]any{"type": "attachment", "attachment": map[string]any{"type": kind}})
	}
	histories := [][]queryloop.Message{{}, {user(nil, "human")}, {attachment("plan_mode")}, {attachment("plan_mode_reentry")}, {attachment("plan_mode"), user(nil, "human"), user(true, "meta"), user(nil, []any{map[string]any{"type": "tool_result", "tool_use_id": "id"}})}, {attachment("plan_mode"), user(nil, []any{map[string]any{"type": "tool_result"}})}, {attachment("plan_mode"), user(nil, []any{map[string]any{"type": "tool_result", "tool_use_id": 4}})}, {attachment("plan_mode"), user([]any{}, "JS truthy meta")}, {attachment("plan_mode"), attachment("plan_mode_exit"), attachment("plan_mode_reentry")}}
	for count := 0; count < 12; count++ {
		for human := 0; human < 11; human++ {
			history := []queryloop.Message{attachment("plan_mode_exit")}
			for i := 0; i < count; i++ {
				history = append(history, attachment("plan_mode"))
			}
			for i := 0; i < human; i++ {
				history = append(history, user(nil, "human"), user(true, "meta"), user(nil, []any{map[string]any{"type": "tool_result", "tool_use_id": "id"}}))
			}
			histories = append(histories, history)
		}
	}
	values := make([][]protocol.JSONObject, len(histories))
	for i, h := range histories {
		for _, m := range h {
			values[i] = append(values[i], m.Record)
		}
		if values[i] == nil {
			values[i] = []protocol.JSONObject{}
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-plan-cadence.fixture.ts")
	home := t.TempDir()
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_PLAN_CADENCE_FIXTURE=1", "BUN_OPTIONS=--no-env-file"}
	data, _ := json.Marshal(values)
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var expected []struct {
		Turns int  `json:"turnCount"`
		Found bool `json:"foundPlanModeAttachment"`
		Count int  `json:"count"`
	}
	if err = json.Unmarshal(out, &expected); err != nil {
		t.Fatal(err, string(out))
	}
	for i, history := range histories {
		turns, found, count := planAttachmentCadence(history)
		want := expected[i]
		if turns != want.Turns || found != want.Found || count != want.Count {
			t.Fatalf("case %d Go=(%d,%v,%d) Source=%+v", i, turns, found, count, want)
		}
	}
}
