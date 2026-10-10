package grokauth

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

func TestGrokFileNormalizationAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	rows := []string{`null`, `[]`, `3`, `"tokens"`, `{}`, `{"accessToken":"a","refreshToken":"r","expiresAt":1}`, `{"access_token":" a ","refresh_token":" r ","expires_at":"200000"}`, `{"accessToken":" a ","access_token":"other","refreshToken":"r","expiresAt":" 0xabc "}`, `{"accessToken":"a","refreshToken":"r","expiresAt":"0b101010"}`, `{"accessToken":"a","refreshToken":"r","expiresAt":"0o10"}`, `{"accessToken":"a","refreshToken":"r","expiresAt":"1e30"}`, `{"accessToken":"a","refreshToken":"r","expiresAt":"Infinity"}`, `{"accessToken":"a","refreshToken":"r","expiresAt":1e400}`, `{"accessToken":"a","refreshToken":"r","expiresAt":false}`, `{"accessToken":"a","refreshToken":"r","expiresAt":null,"expires_at":123}`, `{"accessToken":"a","refreshToken":"r","expiresAt":""}`, `{"accessToken":"a","refreshToken":"r","expiresAt":"bad"}`, `{"accessToken":"a","refreshToken":"r","expiresAt":"2026-10-07T08:30:00Z"}`, `{"accessToken":"a","refreshToken":"r","expiresAt":"Wed, 07 Oct 2026 08:30:00 GMT"}`, `{"accessToken":"a","refreshToken":"r","expiresAt":"2026-10-07"}`, `{"accessToken":"a","refreshToken":"r","expiresAt":123,"idToken":"i","email":" e ","clientId":" c ","scope":" s ","tokenType":" t ","future":true}`, `{"accessToken":"a","refreshToken":"r","expiresAt":123,"id_token":" i ","client_id":" c ","token_type":" t "}`, `{"accessToken":" ","access_token":"snake","refreshToken":"r","expiresAt":123}`, `{"accessToken":"a","refreshToken":"r","expiresAt":123,"email":null,"scope":3}`, `{"accessToken":"\ud800","refreshToken":"r","expiresAt":123}`, `{"accessToken":true,"refreshToken":"r","expiresAt":123}`, `{"accessToken":"a","refreshToken":3,"expiresAt":123}`, `{"accessToken":"a","refreshToken":"r","expiresAt":123,"clientId":[],"client_id":"correct"}`}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-grok-file.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_GROK_FILE_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(rows)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var expected []json.RawMessage
	if err = json.Unmarshal(raw, &expected); err != nil || len(expected) != len(rows) {
		t.Fatal(err, string(raw))
	}
	for i, row := range rows {
		value := NormalizeFile([]byte(row))
		raw, _ := json.Marshal(value)
		var got, want any
		_ = json.Unmarshal(raw, &got)
		_ = json.Unmarshal(expected[i], &want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("case%d Go%s Source%s", i, raw, expected[i])
		}
	}
}
