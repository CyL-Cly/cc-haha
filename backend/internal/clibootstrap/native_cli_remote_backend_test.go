package clibootstrap

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

	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeCLIRemoteGateActualSourceCacheEnvMemoConfigReloadAndExternalFalse(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned Source Bun required")
	}
	type step struct {
		Env      *string         `json:"env,omitempty"`
		Override json.RawMessage `json:"override,omitempty"`
	}
	str := func(s string) *string { return &s }
	cases := []struct {
		name, user, env string
		global          protocol.JSONObject
		steps           []step
	}{
		{"external-cached-payload", "external", `{"tengu_remote_backend":true}`, objectMain(`{"cachedGrowthBookFeatures":{"tengu_remote_backend":true},"growthBookOverrides":{"tengu_remote_backend":true}}`), []step{{}, {Override: []byte(`true`)}}},
		{"ant-env-false-wins-and-memo", "ant", `{"tengu_remote_backend":false}`, objectMain(`{"growthBookOverrides":{"tengu_remote_backend":true}}`), []step{{}, {Env: str(`{"tengu_remote_backend":true}`), Override: []byte(`true`)}}},
		{"ant-env-true-wins-and-memo", "ant", `{"tengu_remote_backend":true}`, objectMain(`{"growthBookOverrides":{"tengu_remote_backend":false}}`), []step{{}, {Env: str(`{"tengu_remote_backend":false}`), Override: []byte(`false`)}}},
		{"ant-config-read-is-fresh", "ant", `{}`, objectMain(`{"growthBookOverrides":{"tengu_remote_backend":false}}`), []step{{}, {Override: []byte(`true`)}, {Override: []byte(`false`)}}},
		{"ant-cached-payload-still-false", "ant", `{}`, objectMain(`{"cachedGrowthBookFeatures":{"tengu_remote_backend":true}}`), []step{{}}},
		{"ant-string-false-is-truthy", "ant", `{"tengu_remote_backend":"false"}`, protocol.NewJSONObject(), []step{{}}},
		{"ant-empty-string-shadows-config", "ant", `{"tengu_remote_backend":""}`, objectMain(`{"growthBookOverrides":{"tengu_remote_backend":true}}`), []step{{}}},
		{"ant-invalid-json-config-fallback", "ant", `{`, objectMain(`{"growthBookOverrides":{"tengu_remote_backend":true}}`), []step{{}}},
		{"ant-array-override-no-property", "ant", `[]`, objectMain(`{"growthBookOverrides":{"tengu_remote_backend":true}}`), []step{{}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			global, err := cliglobalconfig.New(cliglobalconfig.Options{Home: home, ConfigDir: str(filepath.Join(home, ".claude")), Stderr: func(string) {}})
			if err != nil {
				t.Fatal(err)
			}
			defer global.Close()
			if err := global.Enable(); err != nil {
				t.Fatal(err)
			}
			if err := global.Save(func(*protocol.JSONObject) (*protocol.JSONObject, error) {
				next := test.global.Clone()
				return &next, nil
			}); err != nil {
				t.Fatal(err)
			}
			env, _ := clistartupenv.NewEnvironment(protocol.NewJSONObject(protocol.JSONField{Name: "USER_TYPE", Value: quoteMain(test.user)}, protocol.JSONField{Name: "CLAUDE_INTERNAL_FC_OVERRIDES", Value: quoteMain(test.env)}))
			owner := cooperative.New()
			defer owner.Close()
			m := &MainPrint{Owner: owner, Global: global, environment: env, options: MainPrintOptions{Native: &NativeBoundary{NativeProcess: true}}}
			actual := []bool{}
			for _, change := range test.steps {
				if err := owner.Run(context.Background(), func(*cooperative.Scope) error {
					if change.Env != nil {
						if err := env.SetProperty(quoteMain("CLAUDE_INTERNAL_FC_OVERRIDES"), quoteMain(*change.Env)); err != nil {
							return err
						}
					}
					if change.Override != nil && test.user == "ant" {
						return global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
							next := current.Clone()
							overrides, _ := next.Object("growthBookOverrides")
							overrides = overrides.Clone()
							overrides.Set("tengu_remote_backend", change.Override)
							next.Set("growthBookOverrides", rawMain(overrides))
							return &next, nil
						})
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				enabled, err := m.NativeCLIRemoteBackendEnabled(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				actual = append(actual, enabled)
			}
			sourceHome := t.TempDir()
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cli-remote-gate.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			command.Dir = sourceHome
			command.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + sourceHome, "PATH=/usr/bin:/bin", "USER_TYPE=" + test.user, "CLAUDE_INTERNAL_FC_OVERRIDES=" + test.env, "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(sourceHome), "CC_HAHA_REMOTE_GATE_SOURCE=1"}
			data, _ := json.Marshal(map[string]any{"home": sourceHome, "global": test.global, "steps": test.steps})
			command.Stdin = bytes.NewReader(data)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatal(err, string(output))
			}
			var source struct {
				Values  []bool
				Blocked []string
			}
			if json.Unmarshal(output, &source) != nil || len(source.Blocked) != 0 || !reflect.DeepEqual(actual, source.Values) {
				t.Fatalf("actual gate %v Source %s", actual, output)
			}
		})
	}
}
func objectMain(text string) protocol.JSONObject {
	var value protocol.JSONObject
	_ = value.UnmarshalJSON([]byte(text))
	return value
}
