package clisettings

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

func TestNativeAPIKeyHelperSchemaAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	rows := []json.RawMessage{[]byte(`{"future":3,"apiKeyHelper":"printf key","env":{"PROBE":"value"}}`), []byte(`{"apiKeyHelper":""}`), []byte(`{"apiKeyHelper":"raw\ud800"}`), []byte(`{"apiKeyHelper":null}`), []byte(`{"apiKeyHelper":false}`), []byte(`{"apiKeyHelper":3}`), []byte(`{"apiKeyHelper":[]}`), []byte(`{"apiKeyHelper":{}}`)}
	for _, key := range []string{"awsCredentialExport", "awsAuthRefresh", "gcpAuthRefresh"} {
		for _, value := range []string{`"printf private"`, `""`, `"raw\ud800"`, `null`, `false`, `3`, `[]`, `{}`} {
			rows = append(rows, json.RawMessage(`{"future":true,"`+key+`":`+value+`}`))
		}
	}
	rows = append(rows, json.RawMessage(`{"gcpAuthRefresh":null,"awsAuthRefresh":false,"awsCredentialExport":[],"apiKeyHelper":3}`))
	actual := []any{}
	for _, row := range rows {
		parsed, err := ValidateNativeJSONContent(string(row), "fixture")
		if err != nil {
			t.Fatal(err)
		}
		actual = append(actual, map[string]any{"settings": parsed.Settings, "errors": parsed.Errors})
		if _, err := ValidateJSONContent(string(row), "fixture"); err == nil {
			t.Fatal("finite helper schema guard was removed")
		}
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-auth-helper-schema.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_AUTH_HELPER_SCHEMA_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production", "USER_TYPE=external"}
	input, _ := json.Marshal(map[string]any{"schemas": rows, "notices": []any{}})
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct{ Schemas []any }
	if err = json.Unmarshal(output, &source); err != nil {
		t.Fatal(err, string(output))
	}
	raw, _ := json.Marshal(actual)
	var got []any
	_ = json.Unmarshal(raw, &got)
	if !reflect.DeepEqual(got, source.Schemas) {
		t.Fatalf("actual helper schema mismatch\ngot%s\nwant%s", raw, output)
	}
}
