package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

func TestActualNativeSkillDiscoveryAndConditionalActivationAgainstSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun not configured")
	}
	f := newNativePluginFixture(t)
	f.n.Environment["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "1"
	if err := os.MkdirAll(f.n.Temp, 0700); err != nil {
		t.Fatal(err)
	}
	f.write(t, filepath.Join(f.config, "settings.json"), `{}`)
	f.write(t, filepath.Join(f.config, "skills", "own", "SKILL.md"), "---\nname: Friendly\ndescription: true\nargument-hint: [one, two]\narguments: first second\nallowed-tools: [Read, 'Bash(echo *)']\nmodel: TeamModel[1m]\nuser-invocable: true\n---\nRead $first and $second.")
	f.write(t, filepath.Join(f.home, ".agents", "skills", "shared", "SKILL.md"), "---\ndescription: Shared user\n---\nUser shared.")
	f.write(t, filepath.Join(f.cwd, ".claude", "skills", "shared", "SKILL.md"), "---\ndescription: Project Claude wins over user agents\n---\nProject shared.")
	f.write(t, filepath.Join(f.cwd, ".claude", "skills", "conditional", "SKILL.md"), "---\npaths: ['src/**', '!src/excluded/**']\n---\nConditional.")
	f.write(t, filepath.Join(f.cwd, ".claude", "skills", "all", "SKILL.md"), "---\npaths: '**'\ndisable-model-invocation: true\n---\nAll paths.")
	f.write(t, filepath.Join(f.config, "commands", "legacy.md"), "---\nname: ignored legacy display\n---\nLegacy instructions.")
	f.write(t, filepath.Join(f.config, "commands", "sub", "SKILL.md"), "---\ndescription: Legacy directory\n---\nDirectory instructions.")
	f.write(t, filepath.Join(f.config, "commands", "sub", "skip.md"), "Leaf ignores sibling.")
	f.write(t, filepath.Join(f.cwd, ".claude", "skills", "invalid-utf8", "SKILL.md"), "Replace invalid: \xff.")
	settings, err := NewNativeProcessSettings(f.n, runtime.GOOS, DefaultState())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = settings.Close(context.Background()) })
	service, err := settings.SkillCommands(NativeSkillOptions{})
	if err != nil {
		t.Fatal(err)
	}
	metadata := func(commands []skillcommands.Definition) []protocol.JSONObject {
		out := []protocol.JSONObject{}
		for _, command := range commands {
			out = append(out, command.Metadata())
		}
		return out
	}
	operations := []map[string]any{{"kind": "local"}, {"kind": "activate", "paths": []string{"../outside", "src/excluded/file.go"}}, {"kind": "activate", "paths": []string{filepath.Join(f.cwd, "src", "main.go")}}, {"kind": "local"}, {"kind": "clear"}, {"kind": "local"}, {"kind": "activate", "paths": []string{"src/main.go"}}}
	observations := []map[string]any{}
	notifications := 0
	unsubscribe := service.OnLoaded(func() { notifications++ })
	defer unsubscribe()
	for _, op := range operations {
		switch op["kind"] {
		case "local":
			commands, err := service.Local(context.Background(), f.cwd)
			if err != nil {
				t.Fatal(err)
			}
			observations = append(observations, map[string]any{"commands": metadata(commands), "pending": service.ConditionalCount()})
		case "activate":
			names, err := service.ActivateConditional(op["paths"].([]string), f.cwd)
			if err != nil {
				t.Fatal(err)
			}
			observations = append(observations, map[string]any{"names": names, "dynamic": metadata(service.Dynamic()), "pending": service.ConditionalCount()})
		case "clear":
			service.Clear()
			observations = append(observations, map[string]any{"dynamic": metadata(service.Dynamic()), "pending": service.ConditionalCount()})
		}
	}
	if notifications != 2 {
		t.Fatalf("activation notifications=%d want=2", notifications)
	}
	input, _ := json.Marshal(map[string]any{"cwd": f.cwd, "operations": operations})
	_, file, _, _ := runtime.Caller(0)
	fixture := filepath.Join(filepath.Dir(file), "../../../scripts/quality-gate/go-parity/native-skill-commands.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir, command.Stdin = f.home, bytes.NewReader(input)
	command.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_NATIVE_SKILL_COMMAND_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_USE_NATIVE_FILE_SEARCH=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		t.Fatalf("actual Source Skill producer: %v %s", err, stderr.Bytes())
	}
	actual, _ := json.Marshal(map[string]any{"observations": observations})
	var got, source any
	if json.Unmarshal(actual, &got) != nil || json.Unmarshal(out, &source) != nil {
		t.Fatal("invalid observations", string(actual), string(out))
	}
	if !reflect.DeepEqual(got, source) {
		t.Fatalf("actual native local/conditional metadata and order differ:\nGo %s\nSource %s", actual, out)
	}
}
