package clibootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func hookPhysicalWriteFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
func hookDeclaration(command string, once bool) map[string]any {
	return map[string]any{"type": "command", "command": command, "once": once, "timeout": 15}
}
func hookMatcher(matcher string, commands ...map[string]any) []any {
	return []any{map[string]any{"matcher": matcher, "hooks": commands}}
}
func hooksPublicUser(text string) protocol.JSONObject {
	return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": text})})
}

type frontmatterSourceReceipt struct {
	Profile, Entry                                           string
	Events                                                   []string
	Inputs                                                   []protocol.JSONObject
	Artifacts                                                []*string
	PermissionCalls, OutsideRoots                            []string
	ChildQueries, ChildFinally                               int
	AgentSessionCountAfter, ParentStopFailureCountAfterChild *int
	SessionCountAfter                                        int
	ParentPermissionUnchanged, Aborted                       bool
	SlashShouldQuery                                         *bool
	ToolResultSeen                                           bool
	Physical                                                 struct {
		CorpusSHA256 string
		AllClosed    bool
		Remaining    int
		Rows         []struct {
			Kind, Command string
			PID           int
			Spawned       bool
			Close         *struct {
				Code   *int
				Signal *string
			}
			Error json.RawMessage
		}
	}
	Capabilities struct{ Blocked []string }
}
type hookInitialRow struct {
	Relative string
	Mode     uint32
	SHA256   string
}

func hookInitialSnapshot(root string) ([]hookInitialRow, error) {
	rows := []hookInitialRow{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, failure error) error {
		if failure != nil {
			return failure
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		row := hookInitialRow{Relative: relative, Mode: uint32(info.Mode())}
		if info.IsDir() {
			rows = append(rows, row)
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unowned initial fixture type %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		row.SHA256 = hex.EncodeToString(sum[:])
		rows = append(rows, row)
		return nil
	})
	return rows, err
}
func cloneHookInitialHome(t *testing.T, n *NativeBoundary) (string, []hookInitialRow) {
	t.Helper()
	before, err := hookInitialSnapshot(n.Home)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	for _, row := range before {
		if row.Relative == "." {
			continue
		}
		from, to := filepath.Join(n.Home, row.Relative), filepath.Join(home, row.Relative)
		mode := os.FileMode(row.Mode)
		if mode.IsDir() {
			if err := os.Mkdir(to, mode.Perm()); err != nil {
				t.Fatal(err)
			}
			continue
		}
		data, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		// This generated fixture has one structured absolute-path field family.
		// Relocate only its known marketplace paths; all unknown fields survive.
		if row.Relative == filepath.Join(".claude", "plugins", "known_marketplaces.json") {
			var object map[string]map[string]json.RawMessage
			if err := json.Unmarshal(data, &object); err != nil {
				t.Fatal(err)
			}
			mkt := object["mkt"]
			var source map[string]string
			if err := json.Unmarshal(mkt["source"], &source); err != nil || source["path"] != filepath.Join(n.Home, "market") {
				t.Fatal("unexpected fixture marketplace source", err)
			}
			var location string
			if err := json.Unmarshal(mkt["installLocation"], &location); err != nil || location != filepath.Join(n.Home, "market") {
				t.Fatal("unexpected fixture marketplace location", err)
			}
			source["path"] = filepath.Join(home, "market")
			mkt["source"], _ = json.Marshal(source)
			mkt["installLocation"], _ = json.Marshal(filepath.Join(home, "market"))
			data, err = json.Marshal(object)
			if err != nil {
				t.Fatal(err)
			}
		} else if bytes.Contains(data, []byte(n.Home)) {
			t.Fatalf("initial fixture contains undeclared absolute root in %s", row.Relative)
		}
		if err := os.WriteFile(to, data, mode.Perm()); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(to, mode.Perm()); err != nil {
			t.Fatal(err)
		}
	}
	cloned, err := hookInitialSnapshot(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(cloned) != len(before) {
		t.Fatal("initial private clone inventory differs")
	}
	for i, row := range before {
		other := cloned[i]
		if row.Relative != other.Relative || row.Mode != other.Mode || row.SHA256 != other.SHA256 && row.Relative != filepath.Join(".claude", "plugins", "known_marketplaces.json") {
			t.Fatal("initial private clone differs outside declared marketplace roots", row, other)
		}
	}
	t.Logf("FRONTMATTER_HOOK_INITIAL_CLONE %s", rawMain(map[string]any{"originalHome": n.Home, "sourceHome": home, "rootMapping": map[string]string{n.Home: home}, "onlyStructuredContentRelocation": filepath.Join(".claude", "plugins", "known_marketplaces.json"), "original": before, "source": cloned}))
	return home, before
}
func checkActualSourceFrontmatterHooks(t *testing.T, n *NativeBoundary, profile, entry string, background bool, corpusSHA string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("finite physical POSIX shell fixture; Windows acceptance is separate")
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual pinned Source Bun required")
	}
	sourceHome, originalBefore := cloneHookInitialHome(t, n)
	sourceCWD := filepath.Join(sourceHome, "project")
	sourceConfig := filepath.Join(sourceHome, ".claude")
	sourceTemp := filepath.Join(sourceHome, "tmp")
	effects := filepath.Join(sourceCWD, "go-hook-effects")
	sourceCorpus := filepath.Join(sourceHome, "hook-commands.json")
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	source, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-frontmatter-hooks.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", source)
	cmd.Dir = sourceHome
	cmd.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + sourceConfig, "TMPDIR=" + sourceTemp, "TMP=" + sourceTemp, "TEMP=" + sourceTemp, "CLAUDE_CODE_TMPDIR=" + sourceTemp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(sourceHome), "CC_HAHA_NATIVE_FRONTMATTER_HOOKS_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_CLAUDE_MDS=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS=1", "CLAUDE_CODE_DISABLE_ATTACHMENTS=1", "CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING=1", "ANTHROPIC_API_KEY=fake-hook-source-key", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "USER_TYPE=external", "PATH=/usr/bin:/bin"}
	permissionMode := "default"
	if profile == "flaky" || profile == "cancel" || profile == "agent" || profile == "parent-stop-failure" {
		permissionMode = "bypassPermissions"
	}
	payload, _ := json.Marshal(map[string]any{"homeDir": sourceHome, "cwd": sourceCWD, "effects": effects, "profile": profile, "permissionMode": permissionMode, "entry": entry, "background": background, "corpusPath": sourceCorpus, "corpusSHA256": corpusSHA})
	cmd.Stdin = bytes.NewReader(payload)
	output, err := cmd.CombinedOutput()
	originalAfter, snapshotErr := hookInitialSnapshot(n.Home)
	if snapshotErr != nil || !reflect.DeepEqual(originalBefore, originalAfter) {
		t.Fatalf("Source changed Go initial fixture: %v", snapshotErr)
	}
	if err != nil {
		t.Fatalf("actual Source frontmatter hook consumer failed before Go admission: %v %s", err, output)
	}
	var receipt frontmatterSourceReceipt
	if err := json.Unmarshal(output, &receipt); err != nil || len(receipt.Artifacts) != 3 || receipt.Profile != profile || receipt.Entry != entry || len(receipt.Capabilities.Blocked) != 0 || !receipt.Physical.AllClosed || receipt.Physical.Remaining != 0 || receipt.Physical.CorpusSHA256 != corpusSHA || !receipt.ParentPermissionUnchanged || receipt.SessionCountAfter != 0 {
		t.Fatalf("actual Source physical hook receipt not qualified: %v %.16000s", err, output)
	}
	if len(receipt.Inputs) != len(receipt.Events) {
		t.Fatal("Source physical input/event count differs", string(output))
	}
	if len(receipt.Physical.Rows) == 0 {
		t.Fatalf("Source did not execute any physical native hook: %.16000s", output)
	}
	for _, row := range receipt.Physical.Rows {
		if row.PID <= 0 || !row.Spawned || row.Close == nil || string(row.Error) != "null" {
			t.Fatalf("Source native hook did not spawn/join: %.16000s", output)
		}
	}
	events := strings.Join(receipt.Events, "\n")
	want := ""
	switch profile {
	case "inline":
		want = "pre:" + filepath.Join(sourceCWD, ".claude", "skills", "hookprobe") + "\npost\npost\nstop"
		if receipt.Artifacts[0] == nil || *receipt.Artifacts[0] != "PHYSICAL_1" || receipt.Artifacts[1] == nil || *receipt.Artifacts[1] != "PHYSICAL_2" {
			t.Fatal("Source actual Write absent", string(output))
		}
	case "deny":
		want = "deny"
		if receipt.Artifacts[0] != nil {
			t.Fatal("Source hook deny allowed physical Write", string(output))
		}
		for _, name := range receipt.PermissionCalls {
			if name == "Write" {
				t.Fatal("Source hook deny reached UI permission", string(output))
			}
		}
	case "flaky":
		want = "once-failure\nonce-success"
		for _, artifact := range receipt.Artifacts {
			if artifact == nil {
				t.Fatal("Source actual failed-once hook blocked tool or lost later Write", string(output))
			}
		}
	case "parent-stop-failure":
		want = "stop-failure"
		if receipt.ChildQueries != 1 || receipt.ChildFinally != 1 || receipt.ParentStopFailureCountAfterChild == nil || *receipt.ParentStopFailureCountAfterChild != 1 || receipt.Artifacts[0] != nil || receipt.Artifacts[2] == nil || *receipt.Artifacts[2] != "PHYSICAL_3" {
			t.Fatal("Source parent Skill hooks did not survive ordinary child finally", string(output))
		}
	case "stop-failure":
		want = "stop-failure\nstop-failure"
		if receipt.Artifacts[0] != nil {
			t.Fatal("Source StopFailure unexpectedly executed Write", string(output))
		}
	case "cancel":
		want = "held"
		if !receipt.Aborted || receipt.Artifacts[0] != nil {
			t.Fatal("Source did not cancel physical hook before Write", string(output))
		}
	case "agent":
		want = "agent-pre\nagent-post\nagent-pre\nagent-post\nagent-stop"
		if receipt.ChildQueries != 1 || receipt.ChildFinally != 1 || receipt.AgentSessionCountAfter == nil || *receipt.AgentSessionCountAfter != 0 || receipt.Artifacts[2] == nil || *receipt.Artifacts[2] != "PHYSICAL_3" {
			t.Fatal("Source Agent finally/parent isolation incomplete", string(output))
		}
	default:
		t.Fatal("unowned hook profile", profile)
	}
	if profile == "stop-failure" || profile == "parent-stop-failure" {
		wantRoots := []string{"<absent>"}
		if profile == "stop-failure" {
			wantRoots = append(wantRoots, "<absent>")
		}
		if !reflect.DeepEqual(receipt.OutsideRoots, wantRoots) {
			t.Fatal("Source outside executor unexpectedly passed Skill root", receipt.OutsideRoots, string(output))
		}
	}
	if events != want {
		t.Fatalf("Source actual hook side-effect order differs: got%q want%q %.16000s", events, want, output)
	}
	if profile != "agent" && entry == "slash" && (receipt.SlashShouldQuery == nil || !*receipt.SlashShouldQuery) {
		t.Fatal("Source inline slash stopped querying", string(output))
	}
	if profile != "agent" && entry == "tool" && !receipt.ToolResultSeen {
		t.Fatal("Source actual Skill tool result missing", string(output))
	}
	t.Logf("FRONTMATTER_HOOK_SOURCE_PROCESS %s", rawMain(map[string]any{"pid": cmd.Process.Pid, "exitCode": cmd.ProcessState.ExitCode(), "contextError": fmt.Sprint(ctx.Err()), "sourceHome": sourceHome, "sourceCWD": sourceCWD, "originalGoInitialHomeUnchanged": true}))
	t.Logf("FRONTMATTER_HOOK_SOURCE %s", output)
}

func frontmatterHookMain(t *testing.T, url, profile, entry string, background bool, configure func(*MainPrintOptions)) *MainPrint {
	t.Helper()
	return backgroundAgentMainWithOptions(t, url, func(n *NativeBoundary) {
		// The original Go HOME and its Source clone share the same private 0700 mode.
		if err := os.Chmod(n.Home, 0700); err != nil {
			t.Fatal(err)
		}
		n.Environment["CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS"] = "1"
		n.Environment["CLAUDE_CODE_DISABLE_CLAUDE_MDS"] = "1"
		n.Environment["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "1"
		n.Environment["CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING"] = "1"
		effects := filepath.Join(n.CWD, "go-hook-effects")
		if err := os.MkdirAll(effects, 0700); err != nil {
			t.Fatal(err)
		}
		n.Environment["HOOK_OUTPUT_DIR"] = effects
		if profile == "inline" || profile == "deny" {
			hookPhysicalWriteFile(t, filepath.Join(n.ConfigHomeDirectory(), "settings.json"), `{"enabledPlugins":{"sample@mkt":false},"permissions":{"ask":["Write"]}}`)
		}
		corpus, err := filepath.Abs("../../../scripts/quality-gate/go-parity/testdata/native-frontmatter-hooks/commands.json")
		if err != nil {
			t.Fatal(err)
		}
		bytes, err := os.ReadFile(corpus)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(bytes)
		corpusSHA := hex.EncodeToString(hash[:])
		corpusPath := filepath.Join(n.Home, "hook-commands.json")
		if err := os.WriteFile(corpusPath, bytes, 0600); err != nil {
			t.Fatal(err)
		}
		var commands map[string]string
		if err := json.Unmarshal(bytes, &commands); err != nil {
			t.Fatal(err)
		}
		hooks := map[string]any{}
		switch profile {
		case "inline":
			hooks["PreToolUse"] = hookMatcher("Write", hookDeclaration(commands["pre"], true))
			hooks["PostToolUse"] = hookMatcher("Write", hookDeclaration(commands["post"], false))
			hooks["Stop"] = hookMatcher("", hookDeclaration(commands["stop"], false))
		case "deny":
			hooks["PreToolUse"] = hookMatcher("Write", hookDeclaration(commands["deny"], false))
		case "flaky":
			hooks["PreToolUse"] = hookMatcher("Write", hookDeclaration(commands["flaky"], true))
		case "parent-stop-failure":
			hooks["StopFailure"] = hookMatcher("", hookDeclaration(commands["stop-failure"], false))
		case "stop-failure":
			hooks["StopFailure"] = hookMatcher("", hookDeclaration(commands["stop-failure"], true))
		case "cancel":
			hooks["PreToolUse"] = hookMatcher("Write", hookDeclaration(commands["held"], true))
		case "agent":
			hooks["PreToolUse"] = hookMatcher("Write", hookDeclaration(commands["agent-pre"], true))
			hooks["PostToolUse"] = hookMatcher("Write", hookDeclaration(commands["agent-post"], false))
			hooks["Stop"] = hookMatcher("hook-worker", hookDeclaration(commands["agent-stop"], false))
		default:
			t.Fatal("unowned profile", profile)
		}
		raw, _ := json.Marshal(hooks)
		skillHooks := raw
		if profile == "agent" {
			skillHooks = []byte("{}")
		}
		hookPhysicalWriteFile(t, filepath.Join(n.CWD, ".git", "HEAD"), "ref: refs/heads/main\n")
		hookPhysicalWriteFile(t, filepath.Join(n.CWD, ".claude", "skills", "hookprobe", "SKILL.md"), "---\nname: hookprobe\ndescription: Actual inline physical hooks\nhooks: "+string(skillHooks)+"\n---\nHOOK_INLINE_INPUT\n")
		if profile == "agent" {
			hookPhysicalWriteFile(t, filepath.Join(n.CWD, ".claude", "agents", "hook-worker.md"), "---\nname: hook-worker\ndescription: Actual hooks-only Agent\ntools: Write,Read\nmodel: inherit\nhooks: "+string(raw)+"\n---\nHOOK_CHILD_SYSTEM\n")
		}
		if profile == "parent-stop-failure" {
			hookPhysicalWriteFile(t, filepath.Join(n.CWD, ".claude", "agents", "plain-worker.md"), "---\nname: plain-worker\ndescription: Actual ordinary Agent without frontmatter hooks\ntools: Write,Read\nmodel: inherit\n---\nHOOK_PLAIN_CHILD_SYSTEM\n")
		}
		checkActualSourceFrontmatterHooks(t, n, profile, entry, background, corpusSHA)
	}, func(o *MainPrintOptions) {
		o.Arguments.Tools = []string{"Skill,Write,Read,Agent,TaskOutput"}
		o.Arguments.PermissionMode = "default"
		if configure != nil {
			configure(o)
		}
	})
}
func hookEffects(t *testing.T, m *MainPrint) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(m.options.Native.CWD, "go-hook-effects", "events"))
	if err != nil {
		t.Fatalf("actual public frontmatter hook side effects absent: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}
func checkHookInputs(t *testing.T, m *MainPrint, want int) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(m.options.Native.CWD, "go-hook-effects", "inputs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(rows) != want {
		t.Fatal("actual hook stdin count", len(rows), want, string(data))
	}
	for _, row := range rows {
		var in protocol.JSONObject
		if err := in.UnmarshalJSON([]byte(row)); err != nil || in.String("session_id") != m.Controller.SessionID() || in.String("cwd") != m.options.Native.CWD || in.String("hook_event_name") == "" {
			t.Fatal("actual physical hook stdin lost identity", err, row)
		}
	}
}

// Both old and current compile this public test. Source proof is completed in
// setup before old Go can reject frontmatter or silently omit physical hooks.
func TestActualInlineSkillFrontmatterHooksPublicSlashAndToolRunPhysicalOncePostStop(t *testing.T) {
	for _, entry := range []string{"slash", "tool"} {
		t.Run(entry, func(t *testing.T) {
			var rounds, asks atomic.Int32
			var effects string
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.WriteHeader(200)
					return
				}
				body, _ := io.ReadAll(r.Body)
				step := rounds.Add(1)
				offset := int32(0)
				if entry == "tool" {
					offset = 1
					if step == 1 {
						emitNativeTaskTool(w, "skill-admission", "Skill", map[string]any{"skill": "hookprobe"})
						return
					}
				}
				if step-offset <= 2 {
					index := step - offset
					emitNativeTaskTool(w, fmt.Sprintf("hook-write-%d", index), "Write", map[string]any{"file_path": filepath.Join(effects, fmt.Sprintf("artifact-%d.txt", index)), "content": fmt.Sprintf("PHYSICAL_%d", index)})
					return
				}
				if !strings.Contains(string(body), "hook-write-2") {
					t.Error("actual model did not consume second physical Write", string(body))
				}
				emitPlanFixture(w, 4, "", "HOOK_PARENT_DONE")
			}))
			defer peer.Close()
			m := frontmatterHookMain(t, peer.URL, "inline", entry, false, func(o *MainPrintOptions) {
				o.ResolvePermission = func(_ context.Context, _ *cooperative.Scope, _ queryloop.RegisteredTool, in *protocol.JSONObject, _ queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
					asks.Add(1)
					return queryloop.ToolPermission{Behavior: "allow", Input: in}, nil
				}
			})
			effects = filepath.Join(m.options.Native.CWD, "go-hook-effects")
			text := "/hookprobe"
			if entry == "tool" {
				text = "Call hookprobe"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := m.RunTurn(ctx, hooksPublicUser(text)); err != nil {
				t.Fatalf("actual public Skill hook consumer failed: %v", err)
			}
			want := []string{"pre:" + filepath.Join(m.options.Native.CWD, ".claude", "skills", "hookprobe"), "post", "post", "stop"}
			if got := hookEffects(t, m); strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatal("actual public Skill hook order/once/root differs", got, want)
			}
			for i := 1; i <= 2; i++ {
				data, err := os.ReadFile(filepath.Join(effects, fmt.Sprintf("artifact-%d.txt", i)))
				if err != nil || string(data) != fmt.Sprintf("PHYSICAL_%d", i) {
					t.Fatal("actual physical Write missing", string(data), err)
				}
			}
			checkHookInputs(t, m, 4)
			if asks.Load() < 2 {
				t.Fatal("hooks bypassed original default permission consumer", asks.Load())
			}
			if err := m.ShutdownSession(ctx); err != nil {
				t.Fatal(err)
			}
			if m.hook.NativeAgentHookCount(m.Controller.SessionID()) != 0 {
				t.Fatal("main shutdown retained skill hooks")
			}
		})
	}
}

func TestActualInlineSkillFrontmatterHookDenyPrecedesWriteAndPermission(t *testing.T) {
	var rounds, writeAsks atomic.Int32
	var effects string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(200)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if rounds.Add(1) == 1 {
			emitNativeTaskTool(w, "denied-hook-write", "Write", map[string]any{"file_path": filepath.Join(effects, "artifact-1.txt"), "content": "MUST_NOT_WRITE"})
			return
		}
		if !strings.Contains(string(body), "HOOK_REJECTED") {
			t.Error("actual hook denial did not reach next ASK", string(body))
		}
		emitPlanFixture(w, 2, "", "HOOK_DENIAL_CONSUMED")
	}))
	defer peer.Close()
	m := frontmatterHookMain(t, peer.URL, "deny", "slash", false, func(o *MainPrintOptions) {
		o.ResolvePermission = func(_ context.Context, _ *cooperative.Scope, tool queryloop.RegisteredTool, in *protocol.JSONObject, _ queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
			if tool.Name == "Write" {
				writeAsks.Add(1)
			}
			return queryloop.ToolPermission{Behavior: "allow", Input: in}, nil
		}
	})
	effects = filepath.Join(m.options.Native.CWD, "go-hook-effects")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := m.RunTurn(ctx, hooksPublicUser("/hookprobe")); err != nil {
		t.Fatal(err)
	}
	if got := hookEffects(t, m); strings.Join(got, "\n") != "deny" {
		t.Fatal(got)
	}
	if _, err := os.Stat(filepath.Join(effects, "artifact-1.txt")); !os.IsNotExist(err) {
		t.Fatal("hook denied Write ran", err)
	}
	if writeAsks.Load() != 0 || rounds.Load() != 2 {
		t.Fatal("hook deny bypassed permission/result ordering", writeAsks.Load(), rounds.Load())
	}
	checkHookInputs(t, m, 1)
}

func TestActualInlineSkillFrontmatterOnceRetainsFailureAndRemovesOnlyAfterSuccess(t *testing.T) {
	var rounds atomic.Int32
	var effects string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(200)
			return
		}
		if step := rounds.Add(1); step <= 3 {
			emitNativeTaskTool(w, fmt.Sprintf("flaky-write-%d", step), "Write", map[string]any{"file_path": filepath.Join(effects, fmt.Sprintf("artifact-%d.txt", step)), "content": fmt.Sprintf("PHYSICAL_%d", step)})
			return
		}
		emitPlanFixture(w, 4, "", "ONCE_LIFECYCLE_DONE")
	}))
	defer peer.Close()
	m := frontmatterHookMain(t, peer.URL, "flaky", "slash", false, func(o *MainPrintOptions) { o.Arguments.PermissionMode = "bypassPermissions" })
	effects = filepath.Join(m.options.Native.CWD, "go-hook-effects")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := m.RunTurn(ctx, hooksPublicUser("/hookprobe")); err != nil {
		t.Fatal(err)
	}
	if got := hookEffects(t, m); strings.Join(got, "\n") != "once-failure\nonce-success" {
		t.Fatal("one-shot hook removed on failure or retained after success", got)
	}
	for i := 1; i <= 3; i++ {
		if _, err := os.Stat(filepath.Join(effects, fmt.Sprintf("artifact-%d.txt", i))); err != nil {
			t.Fatal(err)
		}
	}
	checkHookInputs(t, m, 2)
}

func TestActualOrdinaryForegroundAndBackgroundAgentFrontmatterHooksUseChildScopeAndJoin(t *testing.T) {
	for _, background := range []bool{false, true} {
		name := "foreground"
		if background {
			name = "background"
		}
		t.Run(name, func(t *testing.T) {
			var parent, child atomic.Int32
			var effects string
			var mu sync.Mutex
			agentID := ""
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.WriteHeader(200)
					return
				}
				body, _ := io.ReadAll(r.Body)
				if strings.Contains(string(body), "HOOK_CHILD_SYSTEM") {
					step := child.Add(1)
					if step <= 2 {
						emitNativeTaskTool(w, fmt.Sprintf("child-hook-write-%d", step), "Write", map[string]any{"file_path": filepath.Join(effects, fmt.Sprintf("artifact-%d.txt", step)), "content": fmt.Sprintf("PHYSICAL_%d", step)})
						return
					}
					emitPlanFixture(w, 3, "", "HOOK_CHILD_DONE")
					return
				}
				step := parent.Add(1)
				if step == 1 {
					emitNativeTaskTool(w, "hook-agent", "Agent", map[string]any{"description": "Actual hooks-only ordinary Agent", "subagent_type": "hook-worker", "prompt": "HOOK_CHILD_INPUT", "run_in_background": background})
					return
				}
				match := regexp.MustCompile(`agentId:\s+([a-zA-Z0-9_-]+)`).FindSubmatch(body)
				if len(match) > 1 {
					mu.Lock()
					agentID = string(match[1])
					mu.Unlock()
				}
				if background && step == 2 {
					if len(match) < 2 {
						t.Error("actual background Agent omitted task identity", string(body))
						w.WriteHeader(500)
						return
					}
					emitNativeTaskTool(w, "hook-task-output", "TaskOutput", map[string]any{"task_id": string(match[1]), "block": true, "timeout": 10000})
					return
				}
				offset := int32(0)
				if background {
					offset = 1
				}
				if step == 2+offset {
					if !strings.Contains(string(body), "HOOK_CHILD_DONE") {
						t.Error("actual parent did not join child result", string(body))
					}
					emitNativeTaskTool(w, "parent-after-child", "Write", map[string]any{"file_path": filepath.Join(effects, "artifact-3.txt"), "content": "PHYSICAL_3"})
					return
				}
				emitPlanFixture(w, 4, "", "HOOK_PARENT_DONE")
			}))
			defer peer.Close()
			m := frontmatterHookMain(t, peer.URL, "agent", "tool", background, func(o *MainPrintOptions) { o.Arguments.PermissionMode = "bypassPermissions" })
			effects = filepath.Join(m.options.Native.CWD, "go-hook-effects")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := m.RunTurn(ctx, hooksPublicUser("Run the ordinary hook-worker Agent")); err != nil {
				t.Fatal(err)
			}
			want := "agent-pre\nagent-post\nagent-pre\nagent-post\nagent-stop"
			if got := hookEffects(t, m); strings.Join(got, "\n") != want {
				t.Fatal("actual ordinary Agent hooks missing/once misapplied/parent leaked", got, want)
			}
			checkHookInputs(t, m, 5)
			mu.Lock()
			id := agentID
			mu.Unlock()
			if id == "" {
				t.Fatal("public Agent output omitted real child identity")
			}
			if m.hook.NativeAgentHookCount(id) != 0 {
				t.Fatal("ordinary Agent finally retained child hooks", id)
			}
			if child.Load() != 3 {
				t.Fatal("actual child query replayed or omitted Write", child.Load())
			}
			data, err := os.ReadFile(filepath.Join(effects, "artifact-3.txt"))
			if err != nil || string(data) != "PHYSICAL_3" {
				t.Fatal("parent unusable after real child join", string(data), err)
			}
		})
	}
}

func TestActualInlineSkillFrontmatterHookCancellationJoinsPhysicalProcessBeforeReturn(t *testing.T) {
	var calls atomic.Int32
	var effects string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(200)
			return
		}
		if calls.Add(1) == 1 {
			emitNativeTaskTool(w, "held-hook-write", "Write", map[string]any{"file_path": filepath.Join(effects, "artifact-1.txt"), "content": "MUST_NOT_WRITE"})
			return
		}
		emitPlanFixture(w, 2, "", "PARENT_AFTER_HOOK_CANCEL")
	}))
	defer peer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var seen atomic.Bool
	m := frontmatterHookMain(t, peer.URL, "cancel", "slash", false, func(o *MainPrintOptions) {
		o.HookEvents = true
		o.Arguments.IncludeHookEvents = true
		o.Arguments.PermissionMode = "bypassPermissions"
		o.OnOutput = func(data []byte) error {
			var event protocol.JSONObject
			if event.UnmarshalJSON(bytes.TrimSpace(data)) == nil && event.String("subtype") == "hook_progress" && strings.Contains(event.String("stdout"), "HOOK_PHYSICAL_STARTED") {
				seen.Store(true)
				cancel()
			}
			return nil
		}
	})
	effects = filepath.Join(m.options.Native.CWD, "go-hook-effects")
	done := make(chan error, 1)
	go func() { done <- m.RunTurn(ctx, hooksPublicUser("/hookprobe")) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		cancel()
		<-done
		t.Fatal("actual cancelled hook did not return and join")
	}
	if !seen.Load() {
		t.Fatal("cancellation did not observe actual physical hook stdout")
	}
	if got := hookEffects(t, m); strings.Join(got, "\n") != "held" {
		t.Fatal(got)
	}
	if _, err := os.Stat(filepath.Join(effects, "artifact-1.txt")); !os.IsNotExist(err) {
		t.Fatal("cancelled hook allowed Write", err)
	}
	pidBytes, pidErr := os.ReadFile(filepath.Join(effects, "held.pid"))
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if pidErr != nil || parseErr != nil || pid <= 0 {
		t.Fatal("actual held hook PID not recorded", pidErr, parseErr, string(pidBytes))
	}
	process, findErr := os.FindProcess(pid)
	if findErr != nil {
		t.Fatal(findErr)
	}
	if err := process.Signal(syscall.Signal(0)); err == nil || !errors.Is(err, syscall.ESRCH) && !errors.Is(err, os.ErrProcessDone) {
		t.Fatal("cancelled public hook left its physical process alive or unknown", pid, err)
	}
	ctx2, stop := context.WithTimeout(context.Background(), 15*time.Second)
	defer stop()
	// Physical process supervision must be joined before ShutdownSession returns.
	if err := m.ShutdownSession(ctx2); err != nil {
		t.Fatal(err)
	}
	if m.hook.NativeAgentHookCount(m.Controller.SessionID()) != 0 {
		t.Fatal("cancelled session retained hooks")
	}
}

func TestActualInlineSkillFrontmatterStopFailureRunsPhysicalHookAfterPublicAPIError(t *testing.T) {
	var posts atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(200)
			return
		}
		posts.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"rate_limit_error","message":"FAKE_RATE_LIMIT"}}`)
	}))
	defer peer.Close()
	m := frontmatterHookMain(t, peer.URL, "stop-failure", "slash", false, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// API failure is the deliberately provided input. Require the real terminal
	// hook effect and its original error fields, regardless of print error shape.
	failure := m.RunTurn(ctx, hooksPublicUser("/hookprobe"))
	// Source's outside-REPL Promise.all executor does not invoke the common
	// executeHooks success callback. Thus this once:true registration remains
	// visible for a second real API error without reinvoking the Skill.
	_ = m.RunTurn(ctx, hooksPublicUser("Second original API error without reinvoking skill"))
	if got := hookEffects(t, m); strings.Join(got, "\n") != "stop-failure\nstop-failure" {
		t.Fatal("outside-REPL StopFailure changed Source once semantics", got, failure)
	}
	checkHookInputs(t, m, 2)
	data, err := os.ReadFile(filepath.Join(m.options.Native.CWD, "go-hook-effects", "inputs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var input protocol.JSONObject
		if err := input.UnmarshalJSON(row); err != nil || input.String("hook_event_name") != "StopFailure" || input.String("error") == "" || !strings.Contains(input.String("last_assistant_message"), "FAKE_RATE_LIMIT") {
			t.Fatal("public error envelope was not the native StopFailure producer", err, string(row), failure)
		}
	}
	outsideRoots, rootErr := os.ReadFile(filepath.Join(m.options.Native.CWD, "go-hook-effects", "outside-roots"))
	if rootErr != nil || string(outsideRoots) != "<absent>\n<absent>\n" {
		t.Fatal("outside executor changed Source Skill-root omission", string(outsideRoots), rootErr)
	}
	if m.hook.NativeAgentHookCount(m.Controller.SessionID()) != 1 {
		t.Fatal("outside-REPL once registration was unexpectedly consumed")
	}
	if posts.Load() != 2 {
		t.Fatal("two real API-error turns retried or were dropped despite zero retry fixture", posts.Load())
	}
}

// Source runAgent keeps the root sessionHooks Map even without an Agent preset.
// Its StopFailure wrapper intentionally selects the main SID rather than AgentID.
func TestActualParentSkillStopFailureReachesOrdinaryAgentWithoutFrontmatterHooks(t *testing.T) {
	var parent, child atomic.Int32
	var effects string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(200)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "HOOK_PLAIN_CHILD_SYSTEM") {
			child.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"rate_limit_error","message":"FAKE_RATE_LIMIT"}}`)
			return
		}
		switch step := parent.Add(1); step {
		case 1:
			emitNativeTaskTool(w, "plain-agent", "Agent", map[string]any{"description": "Actual no-preset Agent failure", "subagent_type": "plain-worker", "prompt": "HOOK_PLAIN_CHILD_INPUT"})
		case 2:
			emitNativeTaskTool(w, "parent-after-plain-child", "Write", map[string]any{"file_path": filepath.Join(effects, "artifact-3.txt"), "content": "PHYSICAL_3"})
		default:
			emitPlanFixture(w, 3, "", "PARENT_AFTER_PLAIN_CHILD")
		}
	}))
	defer peer.Close()
	m := frontmatterHookMain(t, peer.URL, "parent-stop-failure", "slash", false, func(o *MainPrintOptions) { o.Arguments.PermissionMode = "bypassPermissions" })
	effects = filepath.Join(m.options.Native.CWD, "go-hook-effects")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := m.RunTurn(ctx, hooksPublicUser("/hookprobe")); err != nil {
		t.Fatal(err)
	}
	if child.Load() != 1 || parent.Load() != 3 {
		t.Fatal("ordinary child error retried or parent did not join", child.Load(), parent.Load())
	}
	if got := hookEffects(t, m); strings.Join(got, "\n") != "stop-failure" {
		t.Fatal("no-preset child missed parent Skill StopFailure", got)
	}
	checkHookInputs(t, m, 1)
	data, err := os.ReadFile(filepath.Join(effects, "inputs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var input protocol.JSONObject
	if err = input.UnmarshalJSON(bytes.TrimSpace(data)); err != nil {
		t.Fatal(err)
	}
	if input.String("hook_event_name") != "StopFailure" || input.String("agent_id") == "" || input.String("agent_type") != "plain-worker" || input.String("error") == "" || !strings.Contains(input.String("last_assistant_message"), "FAKE_RATE_LIMIT") {
		t.Fatal("ordinary child hook lost original API failure/agent identity", string(data))
	}
	var mainPath string
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var e error
		mainPath, e = m.Controller.Writer().ComputedTranscriptPath(scope)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if input.String("transcript_path") != mainPath {
		t.Fatal("StopFailure used child transcript instead of Source main path", string(data), mainPath)
	}
	outsideRoots, rootErr := os.ReadFile(filepath.Join(effects, "outside-roots"))
	if rootErr != nil || string(outsideRoots) != "<absent>\n" {
		t.Fatal("child outside executor changed Source Skill-root omission", string(outsideRoots), rootErr)
	}
	if m.hook.NativeAgentHookCount(m.Controller.SessionID()) != 1 || m.hook.NativeAgentHookCount(input.String("agent_id")) != 0 {
		t.Fatal("child finally removed parent Skill hooks or left child hooks")
	}
	artifact, err := os.ReadFile(filepath.Join(effects, "artifact-3.txt"))
	if err != nil || string(artifact) != "PHYSICAL_3" {
		t.Fatal("parent unusable after physical child failure/join", string(artifact), err)
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	if m.hook.NativeAgentHookCount(m.Controller.SessionID()) != 0 {
		t.Fatal("shutdown retained parent hooks")
	}
}
