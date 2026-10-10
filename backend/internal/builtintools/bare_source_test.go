package builtintools

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

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestBareCompleteCatalogAndDeniesMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	type row struct {
		Simple string   `json:"simple"`
		Deny   []string `json:"deny"`
	}
	rows := []row{}
	for _, flag := range []string{"1", "TRUE", " yes ", "on"} {
		for _, deny := range [][]string{{}, {"Write"}, {"Read"}, {"Bash"}, {"Bash", "Read", "Edit"}} {
			rows = append(rows, row{flag, deny})
		}
	}
	home := t.TempDir()
	config, temp := filepath.Join(home, ".claude"), filepath.Join(home, "tmp")
	for _, path := range []string{config, temp} {
		if e := os.MkdirAll(path, 0700); e != nil {
			t.Fatal(e)
		}
	}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows})
	path, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-bare-tools.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", path)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_BARE_TOOLS_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, e := cmd.Output()
	if e != nil {
		t.Fatalf("actual Source %v %.2400s", e, stderr.String())
	}
	var source struct {
		Rows         [][]string
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(data, &source); e != nil || len(source.Rows) != len(rows) || len(source.Capabilities.Blocked) != 0 {
		t.Fatalf("Source escaped or wrong shape %v %.2400s", e, data)
	}
	s, owner, _, permissions := fixture(t)
	for n, item := range rows {
		s.options.Environment["CLAUDE_CODE_SIMPLE"] = item.Simple
		permissions.Set("alwaysDenyRules", raw(map[string][]string{"cliArg": item.Deny}))
		actual := []string{}
		if e = owner.Run(ctx, func(scope *cooperative.Scope) error {
			_, tools, e := s.Catalog(scope)
			for _, tool := range tools {
				actual = append(actual, tool.Name)
			}
			return e
		}); e != nil || !reflect.DeepEqual(actual, source.Rows[n]) {
			t.Fatalf("bare catalog row %d Go%v Source%v %v", n, actual, source.Rows[n], e)
		}
	}
}
