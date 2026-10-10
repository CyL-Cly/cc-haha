package memoryapi

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

func sourceHTTP(t *testing.T, home, config, cwd string, operations []operation) []observed {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		return nil
	}
	_, file, _, _ := runtime.Caller(0)
	fixture := filepath.Join(filepath.Dir(file), "../../../scripts/quality-gate/go-parity/native-memory.fixture.ts")
	input, err := json.Marshal(map[string]any{"cwd": cwd, "operations": operations})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_NATIVE_MEMORY_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source Memory: %v %s", err, stderr.Bytes())
	}
	var source []observed
	if err = json.Unmarshal(output, &source); err != nil || len(source) != len(operations) {
		t.Fatalf("actual Source Memory output: %v %s", err, output)
	}
	return source
}
func compareSource(t *testing.T, s *Service, config, cwd string, operations []operation) {
	t.Helper()
	actual := make([]observed, len(operations))
	for i, op := range operations {
		actual[i] = request(s, op)
	}
	source := sourceHTTP(t, s.options.Home, config, cwd, operations)
	if source == nil {
		return
	}
	for i, expected := range source {
		if actual[i].Status != expected.Status || !reflect.DeepEqual(decode(t, actual[i].Body), decode(t, expected.Body)) {
			t.Errorf("%s %s\nGo: %d %s\nSource: %d %s", operations[i].Method, operations[i].Path, actual[i].Status, actual[i].Body, expected.Status, expected.Body)
		}
	}
}
