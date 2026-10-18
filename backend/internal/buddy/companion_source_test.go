package buddy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestBuddyActualSourcePinnedHashRollLegacyOverrideAndAllArtwork(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Source required")
	}
	type row struct {
		Seed   json.RawMessage      `json:"seed,omitempty"`
		Salted bool                 `json:"salted,omitempty"`
		Global *protocol.JSONObject `json:"global,omitempty"`
	}
	rows := []row{}
	for i := 0; i < 350; i++ {
		rows = append(rows, row{Seed: quote(fmt.Sprintf("actual-frozen-buddy-%d", i))})
	}
	for _, seed := range []json.RawMessage{quote(""), quote("汉字😀"), quote("e\u0301"), json.RawMessage(`"\ud800"`), json.RawMessage(`"\udc00"`), json.RawMessage(`"\ufffd"`)} {
		for _, salted := range []bool{false, true} {
			rows = append(rows, row{Seed: seed, Salted: salted})
		}
	}
	for _, raw := range []string{`{}`, `{"userID":"private-user"}`, `{"oauthAccount":{"accountUuid":"private-account"},"userID":"ignored-user"}`, `{"companion":{"name":"Legacy","personality":"Old snapshot","hatchedAt":1777777777777,"species":"stale","rarity":"stale","futureSoul":{"keep":true}}}`, `{"companion":{"name":"Seeded","personality":"Exact stable appearance","hatchedAt":1777777777777,"appearanceSeed":"private-fixed-seed","speciesOverride":"cat","futureSoul":{"keep":true}}}`, `{"companion":{"name":"Empty seed","personality":"Literal empty appearance","hatchedAt":0,"appearanceSeed":""}}`, `{"companion":{"name":"\ud800","personality":"\ud801","hatchedAt":0},"userID":"\udc00"}`, `{"companion":{"name":"Missing legacy fields"}}`} {
		var global protocol.JSONObject
		if err := global.UnmarshalJSON([]byte(raw)); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row{Global: &global})
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-buddy.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_BUDDY_SOURCE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=100", "LINES=35"}
	data, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(data)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Actual buddy Source %v %.5000s", err, output)
	}
	var expected []struct {
		Roll struct {
			Bones           protocol.JSONObject
			InspirationSeed int64
		}
		Stored json.RawMessage
		Face   string
		Frames [][]string
		Count  int
	}
	if err = json.Unmarshal(output, &expected); err != nil || len(expected) != len(rows) {
		t.Fatal(err, string(output))
	}
	species, rarities := map[string]bool{}, map[string]bool{}
	for i, input := range rows {
		seed, salted := input.Seed, input.Salted
		if input.Global != nil {
			seed, salted = UserID(*input.Global), true
		}
		bones, inspiration, err := Roll(seed, salted)
		if err != nil {
			t.Fatal(i, err)
		}
		actual, _ := bones.MarshalJSON()
		want, _ := expected[i].Roll.Bones.MarshalJSON()
		if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(actual), protocol.StringifyValidatedJavaScriptJSON(want)) || inspiration != expected[i].Roll.InspirationSeed {
			t.Fatalf("Exact Source seeded appearance%d actual%s seed%d Source%s seed%d", i, actual, inspiration, want, expected[i].Roll.InspirationSeed)
		}
		if input.Global != nil {
			stored, err := Get(*input.Global)
			if err != nil {
				t.Fatal(i, err)
			}
			var encoded json.RawMessage = quote(nil)
			if stored != nil {
				encoded = rawObject(*stored)
				bones = *stored
			}
			if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(encoded), protocol.StringifyValidatedJavaScriptJSON(expected[i].Stored)) {
				t.Fatalf("Legacy/raw stored companion%d actual%s Source%s", i, encoded, expected[i].Stored)
			}
		}
		if FrameCount(bones.String("species")) != expected[i].Count {
			t.Fatal("Frame count", i)
		}
		if Face(bones) != expected[i].Face {
			t.Fatalf("Exact narrow Source face%d got%q Source%q", i, Face(bones), expected[i].Face)
		}
		for j, frame := range []int{0, 1, 2, 3, 17} {
			lines, err := Sprite(bones, frame)
			if err != nil || !reflect.DeepEqual(lines, expected[i].Frames[j]) {
				t.Fatal("Actual Source art/hat/eye/blank-slot", i, frame, err, lines, expected[i].Frames[j])
			}
		}
		species[bones.String("species")] = true
		rarities[bones.String("rarity")] = true
	}
	if len(species) != len(Species) || len(rarities) != len(Rarities) {
		t.Fatal("Source profile failed to reach every species/rarity", species, rarities)
	}
}
