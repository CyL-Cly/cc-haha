package authhelper

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceFourHelperProvenancePreservesUTF16AndEscapeEquivalence(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	rows := []protocol.JSONObject{}
	for _, pair := range [][3]string{{`"command"`, `"command"`, `null`}, {`"command"`, `null`, `"command"`}, {`"command\ud800"`, `"command\ud800"`, `null`}, {`"command\ud800"`, `"command�"`, `null`}, {`"\u0061"`, `"a"`, `null`}, {`""`, `""`, `null`}, {`"one"`, `"other"`, `"three"`}} {
		row := protocol.NewJSONObject()
		for i, name := range []string{"settings", "projectSettings", "localSettings"} {
			values := protocol.NewJSONObject()
			for _, key := range []string{"apiKeyHelper", "awsAuthRefresh", "awsCredentialExport", "gcpAuthRefresh"} {
				values.Set(key, json.RawMessage(pair[i]))
			}
			encoded, _ := values.MarshalJSON()
			row.Set(name, encoded)
		}
		rows = append(rows, row)
	}
	got := [][]bool{}
	for _, row := range rows {
		settings, _ := row.Object("settings")
		project, _ := row.Object("projectSettings")
		local, _ := row.Object("localSettings")
		values := []bool{}
		for _, key := range []string{"apiKeyHelper", "awsAuthRefresh", "awsCredentialExport", "gcpAuthRefresh"} {
			values = append(values, ConfiguredInSource(settings.Get(key), project.Get(key)) || ConfiguredInSource(settings.Get(key), local.Get(key)))
		}
		got = append(got, values)
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-helper-origin.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_HELPER_ORIGIN_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"rows": rows})
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source [][]bool
	if err = json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, source) {
		t.Fatal("actual helper source trust identities differ", got, source)
	}
}
