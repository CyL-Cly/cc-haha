package computerusemcp

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestActualSourceMCPValidationAndAdvertisedSchema(t *testing.T) {
	b, service := boundFixture(t)
	type row struct {
		Name     string          `json:"name"`
		Args     json.RawMessage `json:"args"`
		Disabled bool            `json:"disabled,omitempty"`
	}
	rows := []row{}
	for _, raw := range []string{"null", "[]", `{}`, `{"code":null}`, `{"code":"1","timeout_ms":0}`, `{"code":"1","timeout_ms":60001}`, `{"code":"1","timeout_ms":1.5}`, `{"code":"1","timeout_ms":"1"}`, `{"code":"1","title":null}`, `{"code":"1","future":true}`} {
		rows = append(rows, row{Name: "js", Args: json.RawMessage(raw)})
	}
	for _, raw := range []string{"null", "[]", `{"future":1}`, `{}`} {
		rows = append(rows, row{Name: "js_reset", Args: json.RawMessage(raw)})
	}
	rows = append(rows, row{Name: "js", Args: json.RawMessage(`{"code":"1"}`), Disabled: true})
	observations := []any{}
	for _, row := range rows {
		if row.Disabled {
			if err := service.UpdateConfig(context.Background(), json.RawMessage(`{"enabled":false}`)); err != nil {
				t.Fatal(err)
			}
		}
		got := b.Call(context.Background(), row.Name, row.Args)
		encoded, _ := json.Marshal(got)
		var value any
		_ = json.Unmarshal(encoded, &value)
		observations = append(observations, value)
	}
	var tools any
	_ = json.Unmarshal(toolDefinitions, &tools)
	got := map[string]any{"observations": observations, "tools": tools}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		var err error
		bun, err = exec.LookPath("bun")
		if err != nil {
			t.Skip("Source Bun unavailable")
		}
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	for _, p := range []string{home, filepath.Join(home, ".claude"), filepath.Join(home, "tmp")} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cu-mcp-binder.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", "run", fixture)
	cmd.Dir = home
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "BUN_OPTIONS=--no-env-file", "NODE_ENV=test", "CC_HAHA_FIXTURE_TEMP_ROOT=" + root, "CC_HAHA_CU_BINDER_FIXTURE=1"}
	encoded, _ := json.Marshal(map[string]any{"cases": rows})
	cmd.Stdin = bytes.NewReader(encoded)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source binder %v %s", err, stderr.String())
	}
	var want any
	if err = json.Unmarshal(output, &want); err != nil {
		t.Fatal(err, string(output))
	}
	if !reflect.DeepEqual(got, want) {
		a, _ := json.Marshal(got)
		t.Fatalf("Go %s\nSource %s", a, output)
	}
}
