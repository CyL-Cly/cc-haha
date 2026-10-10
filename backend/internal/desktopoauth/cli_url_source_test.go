package desktopoauth

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCLIManualConsoleHintsAndScopesActualSourceURL(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	rows := []map[string]any{}
	for _, manual := range []bool{false, true} {
		for _, claudeAI := range []bool{false, true} {
			for _, inference := range []bool{false, true} {
				rows = append(rows, map[string]any{"codeChallenge": "owned-challenge", "state": "owned-state", "port": float64(12345), "isManual": manual, "loginWithClaudeAi": claudeAI, "inferenceOnly": inference, "orgUUID": "owned-org", "loginHint": "fake+用户@example.invalid", "loginMethod": "sso"})
			}
		}
	}
	fixture, _ := filepath.Abs("../../testdata/native-cli-auth-url.fixture.ts")
	profile, _ := filepath.Abs("../../testdata/cli-auth-loopback-only.sb")
	cmd := exec.Command("/usr/bin/sandbox-exec", "-f", profile, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLI_AUTH_URL_FIXTURE=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "USER_TYPE=external", "NODE_ENV=production"}
	raw, _ := json.Marshal(rows)
	cmd.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var expected []string
	if e = json.Unmarshal(output, &expected); e != nil {
		t.Fatal(e)
	}
	config, e := ResolveConfig(map[string]string{})
	if e != nil {
		t.Fatal(e)
	}
	for i, row := range rows {
		got, e := BuildCLIAuthURL(config, CLIAuthURL{Challenge: row["codeChallenge"].(string), State: row["state"].(string), Port: 12345, Manual: row["isManual"].(bool), ClaudeAI: row["loginWithClaudeAi"].(bool), InferenceOnly: row["inferenceOnly"].(bool), Organization: row["orgUUID"].(string), Email: row["loginHint"].(string), LoginMethod: row["loginMethod"].(string)})
		if e != nil || got != expected[i] {
			t.Errorf("actual Source CLI URL case%d: %v Go=%s Source=%s", i, e, got, expected[i])
		}
	}
}
