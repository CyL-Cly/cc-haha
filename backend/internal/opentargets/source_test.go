package opentargets

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func compareSource(t *testing.T, home string, row scenario, actual outcomes) {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		return
	}
	_, file, _, _ := runtime.Caller(0)
	fixture := filepath.Join(filepath.Dir(file), "../../../scripts/quality-gate/go-parity/native-open-targets.fixture.ts")
	input, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_NATIVE_OPEN_TARGETS_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source Open Targets: %v %s", err, stderr.Bytes())
	}
	var source outcomes
	if err = json.Unmarshal(output, &source); err != nil {
		t.Fatalf("actual Source output: %v %s", err, output)
	}
	if len(actual.Observations) != len(source.Observations) {
		t.Fatalf("observation count: Go=%d Source=%d", len(actual.Observations), len(source.Observations))
	}
	for i, row := range actual.Observations {
		expected := source.Observations[i]
		if row.Status != expected.Status || row.Type != expected.Type || !reflect.DeepEqual(row.Cache, expected.Cache) {
			t.Errorf("observation %d headers/status: Go %+v Source %+v", i, row, expected)
		}
		if row.Type == "image/png" {
			if row.Body != expected.Body {
				t.Errorf("observation %d icon bytes differ", i)
			}
		} else if !reflect.DeepEqual(decode(t, row.Body), decode(t, expected.Body)) {
			t.Errorf("observation %d full body: Go %s Source %s", i, row.Body, expected.Body)
		}
	}
	if !reflect.DeepEqual(actual.Launches, source.Launches) || !reflect.DeepEqual(actual.Icons, source.Icons) || !reflect.DeepEqual(actual.Queries, source.Queries) {
		t.Errorf("OS primitive calls: Go %+v Source %+v", actual, source)
	}
}
