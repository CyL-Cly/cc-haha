package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func permissionRestartMain(t *testing.T, n *NativeBoundary, allow bool, addDirs ...string) *MainPrint {
	t.Helper()
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{AdditionalDirectories: addDirs, ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual private permission restart", Tools: []string{"Read,Write,Skill"}, SessionID: uuidMain(), MaxTurns: 4, AllowDangerouslySkipPermissions: allow, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(m.Close)
	if e = m.StartSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	return m
}

func TestActualPermissionPersistenceRestartActivatesModesDirectoryToolsPromptMemoryAndSkills(t *testing.T) {
	for _, mode := range []string{"default", "acceptEdits", "plan", "bypassPermissions"} {
		t.Run(mode, func(t *testing.T) {
			n := nativeForTest(t)
			n.NativeProcess = true
			n.ManagedRoot = filepath.Join(n.Temp, "managed")
			_ = os.MkdirAll(n.ManagedRoot, 0700)
			n.Environment["PATH"] = "/usr/bin:/bin"
			n.Environment["CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD"] = "1"
			n.ResolvePolicy = func() (*protocol.JSONObject, error) { o := protocol.NewJSONObject(); return &o, nil }
			n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
				return nil, errors.New("private absent secure fixture")
			}
			_ = os.MkdirAll(n.Environment["CLAUDE_CONFIG_DIR"], 0700)
			_ = os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"future":{"preserved":true}}`), 0600)
			extra := filepath.Join(n.Home, "additional")
			skill := filepath.Join(extra, ".claude/skills/restarted-skill")
			_ = os.MkdirAll(skill, 0700)
			_ = os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: restarted-skill\ndescription: actual additional directory restart skill\n---\nUse this actual extra skill.\n"), 0600)
			_ = os.WriteFile(filepath.Join(extra, "CLAUDE.md"), []byte("ADDITIONAL_DIRECTORY_MEMORY_REAL"), 0600)
			file := filepath.Join(extra, "target.txt")
			_ = os.WriteFile(file, []byte("real read target"), 0600)
			first := permissionRestartMain(t, n, true)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			updates := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("setMode")}, protocol.JSONField{Name: "destination", Value: quoteMain("userSettings")}, protocol.JSONField{Name: "mode", Value: quoteMain(mode)})
			dirs := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("addDirectories")}, protocol.JSONField{Name: "destination", Value: quoteMain("userSettings")}, protocol.JSONField{Name: "directories", Value: quoteArray([]string{extra})})
			batch, _ := json.Marshal([]protocol.JSONObject{updates, dirs})
			if e := first.Owner.Run(ctx, func(scope *cooperative.Scope) error { return first.Host.ApplyNativePermissionUpdates(scope, batch) }); e != nil {
				t.Fatal(e)
			}
			if e := first.ShutdownSession(ctx); e != nil {
				t.Fatal(e)
			}
			second := permissionRestartMain(t, n, false)
			if e := second.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				actual, e := second.Host.NativePermissionContext(scope)
				if e != nil {
					return e
				}
				if actual.String("mode") != mode {
					t.Errorf("saved mode not active after restart: %s", actual.Get("mode"))
				}
				if !strings.Contains(string(actual.Get("additionalWorkingDirectories")), extra) || !strings.Contains(string(actual.Get("additionalWorkingDirectories")), `"source":"cliArg"`) {
					t.Errorf("saved directory/provenance missing%s", actual.Get("additionalWorkingDirectories"))
				}
				input := protocol.NewJSONObject(protocol.JSONField{Name: "file_path", Value: quoteMain(file)})
				behavior, e := second.nativeTools.Permission(scope, "Read", input)
				if e != nil {
					return e
				}
				if mode != "plan" && behavior != "allow" {
					t.Errorf("real additional Read permission %s", behavior)
				}
				behavior, e = second.nativeTools.Permission(scope, "Write", input)
				if e != nil {
					return e
				}
				expected := "ask"
				if mode == "acceptEdits" || mode == "bypassPermissions" {
					expected = "allow"
				}
				if mode != "plan" && behavior != expected {
					t.Errorf("actual mode's Write permission Go%s expected%s", behavior, expected)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			environment, e := second.prompt.Environment(ctx)
			if e != nil || !strings.Contains(string(environment), extra) {
				t.Fatalf("actual system prompt directory missing%s %v", environment, e)
			}
			memory, e := second.userContext.GetUserContext().Await(ctx)
			if e != nil {
				t.Fatal(e)
			}
			if strings.Contains(string(memory.Get("claudeMd")), "ADDITIONAL_DIRECTORY_MEMORY_REAL") {
				t.Fatal("saved permission directories changed Source CLI-only memory discovery")
			}
			if slices.Contains(second.Registry.Catalog().Skills, "restarted-skill") {
				t.Fatal("saved permission directories changed Source CLI-only skill discovery")
			}
			data, e := os.ReadFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"))
			if e != nil || !strings.Contains(string(data), `"future"`) {
				t.Fatalf("unknown setting lost%s %v", data, e)
			}
			if e := second.ShutdownSession(ctx); e != nil {
				t.Fatal(e)
			}
			// Source's separate CLI bootstrap add-dir list enables instruction
			// and Skill discovery; it is not synthesized from permission maps.
			third := permissionRestartMain(t, n, false, extra)
			memory, e = third.userContext.GetUserContext().Await(ctx)
			if e != nil {
				t.Fatal(e)
			}
			if !strings.Contains(string(memory.Get("claudeMd")), "ADDITIONAL_DIRECTORY_MEMORY_REAL") {
				t.Fatalf("actual CLI add-dir memory absent%s", memory.Get("claudeMd"))
			}
			if !slices.Contains(third.Registry.Catalog().Skills, "restarted-skill") {
				t.Fatalf("actual CLI add-dir skill absent%v", third.Registry.Catalog().Skills)
			}
			if e := third.ShutdownSession(ctx); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func quoteArray(values []string) json.RawMessage { b, _ := json.Marshal(values); return b }
