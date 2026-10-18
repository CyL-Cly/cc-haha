package buddy

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestBuddyActualSourceWholeCardMountActionsRawGlobalAndKeyboard(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Source required")
	}
	type row struct {
		Global protocol.JSONObject `json:"global"`
		Args   string              `json:"args"`
		Now    []float64           `json:"now"`
		Random []float64           `json:"random"`
		Keys   []string            `json:"keys,omitempty"`
	}
	rows := []row{}
	for _, raw := range []string{`{"futureBuddy":{"keep":true}}`, `{"companion":{"name":"Old pet","personality":"Legacy","hatchedAt":1777777777777,"futureSoul":{"keep":true}},"futureBuddy":{"keep":true}}`, `{"companion":{"name":"\ud800","personality":"\ud801","hatchedAt":0},"companionMuted":true,"futureBuddy":{"keep":true}}`, `{"companion":{"hatchedAt":0},"futureBuddy":{"keep":true}}`} {
		var global protocol.JSONObject
		if err := global.UnmarshalJSON([]byte(raw)); err != nil {
			t.Fatal(err)
		}
		for _, args := range []string{"mute", " UNMUTE\u00a0", "pet", "hatch", "release", "info"} {
			rows = append(rows, row{Global: global, Args: args, Now: []float64{1777777777777}, Random: []float64{.125, .25, .5}})
		}
	}
	for _, key := range []string{"q", "\r", "\x1b", "n"} {
		rows = append(rows, row{Global: rows[6].Global, Args: "info", Now: []float64{1777777777777}, Random: []float64{.125}, Keys: []string{key}})
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-buddy-card.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_BUDDY_SOURCE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=100", "LINES=35"}
	data, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(data)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Actual whole Buddy: %v %.5000s", err, output)
	}
	var expected struct {
		Rows []struct {
			Events   [][]json.RawMessage
			Global   protocol.JSONObject
			AppState protocol.JSONObject
			Frames   []struct {
				Key, Output string
				Events      [][]json.RawMessage
			}
		}
	}
	if err = json.Unmarshal(output, &expected); err != nil || len(expected.Rows) != len(rows) {
		t.Fatal(err, string(output))
	}
	for index, input := range rows {
		i, j := 0, 0
		actual, err := Apply(input.Global, input.Args, func() float64 { value := input.Now[min(i, len(input.Now)-1)]; i++; return value }, func() float64 { value := input.Random[min(j, len(input.Random)-1)]; j++; return value })
		if err != nil {
			t.Fatal(index, err)
		}
		want := expected.Rows[index]
		if actual.Done {
			if len(want.Events) != 1 || len(want.Events[0]) != 2 || !bytes.Equal(actual.Output, protocol.StringifyValidatedJavaScriptJSON(want.Events[0][0])) || string(want.Events[0][1]) != `{"display":"system"}` {
				t.Fatalf("Action%d %s got%s Source%s", index, input.Args, actual.Output, rawObject(protocol.NewJSONObject(protocol.JSONField{Name: "events", Value: quote(want.Events)})))
			}
		} else if len(input.Keys) == 0 && len(want.Events) != 0 {
			t.Fatal("Info mount must not complete", index, want.Events)
		}
		global := input.Global
		if actual.Global != nil {
			global = *actual.Global
		}
		projection := protocol.NewJSONObject()
		for _, key := range []string{"companion", "companionMuted", "futureBuddy"} {
			if value := global.Get(key); len(value) > 0 {
				projection.Set(key, value)
			}
		}
		if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(rawObject(projection)), protocol.StringifyValidatedJavaScriptJSON(rawObject(want.Global))) {
			t.Fatalf("Whole mount global%d got%s Source%s", index, rawObject(projection), rawObject(want.Global))
		}
		if actual.PetAt != nil {
			if string(want.AppState.Get("companionPetAt")) != string(quote(*actual.PetAt)) {
				t.Fatal("Actual pet state", index, want.AppState)
			}
		} else if len(want.AppState.Get("companionPetAt")) > 0 {
			t.Fatal("Unexpected pet state", index)
		}
		if len(input.Keys) > 0 {
			expectedCount := 0
			if input.Keys[0] == "q" {
				expectedCount = 1
			}
			if len(want.Events) != expectedCount {
				t.Fatalf("Actual focused Box key%s events%s", input.Keys[0], quote(want.Events))
			}
		}
	}
}
