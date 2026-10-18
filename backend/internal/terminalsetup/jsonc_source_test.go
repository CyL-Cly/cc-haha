package terminalsetup

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

const shiftEnterBinding = `{"key":"shift+enter","command":"workbench.action.terminal.sendSequence","args":{"text":"\u001b\r"},"when":"terminalFocus"}`

func terminalSetupSource(t *testing.T, home string, input map[string]any) []byte {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("Pinned Source Bun required")
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-setup.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	input["home"] = home
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_SETUP_SOURCE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NO_COLOR=1", "NODE_ENV=production", "USER_TYPE=external", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1"}
	command.Stdin = bytes.NewReader(data)
	data, err = command.CombinedOutput()
	if err != nil {
		t.Fatalf("actual terminal setup Source %v %.5000s", err, data)
	}
	return data
}

func TestActualSourceJSONCAppendPreservesPhysicalCommentsFormattingAndBOM(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	contents := []string{"", " \t\n", "[]", "[ ]", "[\n]", "\ufeff[]", "{}", "null", "invalid", `[{}]`, `[{},]`, "[\n  {\"key\":\"another\"}\n]\n", "// header\n[\n    // before\n    {\"key\":\"https://unchanged/😀\"}, // tail\n]\n", "/* head */[ /* only comment */ ] // after", "[\r\n\t{\"key\":\"old\"}\r\n]\r\n", "[\n    {\n        \"key\": \"old\"\n    }\n]", "[\n  {},\n  {}\n]", "[null, false, 2, \"string\"]"}
	contents = append(contents, `[{} broken]`, `[{},,{}]`, `[{"key":"old"}`, `[{"key":"old"`, `[{},`, `[broken]`, `[1 2]`, `[// retained\n]`, `[] trailing`, `[{"key":}]`)
	contents = append(contents, `[01]`, `[0x1]`, `[1.]`, `[1e]`, `[1e+]`, `[1e999]`, `[-]`, `["unfinished`, `["invalid\q"]`, `["invalid\u12X4"]`, "[\"line\nbreak\"]", "[/* unfinished", `[/{"key":"old"}]`)
	data := terminalSetupSource(t, home, map[string]any{"jsonc": contents, "item": json.RawMessage(shiftEnterBinding)})
	var source struct {
		Values []struct {
			Content, Modified string
			Parsed            json.RawMessage
		}
	}
	if err = json.Unmarshal(data, &source); err != nil || len(source.Values) != len(contents) {
		t.Fatalf("invalid Source %v %.3000s", err, data)
	}
	for index, row := range source.Values {
		t.Run(string(rune('a'+index)), func(t *testing.T) {
			parsed, parseErr := parseJSONC(strings.TrimPrefix(row.Content, "\ufeff"))
			var actual, expected any
			if parseErr == nil {
				if err := json.Unmarshal(parsed.raw, &actual); err != nil {
					t.Fatalf("recovered parser returned invalid semantic JSON %s: %v", parsed.raw, err)
				}
			}
			if len(row.Parsed) > 0 {
				if err := json.Unmarshal(row.Parsed, &expected); err != nil {
					t.Fatal(err)
				}
			}
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("recovered JSONC input%q Go%#v Source%#v", row.Content, actual, expected)
			}
			if result := appendJSONC(row.Content, json.RawMessage(shiftEnterBinding)); result != row.Modified {
				t.Fatalf("physical JSONC input%q Go%q Source%q", row.Content, result, row.Modified)
			}
		})
	}
}
