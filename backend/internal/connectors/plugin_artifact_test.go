package connectors

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

func TestOriginalConnectorSkillAndRemoteArtifacts(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun runner not configured")
	}
	catalog, err := DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	i := Installation{Directory: "/owned/runtime", Command: "/工具/O'Brien folder/cli", Args: []string{"--prefix", "with space"}, Environment: map[string]string{"DWS_CONFIG_DIR": "/owned/account", "DWS_KEYCHAIN_DIR": "/owned/keychain", "DWS_DISABLE_KEYCHAIN": "1", "TOKEN": "never-include-secret"}, EnvironmentOrder: []string{"DWS_CONFIG_DIR", "DWS_KEYCHAIN_DIR", "DWS_DISABLE_KEYCHAIN", "TOKEN"}}
	skills := []any{}
	rendered := []any{}
	for _, def := range catalog.Definitions {
		skills = append(skills, map[string]any{"definition": def, "installation": i})
		text, err := RenderConnectorSkill(catalog, def, i)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, "never-include-secret") {
			t.Fatal("wrapper exposed arbitrary environment secret")
		}
		rendered = append(rendered, map[string]any{"text": text})
	}
	remotes := append([]RemoteRecipe(nil), catalog.Remote...)
	for _, endpoint := range []string{"https://official.test/mcp?other=1&key=old#", "http://official.test/mcp", "https://user:pass@official.test/mcp", "https://official.test/mcp#fragment", "https://official.test/${bad}", "not a URL"} {
		r := catalog.Remote[0]
		r.Endpoint = endpoint
		r.Auth.Type = "api-key"
		r.Auth.In = "query"
		r.Auth.Name = "key"
		remotes = append(remotes, r)
	}
	remote := []any{}
	for _, r := range remotes {
		v, err := BuildRemotePlugin(r)
		if err != nil {
			remote = append(remote, map[string]any{"error": err.Error()})
		} else {
			remote = append(remote, map[string]any{"plugin": v})
		}
	}
	home := t.TempDir()
	if err = os.Mkdir(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-connector-artifacts.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_CONNECTOR_ARTIFACT_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "USER_TYPE=external"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"skills": skills, "remote": remotes}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	want, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source artifact child: %v; %s", err, truncate(stderr.String(), 2200))
	}
	actual := raw(map[string]any{"rendered": rendered, "remote": remote})
	var a, b map[string][]any
	if err = json.Unmarshal(actual, &a); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(want, &b); err != nil {
		t.Fatal(err)
	}
	for k, values := range a {
		if len(values) != len(b[k]) {
			t.Fatalf("%s length differs", k)
		}
		for index, value := range values {
			if !reflect.DeepEqual(value, b[k][index]) {
				t.Errorf("%s[%d] Go=%s Source=%s", k, index, truncate(string(raw(value)), 600), truncate(string(raw(b[k][index])), 600))
			}
		}
	}
}

func TestInstallationKeepsEnvironmentOrderAcrossPersistence(t *testing.T) {
	var i Installation
	source := []byte(`{"directory":"/managed","command":"cli","args":[],"env":{"DWS_CONFIG_DIR":"config","DWS_KEYCHAIN_DIR":"keychain","DWS_DISABLE_KEYCHAIN":"1"}}`)
	if err := json.Unmarshal(source, &i); err != nil {
		t.Fatal(err)
	}
	got, err := i.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, source) {
		t.Fatalf("environment property order changed: %s", got)
	}
	def := record(field("id", "dingtalk"), field("pluginId", "office-dingtalk@haha-connectors"), field("version", "1.0.61"))
	text, err := RenderConnectorSkill(Catalog{}, def, i)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(text, "DWS_KEYCHAIN_DIR=") > strings.Index(text, "DWS_DISABLE_KEYCHAIN=") {
		t.Fatal("native wrapper differs from original environment order")
	}
}
