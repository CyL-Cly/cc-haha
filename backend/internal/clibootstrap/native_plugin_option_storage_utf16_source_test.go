package clibootstrap

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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestOriginalPluginOptionsUTF16KeysKeepStorageClassificationAndScrubbing(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	for _, profile := range []string{"initial-sensitive", "swap-stores", "conversion-error-before-write"} {
		t.Run(profile, func(t *testing.T) {
			f := newNativePluginFixture(t)
			f.n.CWD = f.home
			initialSettings := `{"future":{"keep":true}}`
			initialCredentials := `{"futureSecure":{"keep":true}}`
			f.write(t, filepath.Join(f.config, "settings.json"), initialSettings)
			f.write(t, filepath.Join(f.config, ".credentials.json"), initialCredentials)
			p, err := NewNativeProcessSettings(f.n, runtime.GOOS, DefaultState())
			if err != nil {
				t.Fatal(err)
			}
			secure, err := securestorage.New(securestorage.Options{Home: f.home, ConfigDir: f.config, ConfigDirExplicit: true, Platform: "darwin", Environment: f.n.Environment, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }})
			if err != nil {
				t.Fatal(err)
			}
			options, err := p.PluginOptions(secure)
			if err != nil {
				t.Fatal(err)
			}
			parse := func(text string) protocol.JSONObject {
				var object protocol.JSONObject
				if err := object.UnmarshalJSON([]byte(text)); err != nil {
					t.Fatal(err)
				}
				return object
			}
			values := parse(`{"secure\ud800":"SENSITIVE_VALUE\udc00","plain\udc00":"plain-value\ud800"}`)
			if profile == "conversion-error-before-write" {
				values = parse(`{"secure\ud800":{"toString":"not callable"},"plain\udc00":"must-not-write"}`)
			}
			schemas := []protocol.JSONObject{parse(`{"secure\ud800":{"sensitive":true},"plain\udc00":{"sensitive":false}}`)}
			if profile == "swap-stores" {
				schemas = append(schemas, parse(`{"secure\ud800":{"sensitive":false},"plain\udc00":{"sensitive":true}}`))
			}
			operations := []map[string]any{}
			failures := []string{}
			for _, schema := range schemas {
				err := options.Save(context.Background(), "sample@mkt", values, schema)
				if profile == "conversion-error-before-write" {
					if err == nil {
						t.Fatal("invalid ordinary conversion was accepted")
					}
					failures = append(failures, err.Error())
				} else if err != nil {
					t.Fatal(err)
				}
				operations = append(operations, map[string]any{"action": "save", "id": "sample@mkt", "values": values, "schema": schema, "captureError": profile == "conversion-error-before-write"})
			}
			actualSettings, err := os.ReadFile(filepath.Join(f.config, "settings.json"))
			if err != nil {
				t.Fatal(err)
			}
			actualCredentials, err := os.ReadFile(filepath.Join(f.config, ".credentials.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-plugin-options.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			command.Dir = f.home
			command.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_PLUGIN_OPTIONS_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
			input, _ := json.Marshal(map[string]any{"settings": initialSettings, "credentials": initialCredentials, "operations": operations})
			command.Stdin = bytes.NewReader(input)
			var stderr bytes.Buffer
			command.Stderr = &stderr
			output, err := command.Output()
			if err != nil {
				t.Fatal(err, stderr.String())
			}
			var expected struct {
				Settings, Credentials string
				Failures              []string
			}
			if err := json.Unmarshal(output, &expected); err != nil {
				t.Fatal(err)
			}
			if len(failures) != 0 && !reflect.DeepEqual(failures, expected.Failures) {
				t.Fatal("actual Source conversion failure changed", failures, expected.Failures)
			}
			for name, pair := range map[string][2][]byte{"settings": {actualSettings, []byte(expected.Settings)}, "credentials": {actualCredentials, []byte(expected.Credentials)}} {
				got, want := parse(string(pair[0])), parse(string(pair[1]))
				a, _ := got.MarshalJSON()
				b, _ := want.MarshalJSON()
				if !bytes.Equal(a, b) {
					t.Errorf("Source actual %s storage differs\nGo %s\nSource %s", name, a, b)
				}
			}
		})
	}
}
