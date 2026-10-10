package cloudauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestActualSourceAWSRefreshExportProbesTrustValidationAndClearOrder(t *testing.T) {
	valid := `{"Credentials":{"AccessKeyId":"EXPORTED_PRIVATE","SecretAccessKey":"PRIVATE_SECRET","SessionToken":"PRIVATE_TOKEN","Expiration":"ignored","unknown":true},"unknownRoot":9}`
	rows := []map[string]any{
		{},
		{"refresh": "configured refresh", "export": "configured export", "probes": []bool{true, true}, "stdout": valid},
		{"refresh": "configured refresh", "export": "configured export", "probes": []bool{false, true}, "stdout": valid},
		{"refresh": "configured refresh", "export": "configured export", "probes": []bool{false, false}, "stdout": valid},
		{"refresh": "configured refresh", "export": "configured export", "probes": []bool{false, false}, "refreshExit": 1, "exportExit": 1},
		{"refresh": "configured refresh", "export": "configured export", "refreshProject": true, "exportProject": true, "stdout": valid},
		{"refresh": "configured refresh", "export": "configured export", "refreshProject": true, "exportProject": true, "trusted": true, "stdout": valid},
		{"export": "configured export", "exportProject": true, "nonInteractive": true, "stdout": valid},
		{"export": "configured export", "stdout": "not json"},
		{"export": "configured export", "stdout": `{"Credentials":{"AccessKeyId":"a","SecretAccessKey":"b","SessionToken":""}}`},
		{"export": "configured export", "stdout": `{"Credentials":{"AccessKeyId":1,"SecretAccessKey":"b","SessionToken":"t"}}`},
		{"export": "configured export", "stdout": `["unexpected"]`},
		{"export": "configured export", "stdout": " \t" + valid + "\n"},
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-aws-helpers.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_AWS_HELPERS_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	raw, _ := json.Marshal(map[string]any{"rows": rows})
	cmd.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct{ Rows []json.RawMessage }
	if err = json.Unmarshal(out, &source); err != nil {
		t.Fatal(err)
	}
	for index, row := range rows {
		calls := []string{}
		probe := 0
		getString := func(key string) string { value, _ := row[key].(string); return value }
		getBool := func(key string) bool { value, _ := row[key].(bool); return value }
		value, err := RefreshAndExport(ctx, AWSHelperOptions{Refresh: getString("refresh"), Export: getString("export"), RefreshFromProject: getBool("refreshProject"), ExportFromProject: getBool("exportProject"), Trusted: getBool("trusted"), NonInteractive: getBool("nonInteractive"), Probe: func(context.Context) error {
			calls = append(calls, "probe")
			probes, _ := row["probes"].([]bool)
			ok := probe < len(probes) && probes[probe]
			probe++
			if !ok {
				return errors.New("private invalid identity")
			}
			return nil
		}, Run: func(_ context.Context, command string, timeout time.Duration) (CommandOutput, error) {
			if timeout > 0 {
				calls = append(calls, fmt.Sprintf("refresh:%s:%d", command, timeout.Milliseconds()))
				code, _ := row["refreshExit"].(int)
				return CommandOutput{ExitCode: code}, nil
			}
			calls = append(calls, "export:"+command)
			code, _ := row["exportExit"].(int)
			return CommandOutput{ExitCode: code, Stdout: getString("stdout")}, nil
		}, ClearINI: func(context.Context) error { calls = append(calls, "clear"); return nil }})
		if err != nil {
			t.Fatal(err)
		}
		compareAWS(t, index, map[string]any{"value": value, "calls": calls}, source.Rows[index])
	}
}
