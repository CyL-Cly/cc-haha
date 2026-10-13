package agentcatalog

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

func TestActualSourceCLIJSONMapWholeValidationAndCatalogInstall(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	valid := `{"worker":{"description":" Actual worker ","prompt":" Source CLI prompt ","tools":["Read"],"model":"INHERIT","memory":"project","future":true},"second":{"description":"Second worker","prompt":"Second prompt","tools":[]}}`
	rows := []map[string]any{}
	for _, text := range []string{valid, `{}`, `{"__proto__":{"description":"prototype","prompt":"prototype prompt"},"":"empty name"}`, `{"":{"description":"empty name","prompt":"empty key prompt"}}`, `null`, `false`, `[]`, `[1]`, `{bad`, `{"valid":{"description":"yes","prompt":"ok"},"bad":{"description":" ","prompt":"bad"}}`, `{"valid":{"description":"yes","prompt":"ok"},"bad":null}`, `{"worker":{"description":"description","prompt":"prompt","hooks":{"unknown":[]}}}`} {
		for _, memory := range []bool{false, true} {
			rows = append(rows, map[string]any{"text": text, "memory": memory})
		}
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cli-agent-map.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	child.Dir = home
	child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLI_AGENT_MAP_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	child.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	child.Stderr = &stderr
	output, err := child.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var expected []json.RawMessage
	if json.Unmarshal(output, &expected) != nil || len(expected) != len(rows) {
		t.Fatal(string(output))
	}
	for index, row := range rows {
		f := newFixture(t)
		f.env["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "true"
		if row["memory"].(bool) {
			f.env["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "false"
		}
		service := f.service()
		if err := f.owner.Run(ctx, func(scope *cooperative.Scope) error {
			return service.InstallCLIJSON(scope, json.RawMessage(row["text"].(string)))
		}); err != nil {
			t.Error(index, err)
			continue
		}
		fields := []any{}
		for _, definition := range service.cli {
			fields = append(fields, definition.Fields)
		}
		actual, _ := json.Marshal(fields)
		var left, right any
		_ = json.Unmarshal(actual, &left)
		_ = json.Unmarshal(expected[index], &right)
		if !reflect.DeepEqual(left, right) {
			t.Error("actual Source CLI map differs", index, string(actual), string(expected[index]))
		}
		result, err := f.list(service)
		if err != nil {
			t.Fatal(err)
		}
		for _, definition := range service.cli {
			if findName(result.Active, definition.Name) == nil {
				t.Error("CLI definition never reached active catalog", index, definition.Name)
			}
		}
	}
}
