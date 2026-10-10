package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type internalReadRow struct {
	Path        string         `json:"path"`
	Permissions map[string]any `json:"permissions,omitempty"`
}

func TestNativeInternalReadableCompletePathsAndOriginalCanUseToolMessages(t *testing.T) {
	t.Parallel()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun unavailable")
	}
	for _, profile := range []string{"default", "remote", "cowork-override", "user-setting", "user-empty", "user-relative", "user-root", "project-ignored", "policy-wins", "local-wins", "cowork-wins", "scratch-ant"} {
		t.Run(profile, func(t *testing.T) {
			f := newNativePluginFixture(t)
			n := f.n
			old := n.CWD
			current := filepath.Join(n.Home, "other-current")
			_ = os.MkdirAll(current, 0700)
			settings := `{}`
			switch profile {
			case "remote":
				n.Environment["CLAUDE_CODE_REMOTE_MEMORY_DIR"] = filepath.Join(n.Home, "remote")
			case "cowork-override":
				n.Environment["CLAUDE_COWORK_MEMORY_PATH_OVERRIDE"] = filepath.Join(n.Home, "cowork-memory")
			case "user-setting":
				settings = `{"autoMemoryDirectory":"~/trusted-memory"}`
			case "user-empty":
				settings = `{"autoMemoryDirectory":""}`
			case "user-relative":
				settings = `{"autoMemoryDirectory":"../unsafe"}`
			case "user-root":
				settings = `{"autoMemoryDirectory":"/"}`
			case "project-ignored":
				f.write(t, filepath.Join(n.CWD, ".claude", "settings.json"), string(rawMain(map[string]string{"autoMemoryDirectory": filepath.Join(n.Home, "untrusted-project-memory")})))
			case "policy-wins":
				settings = `{"autoMemoryDirectory":"~/user-memory"}`
				f.write(t, filepath.Join(n.ManagedRoot, "managed-settings.json"), string(rawMain(map[string]string{"autoMemoryDirectory": filepath.Join(n.Home, "policy-memory")})))
			case "local-wins":
				settings = `{"autoMemoryDirectory":"~/user-memory"}`
				f.write(t, filepath.Join(n.CWD, ".claude", "settings.local.json"), string(rawMain(map[string]string{"autoMemoryDirectory": filepath.Join(n.Home, "local-memory")})))
			case "cowork-wins":
				n.Environment["CLAUDE_COWORK_MEMORY_PATH_OVERRIDE"] = filepath.Join(n.Home, "cowork-priority")
				settings = `{"autoMemoryDirectory":"~/user-memory"}`
				f.write(t, filepath.Join(n.ManagedRoot, "managed-settings.json"), string(rawMain(map[string]string{"autoMemoryDirectory": filepath.Join(n.Home, "policy-memory")})))
			case "scratch-ant":
			}
			n.Environment["ANTHROPIC_API_KEY"] = "fake-internal-source-576"
			n.Environment["CLAUDE_CODE_TMPDIR"] = n.Temp
			f.write(t, filepath.Join(f.config, "settings.json"), settings)
			m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{Model: "fixture-model", MaxTurns: 100400, PermissionMode: "default", AllowDangerouslySkipPermissions: true, Sources: DefaultState()}, Native: n, PID: os.Getpid(), Version: "fixture", Platform: runtime.GOOS, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
			if e != nil {
				t.Fatal(e)
			}
			if e = m.StartSession(context.Background()); e != nil {
				t.Fatal(e)
			}
			if profile == "scratch-ant" {
				n.Environment["USER_TYPE"] = "ant"
				n.Environment["CLAUDE_INTERNAL_FC_OVERRIDES"] = `{"tengu_scratch":true}`
			}
			t.Cleanup(func() {
				if e := m.ShutdownSession(context.Background()); e != nil {
					t.Error(e)
				}
			})
			sid := m.Controller.SessionID()
			config := n.Environment["CLAUDE_CONFIG_DIR"]
			var slug, bundled, temp, auto string
			if e := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				if e := m.state.SetCWD(scope, current); e != nil {
					return e
				}
				var e error
				slug, e = m.Plans().Slug(scope)
				if e != nil {
					return e
				}
				bundled, e = m.nativeBundledSkillsRoot(scope)
				if e != nil {
					return e
				}
				temp, e = m.nativeClaudeTempRoot(scope)
				if e != nil {
					return e
				}
				auto, e = m.memoryPaths.Directory()
				return e
			}); e != nil {
				t.Fatal(e)
			}
			currentProject := filepath.Join(config, "projects", projectpath.Sanitize(current))
			oldProject := filepath.Join(config, "projects", projectpath.Sanitize(old))
			tempProject := filepath.Join(temp, projectpath.Sanitize(old))
			base := config
			if v := n.Environment["CLAUDE_CODE_REMOTE_MEMORY_DIR"]; v != "" {
				base = v
			}
			paths := []string{
				filepath.Join(config, "tasks"), filepath.Join(config, "tasks", sid, "1.json"), filepath.Join(config, "tasks", sid, "..", "second", "2.json"), filepath.Join(config, "tasks-evil", "x"), filepath.Join(config, "tasks", "..", "settings.json"), filepath.Join(config, "TASKS", "x"), filepath.Join(config, "tasks", "GIT~1", "x"), filepath.Join(config, "tasks", "file."), filepath.Join(config, "tasks", "...", "x"), filepath.Join(config, "tasks", "file.CON"), filepath.Join(config, "tasks", "file:colon"),
				filepath.Join(config, "teams"), filepath.Join(config, "teams", "team", "config.json"), filepath.Join(config, "teams-evil", "x"),
				currentProject, filepath.Join(currentProject, sid, "session-memory", "summary.md"), filepath.Join(currentProject, "different-sid", "session-memory", "summary.md"), filepath.Join(currentProject+"-evil", "x"),
				filepath.Join(config, "plans", slug+".md"), filepath.Join(config, "plans", slug+"-agent-other.md"), filepath.Join(config, "plans", slug+"-extra.md"), filepath.Join(config, "plans", slug+".MD"), filepath.Join(config, "plans", "other.md"),
				filepath.Join(oldProject, sid, "tool-results"), filepath.Join(oldProject, sid, "tool-results", "result.txt"), filepath.Join(oldProject, sid, "tool-results-evil", "x"), filepath.Join(oldProject, "other-sid", "tool-results", "x"),
				tempProject, filepath.Join(tempProject, sid, "scratchpad"), filepath.Join(tempProject, "other-sid", "tasks", "id.output"), filepath.Join(tempProject+"-evil", "x"),
				filepath.Join(base, "agent-memory"), filepath.Join(base, "agent-memory", "agent", "MEMORY.md"), filepath.Join(base, "agent-memory-evil", "x"), filepath.Join(current, ".claude", "agent-memory", "agent", "MEMORY.md"), filepath.Join(current, ".claude", "agent-memory-local", "agent", "MEMORY.md"), filepath.Join(base, "projects", "other-project", "agent-memory-local", "agent", "MEMORY.md"),
				strings.TrimSuffix(auto, string(filepath.Separator)), filepath.Join(auto, "MEMORY.md"), filepath.Join(auto, "..", "outside.md"), filepath.Join(n.Home, "outside.txt"), filepath.Join(config, "settings.json"),
				"%BUNDLED%/skill/ref.md", "%BUNDLED%", "%BUNDLED%-evil/ref.md",
			}
			rows := []internalReadRow{}
			for _, path := range paths {
				rows = append(rows, internalReadRow{Path: path})
			}
			task := filepath.Join(config, "tasks", sid, "1.json")
			absRule := func(path string) string { return "Read(//" + strings.TrimPrefix(filepath.ToSlash(path), "/") + ")" }
			for _, mode := range []string{"default", "bypassPermissions"} {
				for _, kind := range []string{"alwaysDenyRules", "alwaysAskRules"} {
					rows = append(rows, internalReadRow{Path: task, Permissions: map[string]any{"mode": mode, kind: map[string][]string{"userSettings": {absRule(filepath.Join(config, "tasks", "**"))}}}})
				}
			}
			rows = append(rows, internalReadRow{Path: task, Permissions: map[string]any{"alwaysAskRules": map[string][]string{"userSettings": {"Read"}}}})
			// Canonical targets participate in deny/ask checks before lexical internal
			// grants. Source deliberately retains its own internal symlink behavior.
			outside := filepath.Join(n.Home, "outside-link-target")
			_ = os.MkdirAll(outside, 0700)
			_ = os.WriteFile(filepath.Join(outside, "data.txt"), []byte("private"), 0600)
			link := filepath.Join(config, "tasks", "link")
			_ = os.MkdirAll(filepath.Dir(link), 0700)
			if e := os.Symlink(outside, link); e != nil {
				t.Fatal(e)
			}
			rows = append(rows, internalReadRow{Path: filepath.Join(link, "data.txt")})
			for _, kind := range []string{"alwaysDenyRules", "alwaysAskRules"} {
				rows = append(rows, internalReadRow{Path: filepath.Join(link, "data.txt"), Permissions: map[string]any{kind: map[string][]string{"userSettings": {absRule(filepath.Join(outside, "**"))}}}})
			}
			payload, _ := json.Marshal(map[string]any{"cwd": current, "originalCwd": old, "projectRoot": n.CWD, "sid": sid, "planSlug": slug, "managedRoot": n.ManagedRoot, "rows": rows})
			fixture, _ := filepath.Abs("../../testdata/native-internal-read-permission.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = n.Home
			cmd.Stdin = bytes.NewReader(payload)
			env := map[string]string{"HOME": n.Home, "USERPROFILE": n.Home, "CLAUDE_CONFIG_DIR": config, "NODE_ENV": "production", "BUN_OPTIONS": "--no-env-file", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH": "1", "CLAUDE_CODE_TMPDIR": n.Temp, "CC_HAHA_INTERNAL_READ_FIXTURE": "1", "CLAUDE_CODE_LOCAL_VERSION": "fixture", "USER_TYPE": "ant", "CLAUDE_CODE_MANAGED_SETTINGS_PATH": n.ManagedRoot, "PATH": "/usr/bin:/bin"}
			for _, key := range []string{"CLAUDE_CODE_REMOTE_MEMORY_DIR", "CLAUDE_COWORK_MEMORY_PATH_OVERRIDE", "USER_TYPE", "CLAUDE_INTERNAL_FC_OVERRIDES"} {
				if v := n.Environment[key]; v != "" {
					env[key] = v
				}
			}
			for k, v := range env {
				cmd.Env = append(cmd.Env, k+"="+v)
			}
			out, e := cmd.CombinedOutput()
			if e != nil {
				t.Fatalf("actual Source %v %.3000s", e, out)
			}
			var source struct {
				Rows []struct {
					Path     string
					Internal struct{ Behavior, Reason string }
					Decision struct {
						Behavior string
						Message  *string
					}
				}
			}
			if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != len(rows) {
				t.Fatalf("Source shape %v %.3000s", e, out)
			}
			if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				for i, row := range rows {
					path := strings.ReplaceAll(row.Path, "%BUNDLED%", bundled)
					got, reason, e := m.nativeInternalReadable(scope, path)
					if e != nil {
						return e
					}
					expected := source.Rows[i]
					if got != (expected.Internal.Behavior == "allow") || reason != expected.Internal.Reason {
						t.Errorf("row%d %s internal Go %v/%s Source %s/%s", i, path, got, reason, expected.Internal.Behavior, expected.Internal.Reason)
					}
					if e := m.Host.SetNativePermissionMode(scope, "default"); e != nil {
						return e
					}
					updates := []protocol.JSONObject{}
					for kind, behavior := range map[string]string{"alwaysDenyRules": "deny", "alwaysAskRules": "ask", "alwaysAllowRules": "allow"} {
						values := []protocol.JSONObject{}
						if sources, ok := row.Permissions[kind].(map[string][]string); ok {
							for _, rules := range sources {
								for _, rule := range rules {
									v, e := sdkpermissions.ParsePermissionRuleValue(quoteMain(rule))
									if e != nil {
										return e
									}
									values = append(values, v)
								}
							}
						}
						updates = append(updates, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("replaceRules")}, protocol.JSONField{Name: "behavior", Value: quoteMain(behavior)}, protocol.JSONField{Name: "destination", Value: quoteMain("session")}, protocol.JSONField{Name: "rules", Value: rawMain(values)}))
					}
					if e := m.Controller.ApplySDKPermissionUpdates(scope, updates, nil); e != nil {
						return e
					}
					if mode, ok := row.Permissions["mode"].(string); ok {
						if e := m.Host.SetNativePermissionMode(scope, mode); e != nil {
							return e
						}
					}
					in := protocol.NewJSONObject(protocol.JSONField{Name: "file_path", Value: quoteMain(path)})
					decision, e := m.nativeTools.PermissionDecision(scope, "Read", &in)
					if e != nil {
						return e
					}
					message := ""
					if decision.Message != nil {
						_ = json.Unmarshal(decision.Message, &message)
					}
					expMessage := ""
					if expected.Decision.Message != nil {
						expMessage = *expected.Decision.Message
					}
					if decision.Behavior != expected.Decision.Behavior || message != strings.ReplaceAll(expMessage, expected.Path, path) {
						t.Errorf("row%d %s decision Go %s/%q Source %s/%q", i, path, decision.Behavior, message, expected.Decision.Behavior, expMessage)
					}
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestInternalReadOwnerAndProcessNonceBindings(t *testing.T) {
	m := taskMainFixture(t)
	other := cooperative.New()
	defer other.Close()
	if e := other.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, _, e := m.nativeInternalReadable(scope, filepath.Join(m.options.Native.Home, ".claude", "tasks", "1.json"))
		if !errors.Is(e, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign read binding %v", e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	var root string
	if e := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var e error
		root, e = m.nativeBundledSkillsRoot(scope)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	clone := m.options.Native.agentScopedBoundary(m.options.Native.CWD)
	if clone.internalReadState() != m.options.Native.internalReadState() {
		t.Fatal("agent lost actual process memo authority")
	}
	if len(filepath.Base(root)) != 32 {
		t.Fatal("actual nonce missing", root)
	}
}
