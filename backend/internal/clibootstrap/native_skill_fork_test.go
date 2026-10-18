package clibootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func forkSkillMain(t *testing.T, url string, configure ...func(*MainPrintOptions)) *MainPrint {
	t.Helper()
	return forkSkillMainWithParentSkillGrant(t, url, false, configure...)
}

func forkSkillMainWithParentSkillGrant(t *testing.T, url string, parentSkillAllowed bool, configure ...func(*MainPrintOptions)) *MainPrint {
	t.Helper()
	m := backgroundAgentMainWithOptions(t, url, func(n *NativeBoundary) {
		n.Environment["CLAUDE_CODE_DISABLE_CLAUDE_MDS"] = "1"
		// Source uses its original native Markdown discovery too. No rg executable
		// is provided by this private /usr/bin:/bin fixture.
		n.Environment["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "1"
		write := func(path, text string) {
			t.Helper()
			if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(path, []byte(text), 0600); e != nil {
				t.Fatal(e)
			}
		}
		write(filepath.Join(n.CWD, ".claude", "skills", "forkprobe", "SKILL.md"), "---\nname: forkprobe\ndescription: Forked native product fixture\ncontext: fork\nagent: fork-worker\nallowed-tools: Write\neffort: low\n---\nFORK_CHILD_INPUT $ARGUMENTS\n")
		write(filepath.Join(n.CWD, ".claude", "skills", "forkask", "SKILL.md"), "---\nname: forkask\ndescription: Fork child asks parent UI\ncontext: fork\nagent: fork-worker\n---\nFORK_CHILD_INPUT $ARGUMENTS\n")
		write(filepath.Join(n.CWD, ".claude", "agents", "fork-worker.md"), "---\nname: fork-worker\ndescription: Fork child\ntools: Read,Write\nmodel: inherit\n---\nFORK_PRIVATE_CHILD_SYSTEM\n")
		privateSettings := `{"enabledPlugins":{"sample@mkt":false},"permissions":{"deny":["Read(secret.txt)"]}}`
		if parentSkillAllowed {
			privateSettings = `{"enabledPlugins":{"sample@mkt":false},"permissions":{"allow":["Skill(forkprobe)"],"deny":["Read(secret.txt)"]}}`
		}
		write(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), privateSettings)
		checkActualSourceForkSkillContract(t, n, parentSkillAllowed)
	}, func(o *MainPrintOptions) {
		o.Arguments.Tools = []string{"Skill,Write,Read"}
		o.Arguments.PermissionMode = "default"
		for _, apply := range configure {
			apply(o)
		}
	})
	definitions, err := m.AgentDefinitions(context.Background())
	if err != nil {
		t.Fatal("actual Go agent discovery before fork", err)
	}
	found := false
	for _, definition := range definitions.Active {
		if definition.Name == "fork-worker" && strings.Contains(definition.SystemPrompt, "FORK_PRIVATE_CHILD_SYSTEM") {
			found = true
		}
	}
	if !found {
		t.Fatalf("actual Go discovery missed the physical fork-worker before fork: failed=%#v", definitions.FailedFiles)
	}
	return m
}

// This top compiles against the old implementation too. Its RED must be the
// actual capability/absence of a child, never a missing new helper or import.
func TestActualForkedSkillPublicSlashAndToolUseOwnChildWriteWithoutParentHistoryLeak(t *testing.T) {
	for _, entry := range []string{"slash", "tool"} {
		t.Run(entry, func(t *testing.T) {
			var parent, child, progress, routeNoise atomic.Int32
			var slashResult atomic.Bool
			var artifact string
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.WriteHeader(200)
					return
				}
				body, _ := io.ReadAll(r.Body)
				if strings.Contains(string(body), "FORK_PRIVATE_CHILD_SYSTEM") {
					step := child.Add(1)
					if !strings.Contains(string(body), "FORK_CHILD_INPUT ARG_TOKEN") {
						t.Error("original expanded skill prompt missing", string(body))
					}
					if strings.Contains(string(body), `"name":"Skill"`) || strings.Contains(string(body), `"name":"Agent"`) {
						t.Error("child escaped real parent/agent tool selection", string(body))
					}
					if step == 1 {
						emitNativeTaskTool(w, "fork-child-write", "Write", map[string]any{"file_path": artifact, "content": "FORK_PHYSICAL_ARTIFACT"})
						return
					}
					if step != 2 || !strings.Contains(string(body), "fork-child-write") {
						t.Error("real child tool result not consumed exactly once", step, string(body))
					}
					emitPlanFixture(w, 2, "", "FORK_RESULT_TEXT")
					return
				}
				step := parent.Add(1)
				if entry == "tool" && step == 1 {
					emitNativeTaskTool(w, "parent-skill", "Skill", map[string]any{"skill": "forkprobe", "args": "ARG_TOKEN"})
					return
				}
				if entry == "tool" && step == 2 {
					if !strings.Contains(string(body), "FORK_RESULT_TEXT") || !strings.Contains(string(body), "completed (forked execution)") {
						t.Error("parent did not receive actual forked Skill result", string(body))
					}
					if strings.Contains(string(body), "fork-child-write") {
						t.Error("child tool envelopes leaked into parent history", string(body))
					}
				}
				if entry == "slash" && (!strings.Contains(string(body), "FORK_RESULT_TEXT") || strings.Contains(string(body), "fork-child-write")) {
					t.Error("next parent ASK lost the Source fork summary or gained child tool rows", string(body))
				}
				emitPlanFixture(w, 3, "", "PARENT_FINISHED")
			}))
			defer peer.Close()
			m := forkSkillMainWithParentSkillGrant(t, peer.URL, entry == "tool", func(o *MainPrintOptions) {
				o.OnOutput = func(data []byte) error {
					var frame protocol.JSONObject
					if err := frame.UnmarshalJSON(data); err != nil {
						return err
					}
					if frame.String("parent_tool_use_id") == "parent-skill" {
						message, _ := frame.Object("message")
						var blocks []protocol.JSONObject
						if err := json.Unmarshal(message.Get("content"), &blocks); err != nil {
							return err
						}
						if len(blocks) != 1 {
							return fmt.Errorf("Skill SDK progress did not retain one normalized child block: %s", data)
						}
						block := blocks[0]
						if frame.String("type") == "assistant" && block.String("type") == "tool_use" && block.String("id") == "fork-child-write" || frame.String("type") == "user" && block.String("type") == "tool_result" && block.String("tool_use_id") == "fork-child-write" {
							progress.Add(1)
						} else {
							return fmt.Errorf("unexpected Skill SDK progress route: %s", data)
						}
					}
					if bytes.Contains(data, []byte(`"type":"skill_progress"`)) {
						routeNoise.Add(1)
					}
					if bytes.Contains(data, []byte(`"subtype":"agent_run_message"`)) {
						routeNoise.Add(1)
					}
					if bytes.Contains(data, []byte(`"result":"FORK_RESULT_TEXT"`)) {
						slashResult.Store(true)
					}
					return nil
				}
			})
			artifact = filepath.Join(m.options.Native.CWD, "fork-artifact.txt")
			text := "/forkprobe ARG_TOKEN"
			if entry == "tool" {
				text = "Invoke forkprobe with ARG_TOKEN"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			err := m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": text})}))
			if err != nil {
				t.Fatalf("fork skill public consumer failed: %v", err)
			}
			data, err := os.ReadFile(artifact)
			if err != nil || string(data) != "FORK_PHYSICAL_ARTIFACT" || child.Load() != 2 {
				t.Fatalf("fork skill did not execute its real child Write: child=%d artifact=%q error=%v", child.Load(), data, err)
			}
			wantParent := int32(0)
			if entry == "tool" {
				wantParent = 2
			}
			if parent.Load() != wantParent {
				t.Fatal("slash shouldQuery/tool follow-up boundary changed", parent.Load(), wantParent)
			}
			if entry == "slash" {
				if !slashResult.Load() {
					t.Fatal("fork slash omitted original resultText from SDK result")
				}
				if err := m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": "Continue with the fork summary"})})); err != nil {
					t.Fatal(err)
				}
				if parent.Load() != 1 {
					t.Fatal("next parent ASK did not consume summary exactly once", parent.Load())
				}
			}
			if routeNoise.Load() != 0 {
				t.Fatal("fork emitted AgentTool-only SDK route", routeNoise.Load())
			}
			if entry == "tool" && progress.Load() != 2 {
				t.Fatal("real child tool progress was swallowed", progress.Load())
			}
			if changed, err := m.BackgroundForegroundAgents(ctx); err != nil || changed {
				t.Fatal("fork installed AgentTool promotion", changed, err)
			}
			if err = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				rows, err := m.Controller.BackgroundTasksOnOwner(scope)
				if err != nil {
					return err
				}
				if len(rows) != 0 {
					return fmt.Errorf("fork registered an AgentTool foreground task: %s", rawMain(rows))
				}
				encoded, _ := json.Marshal(m.Controller.History())
				if !strings.Contains(string(encoded), "FORK_RESULT_TEXT") {
					return fmt.Errorf("parent lost Source-required fork summary: %s", encoded)
				}
				retainedProgress := 0
				for _, record := range m.Controller.History() {
					if record.String("type") == "progress" {
						data, _ := record.Object("data")
						if data.String("type") == "skill_progress" {
							if entry != "tool" || record.String("parentToolUseID") != "parent-skill" || !strings.Contains(string(rawMain(record)), "fork-child-write") {
								return fmt.Errorf("retained Skill progress lost original parent/child identity: %s", rawMain(record))
							}
							retainedProgress++
						}
					} else if strings.Contains(string(rawMain(record)), "fork-child-write") {
						return fmt.Errorf("child query envelope promoted into ordinary parent history: %s", rawMain(record))
					}
				}
				if entry == "tool" && retainedProgress != 2 || entry == "slash" && retainedProgress != 0 {
					return fmt.Errorf("Source mutable Skill progress history count %d for %s", retainedProgress, entry)
				}
				permission, err := m.nativeToolPermissionContext(scope)
				if err != nil {
					return err
				}
				var allow protocol.JSONObject
				_ = allow.UnmarshalJSON(permission.Get("alwaysAllowRules"))
				if string(allow.Get("command")) != "null" && allow.Get("command") != nil && strings.Contains(string(allow.Get("command")), "Write") {
					return fmt.Errorf("child temporary grant leaked into parent: %s", rawMain(permission))
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := m.ShutdownSession(ctx); err != nil {
				t.Fatal("close actual forked Skill session before disk verification", err)
			}
			parents, err := filepath.Glob(filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "projects", "*", m.Controller.SessionID()+".jsonl"))
			if err != nil || len(parents) != 1 {
				t.Fatalf("actual parent transcript missing: %v %v", parents, err)
			}
			persisted, err := os.ReadFile(parents[0])
			if err != nil || !bytes.Contains(persisted, []byte("FORK_RESULT_TEXT")) {
				t.Fatalf("actual fork summary not persisted: %v %s", err, persisted)
			}
			if bytes.Contains(persisted, []byte("fork-child-write")) || bytes.Contains(persisted, []byte(`"type":"progress"`)) {
				t.Fatalf("Source-excluded progress or child query envelope entered parent disk chain: %s", persisted)
			}
		})
	}
}

func TestActualForkedSkillCancellationJoinsPhysicalChildAndLeavesNextParentTurnUsable(t *testing.T) {
	started, physicalDone := make(chan struct{}), make(chan struct{})
	var child, parent atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(200)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "FORK_PRIVATE_CHILD_SYSTEM") {
			if child.Add(1) != 1 {
				t.Error("child restarted")
				w.WriteHeader(500)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			close(started)
			<-r.Context().Done()
			close(physicalDone)
			return
		}
		parent.Add(1)
		emitPlanFixture(w, 1, "", "PARENT_AFTER_CANCEL")
	}))
	defer peer.Close()
	m := forkSkillMain(t, peer.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": "/forkprobe CANCEL"})}))
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("fork rejected before actual child HTTP: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("no actual child request")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("fork cancellation did not return")
	}
	select {
	case <-physicalDone:
	case <-time.After(10 * time.Second):
		t.Fatal("fork cancellation did not join actual child transport")
	}
	if changed, err := m.BackgroundForegroundAgents(context.Background()); err != nil || changed {
		t.Fatal("cancelled fork left promotion action", changed, err)
	}
	if err := m.RunTurn(context.Background(), protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": "normal parent after cancellation"})})); err != nil {
		t.Fatal(err)
	}
	if parent.Load() != 1 || child.Load() != 1 {
		t.Fatal("cancel restarted child or blocked next turn", parent.Load(), child.Load())
	}
	if err := m.ShutdownSession(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// Source completes before either old/current Go product consumer is admitted.
func checkActualSourceForkSkillContract(t *testing.T, n *NativeBoundary, requireParentSkillPermission bool) {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual pinned Source Bun required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-skill-fork.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	initialGo := forkSkillInitialTree(t, n.Home)
	sourceHome, sourceCWD, sourceConfig, sourceTemp := cloneForkSkillSourceInitial(t, n)
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = sourceHome
	cmd.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + sourceConfig, "TMPDIR=" + sourceTemp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(sourceHome), "CC_HAHA_NATIVE_FORK_SKILL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_CLAUDE_MDS=1", "CLAUDE_CODE_USE_NATIVE_FILE_SEARCH=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "ANTHROPIC_API_KEY=fake-fork-key", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "USER_TYPE=external", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": sourceHome, "cwd": sourceCWD, "requireParentSkillPermission": requireParentSkillPermission})
	cmd.Stdin = bytes.NewReader(data)
	output, err := cmd.CombinedOutput()
	goTreeUnchanged := reflect.DeepEqual(initialGo, forkSkillInitialTree(t, n.Home))
	if !goTreeUnchanged {
		t.Fatal("Source fixture changed the independent initial Go HOME")
	}
	t.Logf("FORK_SOURCE_CLONE %s", rawMain(map[string]any{"sourceHome": sourceHome, "sourceCWD": sourceCWD, "goHome": n.Home, "goCWD": n.CWD, "initialGoTreeUnchanged": goTreeUnchanged, "initialGoTree": initialGo}))
	if err != nil {
		t.Fatalf("actual Source fork contract failed before Go consumer: %v %.5000s", err, output)
	}
	var source struct {
		Completed                 int
		ParentPermissionUnchanged bool
		Slash                     struct {
			ShouldQuery bool
			ResultText  string
		}
		Tool struct {
			Data                                               struct{ Status, Result string }
			AgentIdPresent, HasNewMessages, HasContextModifier bool
		}
		Progress []struct {
			ToolUseID, Type, MessageType string
			AgentIdPresent               bool
		}
		Observations []struct {
			QuerySource  string
			CommandRules []string
			Effort       string
		}
		Capabilities          struct{ Blocked []string }
		ParentSkillPermission *struct {
			Behavior       string
			UpdatedInput   struct{ Skill, Args string }
			DecisionReason struct {
				Type string
				Rule struct {
					Source, RuleBehavior string
					RuleValue            struct{ ToolName, RuleContent string }
				}
			}
		}
	}
	if err := json.Unmarshal(output, &source); err != nil || source.Completed != 2 || !source.ParentPermissionUnchanged || source.Slash.ShouldQuery || source.Slash.ResultText != "FORK_RESULT_TEXT" || source.Tool.Data.Status != "forked" || source.Tool.Data.Result != "FORK_RESULT_TEXT" || !source.Tool.AgentIdPresent || source.Tool.HasNewMessages || source.Tool.HasContextModifier || len(source.Observations) != 2 || len(source.Capabilities.Blocked) != 0 {
		t.Fatalf("actual Source fork contract not qualified: %v %.5000s", err, output)
	}
	if requireParentSkillPermission {
		permission := source.ParentSkillPermission
		if permission == nil || permission.Behavior != "allow" || permission.UpdatedInput.Skill != "forkprobe" || permission.UpdatedInput.Args != "ARG_TOKEN" || permission.DecisionReason.Type != "rule" || permission.DecisionReason.Rule.Source != "userSettings" || permission.DecisionReason.Rule.RuleBehavior != "allow" || permission.DecisionReason.Rule.RuleValue.ToolName != "Skill" || permission.DecisionReason.Rule.RuleValue.RuleContent != "forkprobe" {
			t.Fatalf("actual Source parent Skill permission did not consume the precise private rule: %.5000s", output)
		}
	} else if source.ParentSkillPermission != nil {
		t.Fatalf("unrequested Source parent Skill approval qualification: %.5000s", output)
	}
	if len(source.Progress) != 2 {
		t.Fatalf("Source fork progress missing: %.5000s", output)
	}
	for _, event := range source.Progress {
		if event.Type != "skill_progress" || event.ToolUseID != "skill_source-parent" || !event.AgentIdPresent {
			t.Fatalf("Source fork progress route changed: %.5000s", output)
		}
	}
	for _, observation := range source.Observations {
		if observation.QuerySource != "agent:custom" || observation.Effort != "low" || len(observation.CommandRules) != 1 || observation.CommandRules[0] != "Write" {
			t.Fatalf("actual Source child context differs: %.5000s", output)
		}
	}
	t.Logf("FORK_SOURCE_CONTRACT %s", output)
}

func TestActualForkedSkillChildPermissionAskUsesParentAllowAndDenyWithoutPromotion(t *testing.T) {
	for _, behavior := range []string{"allow", "deny"} {
		t.Run(behavior, func(t *testing.T) {
			var calls, asks atomic.Int32
			var artifact string
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.WriteHeader(200)
					return
				}
				body, _ := io.ReadAll(r.Body)
				if calls.Add(1) == 1 {
					emitNativeTaskTool(w, "fork-ui-write", "Write", map[string]any{"file_path": artifact, "content": "FORK_UI_ARTIFACT"})
					return
				}
				if !strings.Contains(string(body), "fork-ui-write") {
					t.Error("child lost parent permission result", string(body))
				}
				if behavior == "deny" && !strings.Contains(string(body), "FORK_UI_DENIED") {
					t.Error("explicit parent denial not returned to child", string(body))
				}
				emitPlanFixture(w, 2, "", "FORK_UI_DONE")
			}))
			defer peer.Close()
			var m *MainPrint
			m = forkSkillMain(t, peer.URL, func(o *MainPrintOptions) {
				o.ResolvePermission = func(ctx context.Context, scope *cooperative.Scope, tool queryloop.RegisteredTool, in *protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
					if m == nil || !scope.BelongsTo(m.Owner) || tool.Name != "Write" || in == nil {
						return queryloop.ToolPermission{}, fmt.Errorf("permission callback escaped parent owner: %s", tool.Name)
					}
					actual, ok := call.Opaque.(*builtinwrite.CallContext)
					if !ok || actual.AgentID == "" {
						return queryloop.ToolPermission{}, fmt.Errorf("missing actual child permission identity")
					}
					asks.Add(1)
					return queryloop.ToolPermission{Behavior: behavior, Input: in, Message: quoteMain("FORK_UI_DENIED")}, nil
				}
			})
			artifact = filepath.Join(m.options.Native.CWD, "fork-ui-artifact.txt")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": "/forkask ARG_TOKEN"})})); err != nil {
				t.Fatal(err)
			}
			if asks.Load() != 1 || calls.Load() != 2 {
				t.Fatal("fork bypassed parent approval or replayed model", asks.Load(), calls.Load())
			}
			data, err := os.ReadFile(artifact)
			if behavior == "allow" && (err != nil || string(data) != "FORK_UI_ARTIFACT") {
				t.Fatal("parent allow did not reach actual Write", string(data), err)
			}
			if behavior == "deny" && !os.IsNotExist(err) {
				t.Fatal("parent denial allowed actual Write", string(data), err)
			}
			if changed, err := m.BackgroundForegroundAgents(ctx); err != nil || changed {
				t.Fatal("permission fork installed promotion", changed, err)
			}
		})
	}
}

func TestActualForkedSkillLiveExplicitDenySurvivesChildCommandGrantAndSettingsRefresh(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls, asks atomic.Int32
	var artifact string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(200)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if calls.Add(1) == 1 {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			emitNativeTaskTool(w, "fork-live-write", "Write", map[string]any{"file_path": artifact, "content": "MUST_NOT_WRITE"})
			return
		}
		var request struct{ Messages []protocol.JSONObject }
		if err := json.Unmarshal(body, &request); err != nil {
			t.Error(err)
		}
		denied := false
		for _, message := range request.Messages {
			var blocks []protocol.JSONObject
			if raw := message.Get("content"); len(raw) > 0 && raw[0] == '[' {
				if err := json.Unmarshal(raw, &blocks); err != nil {
					t.Error(err)
				}
			}
			for _, block := range blocks {
				if block.String("type") == "tool_result" && block.String("tool_use_id") == "fork-live-write" && string(block.Get("is_error")) == "true" {
					denied = true
				}
			}
		}
		if !denied {
			t.Error("new parent deny was lost after child ASK/permission refresh", string(body))
		}
		emitPlanFixture(w, 2, "", "LIVE_DENY_PRESERVED")
	}))
	defer peer.Close()
	m := forkSkillMain(t, peer.URL, func(o *MainPrintOptions) {
		o.ResolvePermission = func(_ context.Context, _ *cooperative.Scope, _ queryloop.RegisteredTool, in *protocol.JSONObject, _ queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
			asks.Add(1)
			return queryloop.ToolPermission{Behavior: "allow", Input: in}, nil
		}
	})
	artifact = filepath.Join(m.options.Native.CWD, "fork-live-denied.txt")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": "/forkprobe LIVE"})}))
	}()
	joined := false
	defer func() {
		cancel()
		if !joined {
			<-done
		}
	}()
	join := func() error { err := <-done; joined = true; return err }
	select {
	case <-started:
	case err := <-done:
		joined = true
		t.Fatalf("fork rejected before real live-policy HTTP: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// A disk edit is consumed by the existing real settings watcher while the
	// child request is held. The test never mutates live permission internals or
	// starts a second ASK stack. Ordinary Read only observes the watcher result.
	settingsPath := filepath.Join(m.options.Native.ConfigHomeDirectory(), "settings.json")
	settingsBytes, err := os.ReadFile(settingsPath)
	if err != nil {
		cancel()
		join()
		t.Fatal(err)
	}
	var settings, permissions protocol.JSONObject
	if err = settings.UnmarshalJSON(settingsBytes); err != nil {
		cancel()
		join()
		t.Fatal(err)
	}
	if raw := settings.Get("permissions"); raw != nil {
		if err = permissions.UnmarshalJSON(raw); err != nil {
			cancel()
			join()
			t.Fatal(err)
		}
	} else {
		permissions = protocol.NewJSONObject()
	}
	var denies []json.RawMessage
	if raw := permissions.Get("deny"); raw != nil {
		if err = json.Unmarshal(raw, &denies); err != nil {
			cancel()
			join()
			t.Fatal(err)
		}
	}
	permissions.Set("deny", rawMain(append(denies, quoteMain("Write"))))
	settings.Set("permissions", rawMain(permissions))
	if err = os.WriteFile(settingsPath, rawMain(settings), 0600); err != nil {
		cancel()
		join()
		t.Fatal(err)
	}
	watcherObserved := false
	var observationError error
	waitNativeSettings(t, func() bool {
		receipt, e := m.Owner.Submit(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
			actual, e := m.Host.NativePermissionContext(scope)
			if e != nil {
				return e
			}
			var rules protocol.JSONObject
			if e = rules.UnmarshalJSON(actual.Get("alwaysDenyRules")); e != nil {
				return e
			}
			var userRules []string
			if e = json.Unmarshal(rules.Get("userSettings"), &userRules); e != nil {
				return e
			}
			for _, rule := range userRules {
				if rule == "Write" {
					watcherObserved = true
				}
			}
			return nil
		})
		if e == nil {
			e = receipt.Wait()
		}
		observationError = e
		return e != nil || watcherObserved
	})
	if observationError != nil {
		cancel()
		join()
		t.Fatal(observationError)
	}
	if !watcherObserved {
		cancel()
		join()
		t.Fatal("real settings watcher did not publish the parent Write deny")
	}
	close(release)
	select {
	case err := <-done:
		joined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := os.Stat(artifact); !os.IsNotExist(err) {
		t.Fatal("explicit live deny was overridden by child command allow", err)
	}
	if asks.Load() != 0 || calls.Load() != 2 {
		t.Fatal("deny prompted UI or replayed child", asks.Load(), calls.Load())
	}
	if err := m.ShutdownSession(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// Source and Go start from two independent copies of the same private fixture.
// Only the known marketplace's two owned absolute paths need affine relocation;
// all other file bytes/modes are copied verbatim and undeclared references fail.
type forkSkillInitialEntry struct {
	Mode      uint32
	Directory bool
	SHA256    string
}

func forkSkillInitialTree(t *testing.T, home string) map[string]forkSkillInitialEntry {
	t.Helper()
	rows := map[string]forkSkillInitialEntry{}
	err := filepath.WalkDir(home, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !entry.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular fixture input: %s", path)
		}
		relative, err := filepath.Rel(home, path)
		if err != nil {
			return err
		}
		row := forkSkillInitialEntry{Mode: uint32(info.Mode().Perm()), Directory: entry.IsDir()}
		if !entry.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			row.SHA256 = fmt.Sprintf("%x", sha256.Sum256(data))
		}
		rows[relative] = row
		return nil
	})
	if err != nil {
		t.Fatal("private fork fixture inventory", err)
	}
	return rows
}
func cloneForkSkillSourceInitial(t *testing.T, n *NativeBoundary) (string, string, string, string) {
	t.Helper()
	home := t.TempDir()
	relocate := func(path string) string {
		t.Helper()
		relative, err := filepath.Rel(n.Home, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			t.Fatalf("fixture path escaped Go HOME: %s %v", path, err)
		}
		return filepath.Join(home, relative)
	}
	err := filepath.WalkDir(n.Home, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !entry.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular clone input: %s", path)
		}
		target := relocate(path)
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal("clone independent Source HOME", err)
	}
	path := filepath.Join(home, ".claude", "plugins", "known_marketplaces.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var known protocol.JSONObject
	if err = known.UnmarshalJSON(data); err != nil {
		t.Fatal(err)
	}
	market, ok := known.Object("mkt")
	if !ok {
		t.Fatal("missing original private marketplace")
	}
	source, ok := market.Object("source")
	if !ok || source.String("source") != "directory" || source.String("path") != filepath.Join(n.Home, "market") || market.String("installLocation") != filepath.Join(n.Home, "market") {
		t.Fatal("unexpected marketplace relocation", string(data))
	}
	originalKnown := append([]byte(nil), data...)
	source.Set("path", quoteMain(relocate(source.String("path"))))
	market.Set("source", rawMain(source))
	market.Set("installLocation", quoteMain(relocate(market.String("installLocation"))))
	known.Set("mkt", rawMain(market))
	if err = os.WriteFile(path, rawMain(known), 0600); err != nil {
		t.Fatal(err)
	}
	inverseSource := source.Clone()
	inverseMarket := market.Clone()
	inverseKnown := known.Clone()
	inverseSource.Set("path", quoteMain(filepath.Join(n.Home, "market")))
	inverseMarket.Set("source", rawMain(inverseSource))
	inverseMarket.Set("installLocation", quoteMain(filepath.Join(n.Home, "market")))
	inverseKnown.Set("mkt", rawMain(inverseMarket))
	if !bytes.Equal(rawMain(inverseKnown), originalKnown) {
		t.Fatal("marketplace inverse changed undeclared initial bytes")
	}
	originalRows, cloneRows := forkSkillInitialTree(t, n.Home), forkSkillInitialTree(t, home)
	if len(originalRows) != len(cloneRows) {
		t.Fatal("clone changed initial fixture entry count")
	}
	marketplaceRelative := filepath.Join(".claude", "plugins", "known_marketplaces.json")
	for relative, original := range originalRows {
		copied, ok := cloneRows[relative]
		if !ok || copied.Mode != original.Mode || copied.Directory != original.Directory || relative != marketplaceRelative && copied.SHA256 != original.SHA256 {
			t.Fatalf("initial clone changed non-relocated file: %s", relative)
		}
	}
	err = filepath.WalkDir(home, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(n.Home)) {
			return fmt.Errorf("undeclared original Go HOME reference in clone: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return home, relocate(n.CWD), relocate(n.ConfigHomeDirectory()), relocate(n.Temp)
}
