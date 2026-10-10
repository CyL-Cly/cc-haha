package clibootstrap

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

func TestNativeSkillShellUsesActualBashConcurrentIOAndTemporaryPermissions(t *testing.T) {
	f, _, options := computerUseMainFixture(t)
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	f.n.Environment["BASH_DEFAULT_TIMEOUT_MS"] = "3000"
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{ComputerUse: options, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "no model in actual embedded shell test", Tools: []string{"Skill,Bash,Read"}, PermissionMode: "default", MaxTurns: 1, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if e = m.StartSession(ctx); e != nil {
		t.Fatal(e)
	}
	first, second := filepath.Join(f.cwd, "first.txt"), filepath.Join(f.cwd, "second.txt")
	command := func(path, other, word string) string {
		return "printf start > '" + path + "'; n=0; while [ ! -f '" + other + "' ] && [ $n -lt 100 ]; do sleep 0.01; n=$((n+1)); done; [ -f '" + other + "' ] || exit 2; printf '%s' '" + word + "'"
	}
	text := "a !`" + command(first, second, "$$ $& literal") + "` b !`" + command(second, first, "second") + "` c"
	if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		output, e := m.nativeSkillShell(ctx, scope, skillcommands.ShellRequest{Text: text, SlashCommand: "/fixture", AllowedTools: []string{"Bash"}})
		if e != nil {
			return e
		}
		if output != "a $$ $& literal b second c" {
			return fmt.Errorf("concurrent actual shell/literal replacement changed: %q", output)
		}
		permission, e := m.nativeToolPermissionContext(scope)
		if e != nil {
			return e
		}
		var rules protocol.JSONObject
		_ = rules.UnmarshalJSON(permission.Get("alwaysAllowRules"))
		if rules.Get("command") != nil {
			return fmt.Errorf("prompt-only Bash grant leaked: %s", rawMain(permission))
		}
		_, e = m.nativeSkillShell(ctx, scope, skillcommands.ShellRequest{Text: "!`printf forbidden > '" + filepath.Join(f.cwd, "forbidden.txt") + "'`", SlashCommand: "/fixture", AllowedTools: []string{}})
		if e == nil || !strings.Contains(e.Error(), "Shell command permission check failed for pattern") {
			return fmt.Errorf("ungranted actual Bash allowed: %v", e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{first, second} {
		data, e := os.ReadFile(path)
		if e != nil || string(data) != "start" {
			t.Fatal("real shell artifact missing", path, string(data), e)
		}
	}
	if _, e := os.Stat(filepath.Join(f.cwd, "forbidden.txt")); !os.IsNotExist(e) {
		t.Fatal("denied prompt shell caused real IO", e)
	}
}
