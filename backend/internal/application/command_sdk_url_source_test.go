package application

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceSDKURLDefaultsExplicitFormatsAndInitialPrompt(t *testing.T) {
	allocation, _ := filepath.EvalSymlinks(t.TempDir())
	home := filepath.Join(allocation, "source-home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{}
	flags := [][]string{{}, {"--input-format", "stream-json"}, {"--output-format", "stream-json"}, {"--verbose"}, {"--input-format", "text"}, {"--output-format", "json"}, {"--input-format", "stream-json", "--output-format", "text"}, {"--input-format", "text", "--output-format", "stream-json"}}
	for _, row := range flags {
		options := map[string]any{"sdkUrl": "ws://127.0.0.1:12345/private"}
		for i := 0; i < len(row); i++ {
			key := row[i]
			if key == "--verbose" {
				options["verbose"] = true
			} else {
				i++
				if key == "--input-format" {
					options["inputFormat"] = row[i]
				} else {
					options["outputFormat"] = row[i]
				}
			}
		}
		rows = append(rows, map[string]any{"options": options, "prompt": "actual positional prompt"})
	}
	fixture, _ := filepath.Abs("../../testdata/native-sdk-url-options.fixture.ts")
	c := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	c.Dir = home
	c.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_REMOTEIO_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	c.Stdin = bytes.NewReader(raw(map[string]any{"rows": rows}))
	data, err := c.CombinedOutput()
	if err != nil {
		t.Fatal("Source SDK URL consumer", err, string(data))
	}
	var source struct {
		Rows         []protocol.JSONObject
		Capabilities protocol.JSONObject
	}
	if err = json.Unmarshal(data, &source); err != nil || len(source.Rows) != len(rows) {
		t.Fatal(err, string(data))
	}
	if string(source.Capabilities.Get("blocked")) != "[]" {
		t.Fatal(string(source.Capabilities.Get("blocked")))
	}
	for i, row := range flags {
		args := append([]string{"--sdk-url", "ws://127.0.0.1:12345/private"}, row...)
		args = append(args, "actual positional prompt")
		command, _, err := ParseCommand(args, "fixture", home, home)
		want := source.Rows[i].Clone()
		want.Delete("initial")
		got := protocol.NewJSONObject()
		if err != nil {
			got.Set("error", raw(strings.TrimSuffix(err.Error(), "\n")))
		} else {
			got = protocol.NewJSONObject(protocol.JSONField{Name: "inputFormat", Value: raw(command.InputFormat)}, protocol.JSONField{Name: "outputFormat", Value: raw(command.OutputFormat)}, protocol.JSONField{Name: "verbose", Value: raw(command.Verbose)}, protocol.JSONField{Name: "print", Value: raw(command.Print)})
			if command.Arguments.SDKURL != "ws://127.0.0.1:12345/private" || command.Prompt != "actual positional prompt" {
				t.Fatal("public URL/prompt lost", command)
			}
		}
		var a, b any
		_ = json.Unmarshal(raw(want), &a)
		_ = json.Unmarshal(raw(got), &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("flags%v Source%s Go%s", row, raw(want), raw(got))
		}
	}
}
