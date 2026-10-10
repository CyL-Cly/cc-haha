package skillcommands

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestListingBudgetsUnicodeBundledPreservationMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	command := func(name, description, source string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: listingQuoted(name)}, protocol.JSONField{Name: "description", Value: listingQuoted(description)}, protocol.JSONField{Name: "type", Value: listingQuoted("prompt")}, protocol.JSONField{Name: "source", Value: listingQuoted(source)})
	}
	rows := []struct {
		commands []protocol.JSONObject
		window   float64
		budget   string
	}{
		{[]protocol.JSONObject{}, 200000, ""},
		{[]protocol.JSONObject{command("ordinary", "normal description", "projectSettings")}, 200000, ""},
		{[]protocol.JSONObject{command("long", strings.Repeat("a", 300), "projectSettings")}, 200000, ""},
		{[]protocol.JSONObject{command("中文", "中文说明 👨‍👩‍👧‍👦 ⚠️", "projectSettings")}, 200000, ""},
	}
	set := []protocol.JSONObject{command("bundle", strings.Repeat("b", 80), "bundled"), command("other", strings.Repeat("汉字👨‍👩‍👧‍👦 ", 20), "projectSettings"), command("last", strings.Repeat("c", 150), "plugin")}
	for _, budget := range []string{"0", "10", "-4", "90", "180", "230", "8000", " 0x100 ", "NaN", "Infinity"} {
		rows = append(rows, struct {
			commands []protocol.JSONObject
			window   float64
			budget   string
		}{set, 1000, budget})
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	values := []map[string]any{}
	for _, row := range rows {
		raw, _ := json.Marshal(row.commands)
		values = append(values, map[string]any{"commands": string(raw), "contextWindow": row.window, "budget": row.budget})
	}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "listings": values})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-skill-tool.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_SKILL_TOOL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.3000s", e, output)
	}
	var source struct {
		Listings     []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(output, &source); e != nil || len(source.Listings) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source %v %.3000s", e, output)
	}
	for i, row := range rows {
		got, e := FormatWithinBudget(row.commands, row.window, row.budget)
		if e != nil {
			t.Fatal(e)
		}
		if string(protocol.StringifyValidatedJavaScriptJSON(got)) != string(protocol.StringifyValidatedJavaScriptJSON(source.Listings[i])) {
			t.Errorf("row%d Go=%s Source=%s", i, got, source.Listings[i])
		}
	}
}
