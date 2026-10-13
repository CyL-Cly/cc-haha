package clibootstrap

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

	"github.com/NanmiCoder/cc-haha/backend/internal/installationdiagnostics"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sandboxruntime"
)

func TestDoctorSandboxAndLinuxConfigAgainstActualSourceComponents(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	rows := []map[string]any{}
	for _, supported := range []bool{false, true} {
		for _, enabled := range []bool{false, true} {
			for bits := 0; bits < 16; bits++ {
				check := sandboxruntime.CheckLinuxDependencies(sandboxruntime.LinuxDependencyStatus{HasBwrap: bits&1 != 0, HasSocat: bits&2 != 0, HasSeccompBPF: bits&4 != 0, HasSeccompApply: bits&8 != 0})
				rows = append(rows, map[string]any{"kind": "sandbox", "supported": supported, "enabled": enabled, "check": check})
			}
		}
	}
	settings := []string{`{}`, `{"sandbox":{"enabled":false},"permissions":{"allow":["Read(/a/*)"]}}`, `{"sandbox":{"enabled":true},"permissions":{"allow":["Read(/a/**)","Read(/a/**/file)","Read(/b/?)","Read(/c/[ab])","Edit(/literal)","Bash(/ignored/*)","Read(*)","Read(/escaped\\(x\\)/*)"],"deny":["Edit(/d/*)","Read(/e/])"],"ask":["Read(/ask/*)"]}}`, `{"sandbox":{"enabled":true},"permissions":{"allow":["Read(/first/*)",null,"Read(/third/*)"],"deny":["Edit(/last/*)"]}}`, `{"sandbox":{"enabled":true},"permissions":{"allow":{"bad":"array"},"deny":["Read(/deny/*)"]}}`, `{"sandbox":{"enabled":true},"permissions":{"allow":["Read(/first/*)",42],"deny":["Read(/deny/*)"]}}`}
	for _, platform := range []string{"macos", "windows", "linux", "wsl"} {
		for _, raw := range settings {
			var obj any
			if e := json.Unmarshal([]byte(raw), &obj); e != nil {
				t.Fatal(e)
			}
			rows = append(rows, map[string]any{"kind": "glob", "platform": platform, "settings": obj})
		}
	}
	for _, gate := range []any{nil, false, true} {
		rows = append(rows, map[string]any{"kind": "keybinding", "gate": gate})
	}
	rows = append(rows, map[string]any{"kind": "mcp_only", "tools": []any{map[string]any{"name": "mcp__fixture__actual_probe", "isMcp": true}}})
	rows = append(rows, map[string]any{"kind": "mcp_only", "tools": []any{map[string]any{"name": "ToolSearch", "isMcp": false}, map[string]any{"name": "mcp__fixture__actual_probe", "isMcp": true}}})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-doctor-boundaries.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "cases": rows})
	cmd.Stdin = bytes.NewReader(input)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(output))
	}
	var source []struct {
		Text     string
		Patterns []string
		Enabled  bool
		Calls    [][]any
	}
	if e := json.Unmarshal(output, &source); e != nil {
		t.Fatal(e, string(output))
	}
	for i, row := range rows {
		switch row["kind"] {
		case "sandbox":
			check := row["check"].(sandboxruntime.DependencyCheck)
			got := nativeDoctorSandboxText(row["supported"].(bool), row["enabled"].(bool), check.Errors, check.Warnings)
			want := strings.NewReplacer("\n", "", "└ ", "", "  ", "").Replace(source[i].Text)
			actual := strings.NewReplacer("\n", "", "  ", "").Replace(got)
			if actual != want {
				t.Fatalf("row%d Source%q Go%q", i, source[i].Text, got)
			}
		case "glob":
			raw, _ := json.Marshal(row["settings"])
			obj := protocol.NewJSONObject()
			if e := obj.UnmarshalJSON(raw); e != nil {
				t.Fatal(e)
			}
			actual := []string{}
			if row["platform"] == "linux" || row["platform"] == "wsl" {
				actual = installationdiagnostics.LinuxGlobPatterns(obj)
			}
			if !reflect.DeepEqual(actual, source[i].Patterns) {
				t.Fatalf("row%d %s Source%v Go%v", i, raw, source[i].Patterns, actual)
			}
		case "keybinding":
			if !reflect.DeepEqual(source[i].Calls, [][]any{{"tengu_keybinding_customization_release", false}}) || source[i].Enabled != (row["gate"] == true) {
				t.Fatalf("actual Source keybinding gate changed: %#v", source[i])
			}
		case "mcp_only":
			if source[i].Enabled != (len(row["tools"].([]any)) == 2) {
				t.Fatalf("actual Source Doctor MCP-only tool search gate changed: %#v", source[i])
			}
		}
	}
}
