package clistartupenv

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

func TestTrustedEnvironmentAssignmentsAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/cli-startup-env.fixture.ts")
	t.Setenv("CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH", "1")
	for _, profile := range []string{"ordinary", "desktop", "host-managed", "ssh", "isolated"} {
		t.Run(profile, func(t *testing.T) {
			home := t.TempDir()
			env := `{"PROBE_KEEP":"spawn","PROBE_CHANGE":"spawn"}`
			switch profile {
			case "desktop":
				env = `{"CLAUDE_CODE_ENTRYPOINT":"claude-desktop","PROBE_KEEP":"spawn","PROBE_CHANGE":"spawn"}`
			case "host-managed":
				env = `{"CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST":"1","ANTHROPIC_API_KEY":"host","PROBE_KEEP":"spawn","PROBE_CHANGE":"spawn"}`
			case "ssh":
				env = `{"ANTHROPIC_UNIX_SOCKET":"/private-fixture.sock","ANTHROPIC_API_KEY":"ssh","PROBE_KEEP":"spawn","PROBE_CHANGE":"spawn"}`
			}
			global := `{"env":{"PROBE_ORDER":"global","PROBE_KEEP":"global"}}`
			user := `{"env":{"PROBE_ORDER":"user","PROBE_CHANGE":"user","ANTHROPIC_API_KEY":"user"}}`
			project := `{"env":{"PROBE_ORDER":"project","PROBE_PROJECT":"dangerous-post-trust"}}`
			local := `{"env":{"PROBE_ORDER":"local","PROBE_NEW":23,"PROBE_NULL":null,"PROBE_FALSE":false,"CC_HAHA_AGENT_TEAMS_ENABLED":"1"}}`
			cc := `{"env":{"PROBE_ORDER":"haha","PROBE_HAHA":"kept","ANTHROPIC_API_KEY":"haha"}}`
			ops := []fixtureOperation{{Kind: "safe"}, {Kind: "trusted"}, {Kind: "write", File: "local", Raw: `{"env":{"PROBE_ORDER":"updated","PROBE_CHANGE":"new"}}`}, {Kind: "resetSettings"}, {Kind: "trusted"}}
			input := fixtureInput{Home: home, Name: profile, Global: &global, User: &user, Project: &project, Local: &local, CcHaha: &cc, Environment: &env, Operations: ops}
			if profile == "isolated" {
				input.Allowed = []string{}
			}
			got, err := executeFixture(input)
			if err != nil {
				t.Fatal(err)
			}
			payload := map[string]any{"home": home, "name": profile, "global": global, "user": user, "project": project, "local": local, "ccHaha": cc, "environment": env}
			if input.Allowed != nil {
				payload["allowed"] = input.Allowed
			}
			operations := []map[string]string{}
			for _, op := range ops {
				operations = append(operations, map[string]string{"kind": op.Kind, "file": op.File, "raw": op.Raw})
			}
			payload["operations"] = operations
			data, _ := json.Marshal(payload)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, bun, "--feature=TRANSCRIPT_CLASSIFIER", "--no-env-file", fixture)
			command.Dir = home
			command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "USER_TYPE=external", "NODE_ENV=production", "CLAUDE_CODE_ENABLE_XAA=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLI_STARTUP_ENV_FIXTURE=1", "BUN_OPTIONS=--no-env-file"}
			command.Stdin = bytes.NewReader(data)
			var stderr bytes.Buffer
			command.Stderr = &stderr
			output, err := command.Output()
			if err != nil {
				t.Fatalf("actual managedEnv %v %s", err, stderr.String())
			}
			var want map[string]any
			if err = json.Unmarshal(output, &want); err != nil {
				t.Fatal(err, string(output))
			}
			encoded, _ := json.Marshal(got)
			var normalized map[string]any
			_ = json.Unmarshal(encoded, &normalized)
			actual := normalized["results"].([]any)
			expected := want["results"].([]any)
			for i := range expected {
				a, e := actual[i].(map[string]any), expected[i].(map[string]any)
				if !reflect.DeepEqual(a["environment"], e["environment"]) || !reflect.DeepEqual(a["error"], e["error"]) {
					t.Errorf("%d env got%s want%s", i, strings.ReplaceAll(string(rawForTrusted(a)), home, "<HOME>"), strings.ReplaceAll(string(rawForTrusted(e)), home, "<HOME>"))
				}
			}
		})
	}
}
func rawForTrusted(value any) []byte { raw, _ := json.Marshal(value); return raw }
