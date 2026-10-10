package pluginschema

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
)

func TestOriginalStringFormatIssuesRetainZodCodeAndDetails(t *testing.T) {
	home := t.TempDir()
	cases := []json.RawMessage{[]byte(`{"name":"fixture","commands":"file.md"}`), []byte(`{"name":"fixture","agents":"./file.txt"}`), []byte(`{"name":"fixture","hooks":"./file.txt"}`), []byte(`{"name":"fixture","mcpServers":{"server":{"type":"http","url":"invalid-url"}}}`), []byte(`{"name":"fixture","mcpServers":{"server":{"type":"http","url":"http://127.0.0.1/"}},"channels":{"server":"invalid"}}`), []byte(`{"name":"fixture","dependencies":["bad@id@market"]}`), []byte(`{"name":"fixture","userConfig":{"1bad":{"type":"string","title":"Bad","description":"d"}}}`), []byte(`{"name":"","commands":"../bad"}`)}
	fixture, _ := filepath.Abs("../../testdata/native-cli-plugin-validate.fixture.ts")
	profile, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profile, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PLUGIN_VALIDATE_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"schemaCases": cases})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	body, err := cmd.Output()
	if err != nil {
		t.Fatal("actual Source schema", err, stderr.String())
	}
	var source []json.RawMessage
	if err = json.Unmarshal(body, &source); err != nil || len(source) != len(cases) {
		t.Fatal(err, string(body))
	}
	schemas, err := Declarations()
	if err != nil {
		t.Fatal(err)
	}
	for i, value := range cases {
		_, issues := Validate(schemas.Manifest, value)
		actual := ErrorJSON(issues, nil)
		var a, b any
		if err = json.Unmarshal([]byte(actual), &a); err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(source[i], &b); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a, b) {
			t.Errorf("case%d Source=%s Go=%s", i, source[i], actual)
		}
	}
}
