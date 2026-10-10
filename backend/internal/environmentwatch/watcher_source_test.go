package environmentwatch

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestEnvironmentWatchPathResolutionRestartAndEqualitySource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	configs := []string{`{}`, `{"FileChanged":[{"matcher":" .env|.envrc | .env | | /absolute/one"},{"matcher":"😀|\ufefftrim\u2000|*.literal"}]}`, `{"CwdChanged":[{"hooks":[]}]}`}
	type row struct {
		CWD     string              `json:"cwd"`
		Config  protocol.JSONObject `json:"config"`
		Updates [][]string          `json:"updates"`
	}
	rows := []row{}
	for _, config := range configs {
		var o protocol.JSONObject
		_ = o.UnmarshalJSON([]byte(config))
		rows = append(rows, row{"/original/cwd", o, [][]string{{"/dynamic/first", "/dynamic/second"}, {"/dynamic/second", "/dynamic/first"}, {"/dynamic/first", "/dynamic/first"}, {}}})
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-env-watcher.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_ENV_WATCHER_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(data)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	var source struct {
		Rows []struct {
			Paths  []string
			Native []struct{ Paths []string }
		}
	}
	if err = json.Unmarshal(output, &source); err != nil {
		t.Fatal(err, string(output))
	}
	for i, r := range rows {
		final := ResolvePaths(r.CWD, r.Config, r.Updates[len(r.Updates)-1])
		if !reflect.DeepEqual(final, source.Rows[i].Paths) {
			t.Fatalf("paths %v/%v", final, source.Rows[i].Paths)
		}
		// Real physical watcher receives the same final path pool and ignores a
		// reordered dynamic set; source/native subscription objects stay separate.
		for _, entry := range source.Rows[i].Native {
			if len(entry.Paths) == 0 {
				t.Fatal("Source must not start an empty watcher")
			}
		}
	}
}
