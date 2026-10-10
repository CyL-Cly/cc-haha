package sdkhost

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

func TestActualSourceCommandDescriptionAnnotationsUseNativeMetadataAndKeepRawCatalog(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual pinned Source Bun required")
	}
	rows := []protocol.JSONObject{}
	add := func(name, kind, source string, properties ...protocol.JSONField) {
		values := []protocol.JSONField{{Name: "name", Value: quote(name)}, {Name: "type", Value: quote(kind)}, {Name: "description", Value: quote("原始😀 description " + name)}}
		if source != "" {
			values = append(values, protocol.JSONField{Name: "source", Value: quote(source)})
		}
		rows = append(rows, protocol.NewJSONObject(append(values, properties...)...))
	}
	for _, source := range []string{"projectSettings", "userSettings", "localSettings", "flagSettings", "policySettings", "builtin", "mcp", "bundled", "unrecognized", ""} {
		add("source-"+source, "prompt", source)
	}
	add("plugin-missing-info", "prompt", "plugin")
	add("plugin-null-info", "prompt", "plugin", protocol.JSONField{Name: "pluginInfo", Value: json.RawMessage(`null`)})
	for i, name := range []json.RawMessage{json.RawMessage(`"actual-manifest"`), json.RawMessage(`""`), json.RawMessage(`null`), json.RawMessage(`false`), json.RawMessage(`0`), json.RawMessage(`42`), json.RawMessage(`[]`), json.RawMessage(`{"label":"actual"}`)} {
		add("plugin-condition-"+string(rune('a'+i)), "prompt", "plugin", protocol.JSONField{Name: "pluginInfo", Value: raw(map[string]any{"pluginManifest": map[string]any{"name": name}})})
	}
	add("workflow-priority", "prompt", "plugin", protocol.JSONField{Name: "kind", Value: quote("workflow")}, protocol.JSONField{Name: "pluginInfo", Value: json.RawMessage(`{"pluginManifest":{"name":"must-not-win"}}`)})
	add("prompt-display-args", "prompt", "userSettings", protocol.JSONField{Name: "userFacingName", Value: quote("actual-display")}, protocol.JSONField{Name: "argumentHint", Value: quote("<args> 中文")}, protocol.JSONField{Name: "argNames", Value: json.RawMessage(`["one","two"]`)}, protocol.JSONField{Name: "allowedTools", Value: json.RawMessage(`["Read"]`)}, protocol.JSONField{Name: "isHidden", Value: json.RawMessage(`true`)})
	add("suppressed", "prompt", "projectSettings", protocol.JSONField{Name: "userInvocable", Value: json.RawMessage(`false`)})
	add("local-does-not-annotate", "local", "projectSettings", protocol.JSONField{Name: "kind", Value: quote("workflow")})
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("testdata/command-description-source.fixture.ts")
	sandbox, _ := filepath.Abs("../../testdata/loopback-only.sb")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_COMMAND_DESCRIPTION_FIXTURE=1", "ANTHROPIC_API_KEY=fake-command-source", "ANTHROPIC_BASE_URL=http://127.0.0.1:9", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "commands": rows}))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	data, err := command.Output()
	if err != nil {
		t.Fatal("actual Source formatter", err, stderr.String())
	}
	var source struct {
		Control, System, OriginalRows []map[string]any
		Capabilities                  struct{ Blocked []string }
	}
	if err = json.Unmarshal(data, &source); err != nil || len(source.Capabilities.Blocked) != 0 {
		t.Fatal(err, string(data))
	}
	native := []skillcommands.Definition{}
	snapshots := []protocol.JSONObject{}
	for _, row := range rows {
		if row.String("type") != "prompt" {
			continue
		}
		definition, err := skillcommands.NewNativeCommand(row, func(context.Context, *string, skillcommands.Invocation) (string, error) {
			return "", io.ErrUnexpectedEOF
		})
		if err != nil {
			t.Fatal(err)
		}
		native = append(native, definition)
		snapshots = append(snapshots, definition.Metadata())
	}
	registry := &PrintRegistry{}
	registry.ReplaceNativePrompts(native, nil, false)
	// Native definitions pass through the real publisher. The local declaration
	// uses the same production projection as the compiled nonprompt commands.
	all := append([]printCommandDefinition(nil), registry.commands[:len(native)]...)
	local := rows[len(rows)-1]
	all = append(all, printCommandDefinition{name: local.String("name"), description: local.String("description"), kind: local.String("type"), source: local.String("source")})
	goControl, goSystem := projectControlCommands(all), projectSystemCommands(all)
	canonical := func(value any) any {
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var out any
		if err = json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if !reflect.DeepEqual(canonical(goControl), canonical(source.Control)) {
		t.Fatalf("actual Source control descriptions differ\nGo%s\nSource%s", raw(goControl), raw(source.Control))
	}
	if !reflect.DeepEqual(canonical(goSystem), canonical(source.System)) {
		t.Fatalf("system/raw catalog changed\nGo%s\nSource%s", raw(goSystem), raw(source.System))
	}
	for i, definition := range native {
		if !reflect.DeepEqual(canonical(definition.Metadata()), canonical(snapshots[i])) {
			t.Fatal("model-facing/native raw descriptor changed", i)
		}
	}
	hintFound := false
	for _, command := range goControl {
		if command.Name == "actual-display" {
			hintFound = true
			if !command.Hidden || !command.RequiresArguments || !reflect.DeepEqual(command.ArgumentNames, []string{"one", "two"}) || command.ArgumentHint != "<args> 中文" {
				t.Fatal("annotation altered command args/hidden fields", command)
			}
		}
	}
	if !hintFound {
		t.Fatal("native display-name consumer missing")
	}
	t.Logf("actual Source formatter cases=%d, native publisher/raw metadata/system catalog preserved", len(rows))
}
