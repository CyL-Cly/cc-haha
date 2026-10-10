package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualHelperClientHeadersAgainstOriginalGetAnthropicClient(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	rows := []map[string]any{{"env": map[string]string{}}, {"env": map[string]string{"ANTHROPIC_API_KEY": "environment-key"}}, {"env": map[string]string{"ANTHROPIC_AUTH_TOKEN": "environment-token"}}, {"env": map[string]string{"ANTHROPIC_API_KEY": "environment-key", "ANTHROPIC_AUTH_TOKEN": "environment-token"}}, {"env": map[string]string{}, "fail": true}, {"env": map[string]string{"ANTHROPIC_CUSTOM_HEADERS": "Authorization: custom-before-helper"}}}
	got := []map[string]any{}
	for _, row := range rows {
		var headers map[string]any
		var count string
		peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var key, token any
			if values, present := r.Header["X-Api-Key"]; present {
				key = values[0]
			}
			if values, present := r.Header["Authorization"]; present {
				token = values[0]
			}
			headers = map[string]any{"key": key, "token": token}
			emitPlanFixture(w, 1, "", "AUTH_DONE")
		}))
		m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
			delete(n.Environment, "ANTHROPIC_API_KEY")
			delete(n.Environment, "ANTHROPIC_AUTH_TOKEN")
			delete(n.Environment, "CLAUDE_CODE_SIMPLE")
			for key, value := range row["env"].(map[string]string) {
				n.Environment[key] = value
			}
			count = filepath.Join(n.CWD, "helper-count")
			command := "printf 'call\\n' >> " + settingsShellQuote(count) + "; printf '  helper-value\\n'"
			if row["fail"] == true {
				command = "printf 'call\\n' >> " + settingsShellQuote(count) + "; printf ' helper failed\\n' >&2; exit 7"
			}
			if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), rawMain(map[string]any{"apiKeyHelper": command, "awsCredentialExport": "unowned-cloud-helper", "awsAuthRefresh": "unowned-cloud-helper", "gcpAuthRefresh": "unowned-cloud-helper", "enabledPlugins": map[string]bool{"sample@mkt": false}}), 0600); err != nil {
				t.Fatal(err)
			}
		}, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"Read"} })
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Actual credential helper client"})})
		if err := m.RunTurn(ctx, input); err != nil {
			cancel()
			peer.Close()
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(count)
		headers["executions"] = strings.Count(string(raw), "call")
		got = append(got, headers)
		if err := m.ShutdownSession(ctx); err != nil {
			t.Fatal(err)
		}
		cancel()
		peer.Close()
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-auth-helper-client.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CLAUDE_CODE_SIMPLE=1", "CC_HAHA_DISABLE_TERMINAL_SHELL_ENV=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_AUTH_HELPER_CLIENT_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1"}
	command.Stdin = bytes.NewReader(rawMain(map[string]any{"rows": rows}))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var expected any
	_ = json.Unmarshal(output, &expected)
	raw, _ := json.Marshal(got)
	var actual any
	_ = json.Unmarshal(raw, &actual)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal(fmt.Sprintf("original GetClient helper differs\ngot%s\nwant%s\nstderr%s", raw, output, stderr.String()))
	}
}
