package teamspawn

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestOriginalInProcessSystemCompositionAndMessageFormatting(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := []map[string]any{{"base": []string{"first", "second"}}, {"base": []string{}, "custom": "actual custom"}, {"base": []string{"original"}, "mode": "append", "override": "override", "custom": "custom"}, {"base": []string{"unused"}, "mode": "replace", "override": "exact replacement"}, {"base": []string{"first"}, "mode": "replace", "override": "", "custom": ""}, {"base": []string{"", "🌍\n"}, "mode": "other", "override": "ignored"}}
	actual := []json.RawMessage{}
	for _, row := range rows {
		base := []json.RawMessage{}
		for _, part := range row["base"].([]string) {
			base = append(base, quoted(part))
		}
		var custom, override json.RawMessage
		if v, ok := row["custom"]; ok {
			custom = quoted(v.(string))
		}
		if v, ok := row["override"]; ok {
			override = quoted(v.(string))
		}
		mode, _ := row["mode"].(string)
		value, err := SystemPrompt(base, custom, override, mode)
		if err != nil {
			t.Fatal(err)
		}
		actual = append(actual, value)
	}
	messages := []map[string]string{{"from": "team-lead", "content": "actual\nmessage"}, {"from": "peer", "content": "plain", "color": "blue", "summary": "short"}, {"from": "name\"quoted", "content": "<content>", "color": "", "summary": ""}}
	formatted := []string{}
	for _, row := range messages {
		formatted = append(formatted, TeammateMessage(row["from"], row["content"], row["color"], row["summary"]))
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-inprocess-teammate-prompt.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_INPROCESS_TEAM_PROMPT_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows, "messages": messages})
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(output))
	}
	var report struct {
		Rows         []json.RawMessage
		Messages     []string
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &report); err != nil || len(report.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output), stderr.String())
	}
	want, _ := json.Marshal(actual)
	got, _ := json.Marshal(report.Rows)
	if !bytes.Equal(want, got) || !reflect.DeepEqual(formatted, report.Messages) {
		t.Fatalf("Go %s %v\nSource %s %v", want, formatted, got, report.Messages)
	}
}
