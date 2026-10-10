package application

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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestQuestionDesktopActualDenyMessagesAndManualMarkerSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun runner not configured")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.Mkdir(config, 0700)
	_ = os.Mkdir(filepath.Join(home, "tmp"), 0700)
	denials := []map[string]string{}
	gotDenials := []string{}
	for _, tool := range []string{"AskUserQuestion", "ExitPlanMode", "Write", ""} {
		for _, feedback := range []string{"", " \ufeff user choices \u00a0 "} {
			denials = append(denials, map[string]string{"tool": tool, "feedback": feedback})
			gotDenials = append(gotDenials, buildDesktopDenyMessage(tool, feedback))
		}
	}
	inputs := []protocol.JSONObject{obj([]byte(`{"questions":[],"metadata":{"source":"remember","autoAnswered":true,"unknown":"preserved"},"other":true}`)), obj([]byte(`{"metadata":{"autoAnswered":false}}`)), obj([]byte(`{"metadata":null}`)), obj([]byte(`{"metadata":[true]}`)), obj([]byte(`{"other":3}`))}
	actual := []any{}
	for _, input := range inputs {
		decision, err := desktopPermissionDecision(protocol.NewJSONObject(field("allowed", true), protocol.JSONField{Name: "updatedInput", Value: raw(input)}), "AskUserQuestion")
		if err != nil {
			t.Fatal(err)
		}
		var value any
		_ = json.Unmarshal(decision.UpdatedInput, &value)
		actual = append(actual, value)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-question-content.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_QUESTION_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "denials": denials, "strip": inputs}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source desktop permission %v %.2000s", err, stderr.String())
	}
	var source struct {
		Denials      []string
		Stripped     []any
		Capabilities struct{ Blocked []string }
	}
	if json.Unmarshal(output, &source) != nil {
		t.Fatalf("Source JSON %.1500s", output)
	}
	if len(source.Capabilities.Blocked) > 0 {
		t.Fatal("fixture escaped", source.Capabilities.Blocked)
	}
	if !reflect.DeepEqual(source.Denials, gotDenials) {
		t.Errorf("deny Go=%v Source=%v", gotDenials, source.Denials)
	}
	if !reflect.DeepEqual(source.Stripped, actual) {
		t.Errorf("manual marker Go=%s Source=%s", raw(actual), raw(source.Stripped))
	}
}
