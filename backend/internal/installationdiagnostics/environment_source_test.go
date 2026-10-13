package installationdiagnostics

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
)

func TestDoctorBoundedEnvironmentAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	values := []string{"", "0", "-1", "+1suffix", " +0010.5", "1e9", "0x20000", "150000", "150001", " 150001 suffix", "-99999999999999999999999", "Infinity", "NaN", "1_000", "中文", "\ufeff\u00a0 30000rest", strings.Repeat("9", 1000)}
	cases := []any{}
	for _, value := range values {
		cases = append(cases, map[string]any{"mode": "environment", "value": value, "default": 30000, "upper": 150000})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-installation-diagnostic.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	child.Dir = home
	child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	raw, _ := json.Marshal(map[string]any{"homeDir": home, "cases": cases})
	child.Stdin = bytes.NewReader(raw)
	out, e := child.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var source []struct{ Validation EnvironmentValidation }
	if e = json.Unmarshal(out, &source); e != nil {
		t.Fatal(e, string(out))
	}
	for i, value := range values {
		actual := ValidateEnvironment(value, 30000, 150000)
		if !reflect.DeepEqual(actual, source[i].Validation) {
			t.Fatalf("value%q Source%#v Go%#v", value, source[i].Validation, actual)
		}
	}
}
