package skillcommands

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

func TestBuiltinSecurityReviewActualSourceMetadataPromptInternalPermissionsAndShell(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("testdata/builtin-security-review.fixture.ts")
	rows := []map[string]any{{"userType": "external", "shell": "bash", "args": "ignored"}, {"userType": "external", "shell": "powershell", "args": "ignored"}, {"userType": "external", "shell": nil, "args": ""}, {"userType": "ant", "shell": nil, "args": "do not execute"}, {"userType": "Ant", "shell": "bash", "args": "中文😀"}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SECURITY_REVIEW_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	data, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(data)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(raw))
	}
	var source struct {
		Rows []struct {
			Metadata map[string]any
			Prompt   []struct{ Type, Text string }
			Request  map[string]any
			Error    string
		}
	}
	if err = json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err, string(raw))
	}
	for i, row := range rows {
		var request map[string]any
		shell, _ := row["shell"].(string)
		definition, err := NativeSecurityReviewCommand(func(Invocation, string) (string, error) { return row["userType"].(string), nil }, func(Invocation) (string, error) { return shell, nil })
		if err != nil {
			t.Fatal(err)
		}
		args := row["args"].(string)
		text, err := definition.Prompt(ctx, &args, Invocation{ExecuteShell: func(_ context.Context, r ShellRequest) (string, error) {
			request = map[string]any{"text": r.Text, "command": r.SlashCommand, "shell": r.Shell, "allowedTools": r.AllowedTools}
			return strings.ReplaceAll(r.Text, "!`git status`", "PHYSICAL_STATUS_PLACEHOLDER"), nil
		}})
		metadata, _ := json.Marshal(definition.Metadata())
		var actual map[string]any
		_ = json.Unmarshal(metadata, &actual)
		req, _ := json.Marshal(request)
		var actualRequest map[string]any
		_ = json.Unmarshal(req, &actualRequest)
		want := source.Rows[i]
		message := ""
		if err != nil {
			message = err.Error()
		}
		if !reflect.DeepEqual(actual, want.Metadata) || !reflect.DeepEqual(actualRequest, want.Request) || message != want.Error || err == nil && (len(want.Prompt) != 1 || want.Prompt[0].Type != "text" || text != want.Prompt[0].Text) {
			t.Errorf("case%d metadata%v/%v request%v/%v err%v/%q prompt equal=%v", i, actual, want.Metadata, actualRequest, want.Request, err, want.Error, len(want.Prompt) > 0 && text == want.Prompt[0].Text)
		}
	}
}
