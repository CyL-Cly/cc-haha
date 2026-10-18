package queryhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Adjunct to the real public MainPrint/Source physical tests: preserve the
// Source root namespace and exact callback matcher behavior across all kinds.
func TestNativeFrontmatterRootNamespaceOnceCallbackAndOwnerAdmission(t *testing.T) {
	checkActualSourceFrontmatterGroupCallbacks(t)
	owner := cooperative.New()
	defer owner.Close()
	factory, err := messages.NewFactory(func() string { return "actual-hook-id" }, func() time.Time { return time.Unix(0, 0) })
	if err != nil {
		t.Fatal(err)
	}
	service, _, _ := newTestService(t, nil)
	service.options.Commands = &CommandBindings{Owner: owner, Factory: factory, Timestamp: func() string { return "actual" }, NowMillis: func() float64 { return 0 }, Input: func(_ context.Context, call Context) (protocol.JSONObject, error) {
		return protocol.NewJSONObject(protocol.JSONField{Name: "session_id", Value: commandJSON("hook-main")}, protocol.JSONField{Name: "hook_event_name", Value: commandJSON(call.HookEvent)}), nil
	}}
	parse := func(raw string) protocol.JSONObject {
		t.Helper()
		var value protocol.JSONObject
		if err := value.UnmarshalJSON([]byte(raw)); err != nil {
			t.Fatal(err)
		}
		return value
	}
	once := parse(`{"PreToolUse":[{"matcher":"Write","hooks":[{"type":"command","command":"identical","once":true,"future":{"keep":true}}]}]}`)
	plain := parse(`{"PreToolUse":[{"matcher":"Write","hooks":[{"type":"command","command":"identical","timeout":2,"future":{"keep":true}}]}]}`)
	var retained *cooperative.Scope
	if err := service.RegisterNativeSkillHooks(nil, "hook-main", once, "/private/root-a"); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("nil Source Owner scope admitted", err)
	}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		retained = scope
		if err := service.RegisterNativeSkillHooks(scope, "hook-main", once, "/private/root-a"); err != nil {
			return err
		}
		if err := service.RegisterNativeSkillHooks(scope, "hook-main", plain, "/private/root-b"); err != nil {
			return err
		}
		rows, err := service.configuredNativeMatchedCommands("PreToolUse", "hook-main", true)
		if err != nil {
			return err
		}
		if len(rows) != 2 || rows[0].skillRoot != "/private/root-a" || rows[1].skillRoot != "/private/root-b" {
			return fmt.Errorf("cross-root declaration namespace lost: %+v", rows)
		}
		future, ok := rows[0].hook.Object("future")
		if !ok || string(future.Get("keep")) != "true" {
			return fmt.Errorf("unknown Source hook declaration field lost")
		}
		keyA, err := rows[0].key()
		if err != nil {
			return err
		}
		keyB, err := rows[1].key()
		if err != nil {
			return err
		}
		if keyA == keyB {
			return fmt.Errorf("same command deduplicated across roots")
		}
		service.nativeSessionHookSucceeded(scope, "PreToolUse", "Read", rows[0].hook)
		if service.NativeAgentHookCount("hook-main") != 2 {
			return fmt.Errorf("success callback ignored exact Source matcher")
		}
		// Source uses first logical equality independent of timeout/root, then its
		// callback removes every equal declaration, not only the selected root.
		service.nativeSessionHookSucceeded(scope, "PreToolUse", "Write", rows[1].hook)
		if service.NativeAgentHookCount("hook-main") != 0 {
			return fmt.Errorf("one-shot logical removal did not include both roots")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterNativeSkillHooks(retained, "hook-main", once, "/private/root-a"); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("retained Source Owner scope admitted", err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := service.RegisterNativeSkillHooks(scope, "hook-main", once, "/private/root-a"); !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign Source Owner scope admitted: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Existing main declarations before Skill remain the first equal hook:
	// they have no Skill once callback. Reverse admission removes both groups.
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		service.ClearNativeAgentHooks("hook-main")
		if err := service.RegisterNativeMainHooks("hook-main", plain); err != nil {
			return err
		}
		if err := service.RegisterNativeSkillHooks(scope, "hook-main", once, "/private/root-a"); err != nil {
			return err
		}
		rows, err := service.configuredNativeMatchedCommands("PreToolUse", "hook-main", true)
		if err != nil {
			return err
		}
		if len(rows) != 2 || rows[0].skillRoot != "" || rows[1].skillRoot != "/private/root-a" {
			return fmt.Errorf("existing main group moved after Skill")
		}
		service.nativeSessionHookSucceeded(scope, "PreToolUse", "Write", rows[1].hook)
		if service.NativeAgentHookCount("hook-main") != 2 {
			return fmt.Errorf("later Skill once shadowed first legacy main declaration")
		}
		service.ClearNativeAgentHooks("hook-main")
		if err := service.RegisterNativeSkillHooks(scope, "hook-main", once, "/private/root-a"); err != nil {
			return err
		}
		if err := service.RegisterNativeMainHooks("hook-main", plain); err != nil {
			return err
		}
		rows, err = service.configuredNativeMatchedCommands("PreToolUse", "hook-main", true)
		if err != nil {
			return err
		}
		if len(rows) != 2 || rows[0].skillRoot != "/private/root-a" || rows[1].skillRoot != "" {
			return fmt.Errorf("later legacy main declaration moved ahead of Skill")
		}
		service.nativeSessionHookSucceeded(scope, "PreToolUse", "Write", rows[1].hook)
		if service.NativeAgentHookCount("hook-main") != 0 {
			return fmt.Errorf("first Skill once did not remove logically equal later legacy group")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service.options.Prompts = &PromptBindings{Owner: owner, Query: func(context.Context, *cooperative.Scope, PromptModelInput) (protocol.JSONObject, error) {
		return protocol.NewJSONObject(), nil
	}}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		service.ClearNativeAgentHooks("hook-main")
		prompt := parse(`{"type":"prompt","prompt":"mixed prompt"}`)
		declaration := parse(`{"Stop":[{"hooks":[{"type":"prompt","prompt":"mixed prompt","once":true}]}]}`)
		called := 0
		if err := service.RegisterNativePromptHook(scope, "hook-main", "Stop", "", prompt, func(current *cooperative.Scope) error {
			if !current.BelongsTo(owner) {
				return fmt.Errorf("callback lost actual scope")
			}
			called++
			return fmt.Errorf("Source catches callback exception")
		}); err != nil {
			return err
		}
		if err := service.RegisterNativeSkillHooks(scope, "hook-main", declaration, "/private/root-a"); err != nil {
			return err
		}
		service.nativeSessionHookSucceeded(scope, "Stop", "", prompt)
		if called != 1 || service.NativeAgentHookCount("hook-main") != 2 {
			return fmt.Errorf("first legacy goal callback did not retain later Skill once: %d", called)
		}
		service.ClearNativeAgentHooks("hook-main")
		called = 0
		if err := service.RegisterNativeSkillHooks(scope, "hook-main", declaration, "/private/root-a"); err != nil {
			return err
		}
		if err := service.RegisterNativePromptHook(scope, "hook-main", "Stop", "", prompt, func(*cooperative.Scope) error { called++; return nil }); err != nil {
			return err
		}
		service.nativeSessionHookSucceeded(scope, "Stop", "", prompt)
		if called != 0 || service.NativeAgentHookCount("hook-main") != 0 {
			return fmt.Errorf("later goal callback replaced first Skill once callback: %d", called)
		}
		if service.NativeCommandStore().promptSuccess["hook-main"]["Stop\x00"+promptIdentity(prompt)] != nil {
			return fmt.Errorf("one-shot removal retained a stale legacy goal callback")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	agent := parse(`{"SubagentStop":[{"hooks":[{"type":"http","url":"http://127.0.0.1/existing"}]}],"PreToolUse":[{"matcher":"Write","hooks":[{"type":"command","command":"agent","once":true}]}],"Stop":[{"hooks":[{"type":"prompt","prompt":"stop-first"}]}]}`)
	if err := service.RegisterNativeAgentHooks("child-agent", agent); err != nil {
		t.Fatal(err)
	}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		stop, err := service.configuredNativeMatchedCommands("SubagentStop", "child-agent", true)
		if err != nil {
			return err
		}
		if len(stop) != 2 || stop[0].hook.String("type") != "prompt" || stop[1].hook.String("type") != "http" {
			return fmt.Errorf("Agent Stop conversion did not preserve HOOK_EVENTS order: %+v", stop)
		}
		rows, err := service.configuredNativeMatchedCommands("PreToolUse", "child-agent", true)
		if err != nil {
			return err
		}
		if len(rows) != 1 || rows[0].skillRoot != "" {
			return fmt.Errorf("ordinary Agent frontmatter root/selection lost")
		}
		service.nativeSessionHookSucceeded(scope, "PreToolUse", "Write", rows[0].hook)
		if service.NativeAgentHookCount("child-agent") != 3 {
			return fmt.Errorf("Agent once gained Skill callback behavior")
		}
		service.ClearNativeAgentHooks("child-agent")
		if service.NativeAgentHookCount("child-agent") != 0 {
			return fmt.Errorf("Agent finally left frontmatter declarations")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func checkActualSourceFrontmatterGroupCallbacks(t *testing.T) {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual pinned Source Bun required")
	}
	parse := func(text string) protocol.JSONObject {
		t.Helper()
		var row protocol.JSONObject
		if err := row.UnmarshalJSON([]byte(text)); err != nil {
			t.Fatal(err)
		}
		return row
	}
	once := parse(`{"PreToolUse":[{"matcher":"Write","hooks":[{"type":"command","command":"identical","once":true,"future":{"keep":true}}]}]}`)
	plain := parse(`{"PreToolUse":[{"matcher":"Write","hooks":[{"type":"command","command":"identical","timeout":2,"future":{"keep":true}}]}]}`)
	hook := parse(`{"type":"command","command":"identical"}`)
	skill := func(root string, hooks protocol.JSONObject) map[string]any {
		return map[string]any{"kind": "skill", "root": root, "hooks": hooks}
	}
	main := map[string]any{"kind": "main", "hooks": plain}
	rows := []map[string]any{
		{"name": "main-before-skill", "event": "PreToolUse", "matchQuery": "Write", "completedHook": hook, "registrations": []any{main, skill("/private/root-a", once)}},
		{"name": "skill-before-main", "event": "PreToolUse", "matchQuery": "Write", "completedHook": hook, "registrations": []any{skill("/private/root-a", once), main}},
		{"name": "two-roots", "event": "PreToolUse", "matchQuery": "Write", "completedHook": hook, "registrations": []any{skill("/private/root-a", once), skill("/private/root-b", plain)}},
		{"name": "exact-matcher", "event": "PreToolUse", "matchQuery": "Read", "completedHook": hook, "registrations": []any{skill("/private/root-a", once)}},
	}
	prompt := parse(`{"type":"prompt","prompt":"mixed prompt"}`)
	promptHooks := parse(`{"Stop":[{"hooks":[{"type":"prompt","prompt":"mixed prompt","once":true}]}]}`)
	goal := map[string]any{"kind": "goal-callback", "matcher": "", "hook": prompt}
	rows = append(rows, map[string]any{"name": "goal-before-skill", "event": "Stop", "matchQuery": "", "completedHook": prompt, "registrations": []any{goal, skill("/private/root-a", promptHooks)}}, map[string]any{"name": "skill-before-goal", "event": "Stop", "matchQuery": "", "completedHook": prompt, "registrations": []any{skill("/private/root-a", promptHooks), goal}})
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sourceFixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-frontmatter-groups.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", sourceFixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_FRONTMATTER_GROUP_SOURCE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(commandJSON(map[string]any{"homeDir": home, "rows": rows}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source group callback fixture failed: %v %.8000s", err, output)
	}
	var source struct {
		Rows []struct {
			Name                            string
			BeforeCount, AfterCount, Called int
			FirstCallbackPresent            bool
		}
		Capabilities struct{ Blocked []string }
	}
	if err := json.Unmarshal(output, &source); err != nil || len(source.Rows) != 6 || len(source.Capabilities.Blocked) != 0 {
		t.Fatal("Source callback group receipt incomplete", err, string(output))
	}
	wants := []struct {
		Name                  string
		Before, After, Called int
		Callback              bool
	}{{"main-before-skill", 2, 2, 0, false}, {"skill-before-main", 2, 0, 0, true}, {"two-roots", 2, 0, 0, true}, {"exact-matcher", 1, 1, 0, false}, {"goal-before-skill", 2, 2, 1, true}, {"skill-before-goal", 2, 0, 0, true}}
	for i, row := range source.Rows {
		want := wants[i]
		if row.Name != want.Name || row.BeforeCount != want.Before || row.AfterCount != want.After || row.Called != want.Called || row.FirstCallbackPresent != want.Callback {
			t.Fatal("Source exact callback/group behavior differs", i, row, want, string(output))
		}
	}
	t.Logf("FRONTMATTER_GROUP_SOURCE %s", output)
}
