package skillcommands

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
)

func TestEmbeddedShellMatcherMatchesSourceProductionParser(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	texts := []string{"plain", "!`printf one`", "not!`printf skipped`", "email@test !`printf two`", "\u00a0!`printf three`", "\ufeff!`printf four`", "!`   `", "```!\n printf block \n``` then !`printf inline`", "```!\ufeffprintf unicode```", "```!\n!`printf nested`\n```", "!`first` !`first`", "`foo`!`bar` $!`not`", "```!\nfirst```\n```! second```\n!`third`"}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-skill-tool.fixture.ts")
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_SKILL_TOOL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "shellMatches": texts})
	cmd.Stdin = bytes.NewReader(data)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.3000s", e, output)
	}
	var source struct {
		ShellMatches []struct {
			Commands []string
			Failure  string
		}
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(output, &source); e != nil || len(source.ShellMatches) != len(texts) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source %v %.3000s", e, output)
	}
	for i, text := range texts {
		got, e := PromptShellMatches(text)
		if e != nil {
			t.Fatal(e)
		}
		commands := []string{}
		for _, match := range got {
			commands = append(commands, match.Command)
		}
		if !reflect.DeepEqual(commands, source.ShellMatches[i].Commands) {
			t.Errorf("case%d commands Go%v Source%v", i, commands, source.ShellMatches[i].Commands)
		}
		if len(got) > 0 {
			expected := `Shell command permission check failed for pattern "` + got[0].Pattern + `": parser observer denies before execution`
			if source.ShellMatches[i].Failure != expected {
				t.Errorf("case%d pattern Go%q Source%q", i, expected, source.ShellMatches[i].Failure)
			}
		}
	}
}
