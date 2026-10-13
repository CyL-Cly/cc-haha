package automode

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualAutoModeHandlersCritiqueProducerAndOutputs(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	type row struct {
		Operation string                     `json:"operation"`
		Model     string                     `json:"model,omitempty"`
		Sources   map[string]json.RawMessage `json:"sources"`
		Response  json.RawMessage            `json:"response"`
		Failure   string                     `json:"failure,omitempty"`
	}
	profiles := []map[string]json.RawMessage{
		{}, {"userSettings": []byte(`{"autoMode":{"classifyAllShell":true}}`)},
		{"userSettings": []byte(`{"autoMode":{"allow":[],"environment":["private home"]}}`)},
		{"userSettings": []byte(`{"autoMode":{"allow":["$defaults","first"],"hard_deny":["Never delete"],"soft_deny":[]}}`), "localSettings": []byte(`{"autoMode":{"allow":["second\ud800"],"environment":["中文"]}}`), "projectSettings": []byte(`{"autoMode":{"allow":["UNTRUSTED PROJECT"]}}`)},
		{"userSettings": []byte(`{"autoMode":{"allow":false}}`), "flagSettings": []byte(`{"autoMode":{"environment":[]}}`)},
		{"userSettings": []byte(`{"autoMode":{"allow":["prefix"],"deny":["external legacy ignored"]}}`), "policySettings": []byte(`{"autoMode":{"allow":["$defaults"],"hard_deny":["policy"]}}`)},
	}
	rows := []row{}
	for _, sources := range profiles {
		for _, operation := range []string{"defaults", "config", "critique"} {
			rows = append(rows, row{Operation: operation, Model: "fixture-model", Sources: sources, Response: []byte(`{"content":[{"type":"thinking","thinking":"hidden"},{"type":"text","text":"ORIGINAL CRITIQUE"},{"type":"text","text":"SECOND IGNORED"}]}`)})
		}
	}
	rows = append(rows, row{Operation: "critique", Sources: profiles[2], Response: []byte(`{"content":[]}`)})
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../testdata/native-auto-mode-command.fixture.ts")
	sandbox, _ := filepath.Abs("../../testdata/loopback-only.sb")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, bun, "--no-env-file", "--feature=TRANSCRIPT_CLASSIFIER", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_AUTO_COMMAND_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "ANTHROPIC_API_KEY=fake-auto-command", "ANTHROPIC_MODEL=fixture-model", "ANTHROPIC_BASE_URL=http://127.0.0.1:9", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "USER_TYPE=external", "PATH=/usr/bin:/bin"}
	raw, _ := json.Marshal(rows)
	cmd.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var original struct {
		Results []struct {
			Stdout, Stderr string
			Code           int
			Calls          []struct {
				Model, System, QuerySource string
				SkipSystemPromptPrefix     bool
				MaxTokens                  float64 `json:"max_tokens"`
				Messages                   []protocol.JSONObject
			}
		}
		Blocked []string
	}
	if err := json.Unmarshal(data, &original); err != nil || len(original.Results) != len(rows) || len(original.Blocked) != 0 {
		t.Fatal(err, string(data))
	}
	for i, input := range rows {
		want := original.Results[i]
		snapshot := Snapshot{TranscriptClassifier: true, Sources: input.Sources}
		var out bytes.Buffer
		switch input.Operation {
		case "defaults":
			err = DefaultsHandler(&out, true)
		case "config":
			err = ConfigHandler(&out, snapshot)
		case "critique":
			options, custom, e := CritiqueOptions(snapshot)
			if e != nil {
				t.Fatal(e)
			}
			if !custom {
				out.WriteString(CritiqueNoCustomRules)
				if len(want.Calls) != 0 {
					t.Fatal("Source entered model without custom sections", i)
				}
			} else {
				if len(want.Calls) != 1 {
					t.Fatal("Source did not produce single side query", i)
				}
				call := want.Calls[0]
				message, _ := json.Marshal(options.Messages)
				expectedMessages, _ := json.Marshal(call.Messages)
				if call.QuerySource != "auto_mode_critique" || call.MaxTokens != options.MaxTokens || call.SkipSystemPromptPrefix != options.SkipPrefix || call.System != options.System[0].String("text") || !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(message), protocol.StringifyValidatedJavaScriptJSON(expectedMessages)) {
					t.Fatalf("actual sideQuery producer row%d differs\nGo:%s\nSource:%s", i, message, expectedMessages)
				}
				out.WriteString(CritiqueAnalyzing)
				err = WriteCritiqueResponse(&out, input.Response)
			}
		}
		if err != nil || want.Code != 0 || want.Stderr != "" || out.String() != want.Stdout {
			t.Fatalf("row%d output differs err%v Sourcecode%d\nGo:%q\nSource:%q stderr%q", i, err, want.Code, out.String(), want.Stdout, want.Stderr)
		}
	}
}
