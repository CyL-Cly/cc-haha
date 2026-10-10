package teamworker

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTeamPresetSourceAuthorityMatchesActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	home := t.TempDir()
	file := filepath.Join(home, "preset.md")
	large := filepath.Join(home, "large.md")
	data := []byte("---\ndescription: original source\n---\nFrozen original body")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(large, make([]byte, maxPresetBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	rows := []map[string]any{
		{"agent": map[string]string{"source": "built-in"}}, {"agent": map[string]string{"source": "flagSettings"}},
		{"agent": map[string]string{"source": "projectSettings", "sourceFilePath": file, "sourceContentHash": hash}},
		{"agent": map[string]string{"source": "plugin"}},
		{"agent": map[string]string{"source": "projectSettings", "sourceFilePath": "missing"}},
		{"agent": map[string]string{"source": "projectSettings", "sourceFilePath": file, "sourceContentHash": "old"}},
		{"agent": map[string]string{"source": "plugin", "sourceFilePath": large, "sourceContentHash": "old"}},
		{"snapshot": map[string]any{"source": "built-in", "sourceIdentity": map[string]string{"kind": "builtin"}}},
		{"snapshot": map[string]any{"source": "flagSettings", "sourceIdentity": map[string]string{"kind": "session"}}},
		{"snapshot": map[string]any{"source": "plugin", "sourceIdentity": map[string]string{"kind": "file", "path": file, "sha256": hash}}},
		{"snapshot": map[string]any{"source": "plugin"}},
		{"snapshot": map[string]any{"source": "built-in", "sourceIdentity": map[string]string{"kind": "file", "path": file, "sha256": hash}}},
		{"snapshot": map[string]any{"source": "plugin", "sourceIdentity": map[string]string{"kind": "file", "path": file, "sha256": "old"}}},
		{"snapshot": map[string]any{"source": "plugin", "sourceIdentity": map[string]string{"kind": "file", "path": large, "sha256": "old"}}},
		{"snapshot": map[string]any{"source": "plugin", "sourceIdentity": map[string]string{"kind": "file", "path": filepath.Join(home, "absent"), "sha256": "old"}}},
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-preset-source.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_PRESET_SOURCE_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "cases": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Rows         []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Rows) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output))
	}
	for i, row := range rows {
		var value any
		var failure error
		if v, ok := row["agent"].(map[string]string); ok {
			var got protocol.JSONObject
			got, failure = SnapshotPresetSource(agentcatalog.Definition{Source: v["source"], FilePath: v["sourceFilePath"], ContentHash: v["sourceContentHash"]})
			if failure == nil {
				value = got
			}
		} else {
			var snapshot protocol.JSONObject
			_ = snapshot.UnmarshalJSON(raw(row["snapshot"]))
			failure = ValidatePresetSource(snapshot)
		}
		var errorText any
		if failure != nil {
			errorText = failure.Error()
		}
		actual := raw(map[string]any{"value": value, "error": errorText})
		var a, b any
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(source.Rows[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("case %d Go %s Source %s", i, actual, source.Rows[i])
		}
	}
}
