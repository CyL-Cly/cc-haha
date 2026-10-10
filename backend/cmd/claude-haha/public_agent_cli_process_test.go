package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualPublicCLISelectedAgentSourceReadEOF(t *testing.T) {
	auditPublicAgentInputs(t, "test-before")
	t.Cleanup(func() { auditPublicAgentInputs(t, "test-after-cleanup") })
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	binary := filepath.Join(root, "actual-public-agent")
	build := exec.CommandContext(ctx, "go", "build", "-p=4", "-race", "-tags=native_process_fixture", "-o", binary, ".")
	auditPublicAgentInputs(t, "child-build-before")
	out, buildError := build.CombinedOutput()
	auditPublicAgentInputs(t, "child-build-after")
	if buildError != nil {
		t.Fatal(buildError, string(out))
	}
	for _, profile := range []string{"project-reader", "initial-prompt", "initial-only", "initial-slash", "sdk-initial-blocks", "sdk-no-initialize", "sdk-settings-initial", "settings-nested-agent", "project-model", "explicit-model", "agent-effort", "cli-effort", "env-effort", "explicit-prompt", "settings-agent", "missing-agent", "guide-sdk", "guide-cli"} {
		kind := "project-reader"
		if strings.HasPrefix(profile, "guide-") {
			kind = "claude-code-guide"
		}
		if profile == "missing-agent" {
			kind = "private-missing-agent"
		}
		t.Run(profile, func(t *testing.T) {
			auditPublicAgentInputs(t, "profile-before")
			t.Cleanup(func() { auditPublicAgentInputs(t, "profile-after") })
			var mu sync.Mutex
			requests := []json.RawMessage{}
			activeWork := ""
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				if r.URL.Path != "/v1/messages" {
					http.Error(w, "no nonmodel in private peer", 404)
					return
				}
				mu.Lock()
				requests = append(requests, append(json.RawMessage(nil), data...))
				round := len(requests)
				work := activeWork
				mu.Unlock()
				if profile == "settings-nested-agent" {
					switch round {
					case 1:
						jsonSchemaCLIResponse(w, "actual-root-agent", "Agent", map[string]any{"description": "Private actual child reader", "prompt": "Read actual-agent-input.txt and report the marker", "subagent_type": "general-purpose"}, "")
						return
					case 2:
						jsonSchemaCLIResponse(w, "actual-child-read", "Read", map[string]any{"file_path": filepath.Join(work, "actual-agent-input.txt")}, "")
						return
					}
				}
				if round == 1 {
					jsonSchemaCLIResponse(w, "actual-agent-read", "Read", map[string]any{"file_path": filepath.Join(work, "actual-agent-input.txt")}, "")
					return
				}
				if profile == "sdk-initial-blocks" && round == 3 {
					for _, marker := range []string{"ACTUAL_AGENT_SKILL_BODY", "ACTUAL_AGENT_PRECEDING_BLOCK", "after-initial"} {
						if !bytes.Contains(data, []byte(marker)) {
							t.Errorf("actual SDK slash body omitted %s", marker)
						}
					}
				}
				if (profile != "settings-nested-agent" || round == 3) && !bytes.Contains(data, []byte("ACTUAL_PUBLIC_AGENT_PHYSICAL_READ")) {
					t.Error("real Read result did not reach next physical model request")
				}
				jsonSchemaCLIResponse(w, "actual-agent-done", "", nil, "ACTUAL_PUBLIC_AGENT_DONE")
			}))
			defer peer.Close()
			observations := map[string]any{}
			for _, runtime := range []string{"source", "go"} {
				home := filepath.Join(root, profile, runtime)
				work := filepath.Join(home, "work")
				tmp := filepath.Join(home, "tmp")
				for _, path := range []string{tmp, filepath.Join(work, ".git"), filepath.Join(work, ".claude", "agents")} {
					if err := os.MkdirAll(path, 0700); err != nil {
						t.Fatal(err)
					}
				}
				agentModel := "inherit"
				agentEffort := ""
				if profile == "project-model" || profile == "explicit-model" {
					agentModel = "fixture-agent-model"
				}
				if profile == "agent-effort" || profile == "cli-effort" {
					agentEffort = "effort: low\n"
				}
				if profile == "env-effort" {
					agentEffort = "effort: high\n"
				}
				if strings.HasPrefix(profile, "initial-") || strings.HasPrefix(profile, "sdk-") {
					agentEffort = "initialPrompt: ACTUAL_AGENT_INITIAL_PROMPT\n"
				}
				if profile == "initial-slash" {
					agentEffort = "initialPrompt: /plain ACTUAL_AGENT_INITIAL_PROMPT\n"
				}
				if err := os.WriteFile(filepath.Join(work, ".claude", "agents", "project-reader.md"), []byte("---\nname: project-reader\ndescription: Actual project reader\ntools: Bash\nmodel: "+agentModel+"\n"+agentEffort+"permissionMode: dontAsk\n---\nACTUAL_PROJECT_AGENT_SYSTEM_PROMPT. Read the physical file and report its marker."), 0600); err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(profile, "sdk-") || profile == "initial-slash" {
					if err := os.MkdirAll(filepath.Join(work, ".claude", "skills", "plain"), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(work, ".claude", "skills", "plain", "SKILL.md"), []byte("---\nname: plain\ndescription: Actual SDK plain skill\n---\nACTUAL_AGENT_SKILL_BODY $ARGUMENTS\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(work, "actual-agent-input.txt"), []byte("ACTUAL_PUBLIC_AGENT_PHYSICAL_READ\n17 29\n"), 0600); err != nil {
					t.Fatal(err)
				}
				rg, err := exec.LookPath("rg")
				if err != nil {
					t.Fatal(err)
				}
				rgBytes, err := os.ReadFile(rg)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(home, "private-rg"), rgBytes, 0700); err != nil {
					t.Fatal(err)
				}
				args := []string{"-p", "--agent", kind, "--model", "fixture-model", "--thinking", "disabled", "--tools", "Read", "--no-computer-use", "--no-chrome", "--output-format", "stream-json", "--verbose", "--setting-sources", "user,project,local", "--debug-file", filepath.Join(tmp, "actual-cli-debug.log"), "Use Read on actual-agent-input.txt then report the marker"}
				env := []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + tmp, "CLAUDE_CODE_TMPDIR=" + tmp, "CC_HAHA_NATIVE_FIXTURE_ROOT=" + root, "CC_HAHA_FIXTURE_TEMP_ROOT=" + root, "PATH=" + os.Getenv("PATH"), "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "CLAUDE_CODE_DISABLE_ATTACHMENTS=1", "CLAUDE_CODE_MAX_RETRIES=0", "ANTHROPIC_API_KEY=private-public-agent-fake", "ANTHROPIC_BASE_URL=" + peer.URL, "CC_HAHA_RIPGREP_PATH=" + filepath.Join(home, "private-rg"), "BUN_OPTIONS=--no-env-file"}
				if profile == "project-model" {
					for i := 0; i < len(args); i++ {
						if args[i] == "--model" {
							args = append(args[:i], args[i+2:]...)
							break
						}
					}
				}
				if profile == "agent-effort" {
					args = append(args[:len(args)-1], "--settings", `{"effortLevel":"high"}`, args[len(args)-1])
				}
				if profile == "cli-effort" {
					args = append(args[:len(args)-1], "--effort", "high", args[len(args)-1])
				}
				if profile == "env-effort" {
					env = append(env, "CLAUDE_CODE_EFFORT_LEVEL=medium")
				}
				if strings.Contains(profile, "effort") {
					env = append(env, "CLAUDE_CODE_ALWAYS_ENABLE_EFFORT=1")
				}
				if profile == "explicit-prompt" {
					args = append(args[:len(args)-1], "--system-prompt", "ACTUAL_EXPLICIT_CLI_PROMPT", args[len(args)-1])
				}
				if profile == "settings-agent" || profile == "settings-nested-agent" || profile == "sdk-settings-initial" {
					args = append(args[:1], args[3:]...)
					args = append(args[:len(args)-1], "--settings", `{"agent":"project-reader"}`, args[len(args)-1])
				}
				if profile == "guide-cli" {
					env = append(env, "CLAUDE_CODE_ENTRYPOINT=cli")
				}
				if strings.HasPrefix(profile, "sdk-") {
					args = append(args[:len(args)-1], "--input-format", "stream-json")
				}
				if profile == "initial-only" {
					args = args[:len(args)-1]
				}
				if profile == "settings-nested-agent" {
					for i, value := range args {
						if value == "--tools" {
							args[i+1] = "Agent,Read"
						}
					}
				}
				env = append(env, "ANTHROPIC_MODEL=fixture-default-model")
				command := exec.CommandContext(ctx, binary, args...)
				if runtime == "source" {
					fixture, _ := filepath.Abs("testdata/public-agent-cli.fixture.ts")
					input, _ := json.Marshal(map[string]any{"home": home, "cwd": work, "arguments": args})
					command = exec.CommandContext(ctx, bun, "--no-env-file", fixture)
					env = append(env, "CC_HAHA_PUBLIC_AGENT_CLI_FIXTURE=1", "CC_HAHA_PUBLIC_AGENT_CLI_INPUT="+string(input))
				}
				command.Dir = work
				if runtime == "source" {
					command.Dir = home
				}
				command.Env = env
				command.Stdin = strings.NewReader("")
				if profile == "sdk-no-initialize" {
					command.Stdin = strings.NewReader(`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"ACTUAL_AGENT_PRECEDING_BLOCK"},{"type":"text","text":"/plain no-initial"}]}}` + "\n")
				}
				var stdout, stderr bytes.Buffer
				command.Stdout = &stdout
				command.Stderr = &stderr
				mu.Lock()
				activeWork = work
				requests = nil
				mu.Unlock()
				auditPublicAgentInputs(t, runtime+"-process-before")
				if profile == "sdk-initial-blocks" || profile == "sdk-settings-initial" {
					err = runPublicAgentInitialStream(command, &stdout, &stderr, profile == "sdk-initial-blocks")
				} else {
					err = command.Run()
				}
				auditPublicAgentInputs(t, runtime+"-process-after")
				mu.Lock()
				observed := append([]json.RawMessage(nil), requests...)
				mu.Unlock()
				code := 0
				if err != nil {
					code = 1
					if e, ok := err.(*exec.ExitError); ok {
						code = e.ExitCode()
					}
				}
				observations[runtime] = map[string]any{"code": code, "requests": observed, "stdout": stdout.String(), "stderr": stderr.String(), "arguments": args}
				if runtime == "source" {
					for _, name := range []string{"original-public-agent-receipt.json", "tmp/actual-cli-debug.log"} {
						if data, e := os.ReadFile(filepath.Join(home, name)); e == nil {
							observations["source-"+name] = string(data)
						}
					}
				}

				evidence, _ := filepath.Abs("../../../evidence")
				if stat, e := os.Stat(evidence); e == nil && stat.IsDir() {
					data, _ := json.MarshalIndent(observations, "", "  ")
					_ = os.WriteFile(filepath.Join(evidence, "actual-public-agent-"+profile+"-observations.json"), data, 0600)
				}
				if (strings.HasPrefix(profile, "initial-") || profile == "sdk-initial-blocks") && len(observed) > 0 && !bytes.Contains(observed[0], []byte("ACTUAL_AGENT_INITIAL_PROMPT")) {
					t.Fatal("actual CLI omitted selected Agent initialPrompt", runtime)
				}
				if profile == "initial-slash" && len(observed) > 0 {
					for _, marker := range []string{"ACTUAL_AGENT_SKILL_BODY", "ACTUAL_AGENT_INITIAL_PROMPT", "Use Read on actual-agent-input.txt"} {
						if !bytes.Contains(observed[0], []byte(marker)) {
							t.Fatal("actual initial slash lost body/trailing input", runtime, marker)
						}
					}
				}
				if (profile == "sdk-no-initialize" || profile == "sdk-settings-initial") && len(observed) > 0 && bytes.Contains(observed[0], []byte("ACTUAL_AGENT_INITIAL_PROMPT")) {
					t.Fatal("SDK applied initialPrompt without original CLI initialize selection", runtime)
				}
				if profile == "sdk-initial-blocks" || profile == "sdk-settings-initial" {
					ack, init := -1, -1
					for i, line := range strings.Split(stdout.String(), "\n") {
						var row protocol.JSONObject
						if row.UnmarshalJSON([]byte(line)) != nil {
							continue
						}
						if response, ok := row.Object("response"); ok && row.String("type") == "control_response" && response.String("request_id") == "actual-agent-init" {
							ack = i
						}
						if init < 0 && row.String("type") == "system" && row.String("subtype") == "init" {
							init = i
						}
					}
					if ack < 0 || init < 0 || ack >= init {
						t.Fatalf("actual initialize ACK must precede initial ASK system/init: %s ack%d init%d", runtime, ack, init)
					}
					if len(observed) > 0 && (!bytes.Contains(observed[0], []byte("ACTUAL_SDK_INIT_SYSTEM_PROMPT")) || bytes.Contains(observed[0], []byte("ACTUAL_PROJECT_AGENT_SYSTEM_PROMPT"))) {
						t.Fatal("SDK initialize prompt must reach the first initial ASK", runtime)
					}
				}
				expectedCalls := 2
				if profile == "settings-nested-agent" {
					expectedCalls = 4
				}
				if profile == "sdk-initial-blocks" {
					expectedCalls = 3
				}
				if code != 0 || len(observed) != expectedCalls || !strings.Contains(stdout.String(), "ACTUAL_PUBLIC_AGENT_DONE") {
					t.Fatalf("actual %s public --agent %s failed: code%d requests%d stdout %.2500s stderr %.2500s", runtime, kind, code, len(observed), stdout.String(), stderr.String())
				}
				if runtime == "source" {
					receipt, err := os.ReadFile(filepath.Join(home, "original-public-agent-receipt.json"))
					if err != nil {
						t.Fatal(err)
					}
					var record protocol.JSONObject
					_ = record.UnmarshalJSON(receipt)
					capability, _ := record.Object("capabilities")
					if string(capability.Get("blocked")) != "[]" {
						t.Fatal("Source escaped owned fixture", string(capability.Get("blocked")))
					}
					if kind == "project-reader" && record.String("agentType") != kind {
						t.Fatal("original main did not resolve selected agent", string(receipt))
					}
					if kind == "project-reader" && profile != "explicit-prompt" && profile != "sdk-initial-blocks" && profile != "sdk-settings-initial" && !bytes.Contains(observed[0], []byte("ACTUAL_PROJECT_AGENT_SYSTEM_PROMPT")) {
						t.Fatal("original public project agent prompt not applied")
					}
					if profile == "guide-cli" && record.String("agentType") != "claude-code-guide" {
						t.Fatal("explicit cli entrypoint did not select builtin Guide")
					}
					if profile == "guide-sdk" && record.String("agentType") != "" {
						t.Fatal("SDK entrypoint incorrectly selected excluded builtin Guide")
					}
					if kind == "claude-code-guide" && bytes.Contains(observed[0], []byte("You are the Claude guide agent")) {
						t.Fatal("public print applied a builtin child prompt contrary to original main caller")
					}
				} else if kind == "project-reader" && profile != "explicit-prompt" && profile != "sdk-initial-blocks" && profile != "sdk-settings-initial" && !bytes.Contains(observed[0], []byte("ACTUAL_PROJECT_AGENT_SYSTEM_PROMPT")) {
					t.Fatal("selected project prompt lost before model")
				}

			}
			var sourceRows, goRows []protocol.JSONObject
			source := observations["source"].(map[string]any)
			goResult := observations["go"].(map[string]any)
			data, _ := json.Marshal(source["requests"])
			_ = json.Unmarshal(data, &sourceRows)
			data, _ = json.Marshal(goResult["requests"])
			_ = json.Unmarshal(data, &goRows)
			for i := range sourceRows {
				if sourceRows[i].String("model") != goRows[i].String("model") {
					t.Fatal("actual selected request model differs", sourceRows[i].String("model"), goRows[i].String("model"))
				}
				a, _ := sourceRows[i].Object("output_config")
				b, _ := goRows[i].Object("output_config")
				if string(a.Get("effort")) != string(b.Get("effort")) {
					t.Fatal("actual effort precedence differs", string(a.Get("effort")), string(b.Get("effort")))
				}
				names := func(row protocol.JSONObject) string {
					var tools []protocol.JSONObject
					_ = json.Unmarshal(row.Get("tools"), &tools)
					out := []string{}
					for _, tool := range tools {
						out = append(out, tool.String("name"))
					}
					return strings.Join(out, ",")
				}
				if profile != "settings-nested-agent" && (names(sourceRows[i]) != names(goRows[i]) || names(goRows[i]) != "Read") {
					t.Fatal("main --agent imposed child-only tool rules", names(sourceRows[i]), names(goRows[i]))
				}
			}
			if profile == "explicit-prompt" {
				for _, row := range []protocol.JSONObject{sourceRows[0], goRows[0]} {
					if !bytes.Contains(row.Get("system"), []byte("ACTUAL_EXPLICIT_CLI_PROMPT")) || bytes.Contains(row.Get("system"), []byte("ACTUAL_PROJECT_AGENT_SYSTEM_PROMPT")) {
						t.Fatal("explicit CLI prompt lost priority")
					}
				}
			}
		})
	}
}

func TestActualPublicCLISelectedAgentRestoresOriginalMetadataHooksAndNextASK(t *testing.T) {
	auditPublicAgentInputs(t, "test-before")
	t.Cleanup(func() { auditPublicAgentInputs(t, "test-after-cleanup") })
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	binary := filepath.Join(root, "actual-public-agent-restore")
	build := exec.CommandContext(ctx, "go", "build", "-race", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	auditPublicAgentInputs(t, "child-build-before")
	data, buildError := build.CombinedOutput()
	auditPublicAgentInputs(t, "child-build-after")
	if buildError != nil {
		t.Fatal(buildError, string(data))
	}
	observations := map[string]any{}
	for _, runtime := range []string{"source", "go"} {
		home := filepath.Join(root, runtime)
		work := filepath.Join(home, "work")
		tmp := filepath.Join(home, "tmp")
		config := filepath.Join(home, ".claude")
		for _, dir := range []string{tmp, config, filepath.Join(work, ".git"), filepath.Join(work, ".claude", "agents")} {
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
		}
		rg, err := exec.LookPath("rg")
		if err != nil {
			t.Fatal(err)
		}
		rgBytes, err := os.ReadFile(rg)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(home, "private-rg"), rgBytes, 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(work, "actual-agent-input.txt"), []byte("ACTUAL_PUBLIC_AGENT_RESTORE_READ\n17 29\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(work, ".claude", "agents", "project-reader.md"), []byte("---\nname: project-reader\ndescription: Actual restore reader\ntools: Bash\nmodel: fixture-agent-model\ninitialPrompt: ACTUAL_AGENT_RESTORE_INITIAL\n---\nACTUAL_PROJECT_AGENT_RESTORE_PROMPT. Read and retain old history."), 0600); err != nil {
			t.Fatal(err)
		}
		var mu sync.Mutex
		requests := []json.RawMessage{}
		hooks := []protocol.JSONObject{}
		peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			data, _ := io.ReadAll(r.Body)
			if r.URL.Path == "/agent-hook" {
				var row protocol.JSONObject
				if err := row.UnmarshalJSON(data); err != nil {
					t.Error(err)
				}
				mu.Lock()
				hooks = append(hooks, row)
				mu.Unlock()
				w.Header().Set("content-type", "application/json")
				_, _ = io.WriteString(w, "{}")
				return
			}
			if r.URL.Path != "/v1/messages" {
				http.Error(w, "no outside endpoint", 404)
				return
			}
			mu.Lock()
			requests = append(requests, append(json.RawMessage(nil), data...))
			round := len(requests)
			mu.Unlock()
			if round%2 == 1 {
				jsonSchemaCLIResponse(w, "actual-resume-read", "Read", map[string]any{"file_path": filepath.Join(work, "actual-agent-input.txt")}, "")
				return
			}
			if !bytes.Contains(data, []byte("ACTUAL_PUBLIC_AGENT_RESTORE_READ")) {
				t.Error("restore real Read not in next request")
			}
			jsonSchemaCLIResponse(w, "actual-resume-done", "", nil, "ACTUAL_PUBLIC_AGENT_RESTORE_DONE")
		}))
		startHookCommand := "/bin/cat >> '" + filepath.Join(home, "actual-start-hooks.jsonl") + "'; /usr/bin/printf '\\n' >> '" + filepath.Join(home, "actual-start-hooks.jsonl") + "'; /usr/bin/printf '%s' '{}'"
		defer peer.Close()
		settings := map[string]any{"futurePrivateSettings": map[string]any{"retain": "yes"}, "hooks": map[string]any{}}
		for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Stop"} {
			matcher := map[string]any{"hooks": []any{map[string]any{"type": "http", "url": peer.URL + "/agent-hook"}}}
			if event == "SessionStart" {
				matcher["matcher"] = "startup|resume"
				matcher["hooks"] = []any{map[string]any{"type": "command", "command": startHookCommand}}
			}
			if event == "PreToolUse" || event == "PostToolUse" {
				matcher["matcher"] = "Read"
			}
			settings["hooks"].(map[string]any)[event] = []any{matcher}
		}
		settingsBytes, _ := json.Marshal(settings)
		if err = os.WriteFile(filepath.Join(config, "settings.json"), settingsBytes, 0600); err != nil {
			t.Fatal(err)
		}
		sid := "dd532fb7-c9e0-48ad-962e-d24e45bb803f"
		for _, phase := range []string{"fresh", "resume"} {
			args := []string{"-p", "--thinking", "disabled", "--tools", "Read", "--no-computer-use", "--no-chrome", "--output-format", "stream-json", "--verbose", "--setting-sources", "user,project,local"}
			if phase == "fresh" {
				args = append(args, "--agent", "project-reader", "--session-id", sid)
			} else {
				args = append(args, "--resume", sid)
			}
			args = append(args, "--debug-file", filepath.Join(tmp, "restore-debug.log"), "Read actual-agent-input.txt and retain previous selected Agent history")
			env := []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + tmp, "CLAUDE_CODE_TMPDIR=" + tmp, "CC_HAHA_NATIVE_FIXTURE_ROOT=" + root, "CC_HAHA_FIXTURE_TEMP_ROOT=" + root, "PATH=" + os.Getenv("PATH"), "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "CLAUDE_CODE_DISABLE_ATTACHMENTS=1", "CLAUDE_CODE_MAX_RETRIES=0", "ANTHROPIC_API_KEY=private-public-agent-fake", "ANTHROPIC_BASE_URL=" + peer.URL, "ANTHROPIC_MODEL=fixture-default-model", "CC_HAHA_RIPGREP_PATH=" + filepath.Join(home, "private-rg"), "BUN_OPTIONS=--no-env-file"}
			command := exec.CommandContext(ctx, binary, args...)
			command.Dir = work
			if runtime == "source" {
				fixture, _ := filepath.Abs("testdata/public-agent-cli.fixture.ts")
				input, _ := json.Marshal(map[string]any{"home": home, "cwd": work, "arguments": args, "startHookCommand": startHookCommand})
				command = exec.CommandContext(ctx, bun, "--no-env-file", fixture)
				command.Dir = home
				env = append(env, "CC_HAHA_PUBLIC_AGENT_CLI_FIXTURE=1", "CC_HAHA_PUBLIC_AGENT_CLI_INPUT="+string(input))
			}
			command.Env = env
			command.Stdin = strings.NewReader("")
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			auditPublicAgentInputs(t, runtime+"-"+phase+"-process-before")
			processError := command.Run()
			auditPublicAgentInputs(t, runtime+"-"+phase+"-process-after")
			if processError != nil {
				t.Fatalf("actual %s %s selected Agent CLI %v stdout %.1500s stderr %.1500s", runtime, phase, processError, stdout.String(), stderr.String())
			}
			mu.Lock()
			latestRequest := append(json.RawMessage(nil), requests[len(requests)-1]...)
			mu.Unlock()
			if bytes.Count(latestRequest, []byte("ACTUAL_AGENT_RESTORE_INITIAL")) != 1 {
				t.Fatal("actual restored selection repeated or omitted startup initialPrompt", runtime, phase)
			}
			if !strings.Contains(stdout.String(), "ACTUAL_PUBLIC_AGENT_RESTORE_DONE") {
				t.Fatal("actual restore result missing")
			}
			if runtime == "source" {
				data, err := os.ReadFile(filepath.Join(home, "original-public-agent-receipt.json"))
				if err != nil {
					t.Fatal(err)
				}
				var receipt protocol.JSONObject
				_ = receipt.UnmarshalJSON(data)
				caps, _ := receipt.Object("capabilities")
				if string(caps.Get("blocked")) != "[]" {
					t.Fatal("Source capability escape", string(caps.Get("blocked")))
				}
			}
			path := ""
			_ = filepath.WalkDir(filepath.Join(config, "projects"), func(p string, d os.DirEntry, e error) error {
				if e == nil && !d.IsDir() && d.Name() == sid+".jsonl" {
					path = p
				}
				return nil
			})
			if path == "" {
				t.Fatal("actual persisted SID missing", runtime, phase)
			}
			transcript, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(transcript, []byte(`"agentSetting":"project-reader"`)) {
				t.Fatal("actual original agent-setting not materialized", runtime, phase)
			}
			if phase == "fresh" {
				rows := bytes.Split(transcript, []byte("\n"))
				added := false
				for i, line := range rows {
					var row protocol.JSONObject
					if row.UnmarshalJSON(line) == nil && row.String("type") == "user" && !added {
						row.Set("futurePrivateAgentHistory", json.RawMessage(`{"retain":true,"raw":"\ud800"}`))
						rows[i], _ = row.MarshalJSON()
						added = true
					}
				}
				if !added {
					t.Fatal("no actual old user record")
				}
				if err = os.WriteFile(path, bytes.Join(rows, []byte("\n")), 0600); err != nil {
					t.Fatal(err)
				}
			} else if !bytes.Contains(transcript, []byte(`"futurePrivateAgentHistory":{"retain":true,"raw":"\ud800"}`)) {
				t.Fatal("old unknown history field lost", runtime)
			}
		}
		if runtime == "source" {
			debugData, _ := os.ReadFile(filepath.Join(tmp, "restore-debug.log"))
			receiptData, _ := os.ReadFile(filepath.Join(home, "original-public-agent-receipt.json"))
			evidence, _ := filepath.Abs("../../../evidence")
			if stat, e := os.Stat(evidence); e == nil && stat.IsDir() {
				_ = os.WriteFile(filepath.Join(evidence, "actual-startup-source-debug.log"), debugData, 0600)
				_ = os.WriteFile(filepath.Join(evidence, "actual-startup-source-receipt.json"), receiptData, 0600)
			}
		}
		startData, err := os.ReadFile(filepath.Join(home, "actual-start-hooks.jsonl"))
		if err != nil {
			t.Fatal("actual startup hook input", runtime, err)
		}
		for _, line := range bytes.Split(startData, []byte("\n")) {
			var row protocol.JSONObject
			if row.UnmarshalJSON(line) == nil {
				hooks = append(hooks, row)
			}
		}
		peer.Close()
		mu.Lock()
		captured := append([]json.RawMessage(nil), requests...)
		capturedHooks := append([]protocol.JSONObject(nil), hooks...)
		mu.Unlock()
		observations[runtime] = map[string]any{"requests": captured, "hooks": capturedHooks}
		if runtime == "source" {
			for _, name := range []string{"original-public-agent-receipt.json", "tmp/restore-debug.log"} {
				if data, e := os.ReadFile(filepath.Join(home, name)); e == nil {
					observations["source-"+name] = string(data)
				}
			}
		}
		evidence, _ := filepath.Abs("../../../evidence")
		if stat, e := os.Stat(evidence); e == nil && stat.IsDir() {
			data, _ := json.MarshalIndent(observations, "", "  ")
			_ = os.WriteFile(filepath.Join(evidence, "actual-public-agent-restore-hooks-observations.json"), data, 0600)
		}

		if len(captured) != 4 {
			t.Fatal("actual restore request count", runtime, len(captured))
		}
		for _, raw := range captured {
			var request protocol.JSONObject
			_ = request.UnmarshalJSON(raw)
			if request.String("model") != "fixture-agent-model" || !bytes.Contains(request.Get("system"), []byte("ACTUAL_PROJECT_AGENT_RESTORE_PROMPT")) {
				t.Fatal("restored prompt/model not captured at actual request", runtime, request.String("model"))
			}
		}
		events := map[string][]string{}
		for _, row := range capturedHooks {
			events[row.String("hook_event_name")] = append(events[row.String("hook_event_name")], row.String("agent_type"))
		}
		for _, event := range []string{"UserPromptSubmit", "PreToolUse", "PostToolUse", "Stop"} {
			if len(events[event]) != 2 || events[event][0] != "project-reader" || events[event][1] != "project-reader" {
				t.Fatal("actual main-thread hook agent_type", runtime, event, events[event])
			}
		}
		if len(events["SessionStart"]) != 2 || events["SessionStart"][0] != "project-reader" || events["SessionStart"][1] != "" {
			t.Fatal("Source startup/resume publication timing", runtime, events["SessionStart"])
		}
		data, err := os.ReadFile(filepath.Join(config, "settings.json"))
		if err != nil || !bytes.Equal(data, settingsBytes) {
			t.Fatal("unknown settings mutated", runtime, err)
		}
	}
	evidence, _ := filepath.Abs("../../../evidence")
	if stat, e := os.Stat(evidence); e == nil && stat.IsDir() {
		data, _ := json.MarshalIndent(observations, "", "  ")
		_ = os.WriteFile(filepath.Join(evidence, "actual-public-agent-restore-hooks-observations.json"), data, 0600)
	}
}

// Initialization is the SDK producer. Wait for its first actual result before
// writing the original content-block slash input, then close the physical pipe.
func runPublicAgentInitialStream(cmd *exec.Cmd, stdout, stderr *bytes.Buffer, expectInitial bool) error {
	cmd.Stdin = nil
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stream := &schemaCLIStreamWriter{results: make(chan struct{}, 2)}
	cmd.Stdout = stream
	cmd.Stderr = stderr
	if err = cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	inputs := []string{
		`{"type":"control_request","request_id":"actual-agent-init","request":{"subtype":"initialize","systemPrompt":"ACTUAL_SDK_INIT_SYSTEM_PROMPT"}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"ACTUAL_AGENT_PRECEDING_BLOCK"},{"type":"text","text":"/plain after-initial"}]}}`,
	}
	for i, line := range inputs {
		if _, err = io.WriteString(in, line+"\n"); err != nil {
			in.Close()
			<-done
			stdout.Write(stream.all.Bytes())
			return err
		}
		if i == 0 && !expectInitial {
			continue
		}
		select {
		case <-stream.results:
		case err = <-done:
			stdout.Write(stream.all.Bytes())
			return fmt.Errorf("SDK initial producer exited before actual result: %w", err)
		}
	}
	in.Close()
	err = <-done
	stdout.Write(stream.all.Bytes())
	return err
}

// Optional maintainer evidence records physical inputs around each process and
// temporary-directory cleanup. Normal repository tests have no artifact input.
func auditPublicAgentInputs(t *testing.T, phase string) {
	t.Helper()
	path := os.Getenv("CC_HAHA_PUBLIC_AGENT_INPUT_MANIFEST")
	if path == "" {
		return
	}
	var expected struct {
		Root  string            `json:"root"`
		Files map[string]string `json:"files"`
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &expected); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(expected.Files))
	for key := range expected.Files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hash := sha256.New()
	missing, changed := []string{}, []string{}
	for _, key := range keys {
		body, e := os.ReadFile(filepath.Join(expected.Root, key))
		if e != nil {
			missing = append(missing, key)
			continue
		}
		actual := fmt.Sprintf("%x", sha256.Sum256(body))
		if actual != expected.Files[key] {
			changed = append(changed, key)
		}
		fmt.Fprintf(hash, "%s\x00%s\n", key, actual)
	}
	record, err := json.Marshal(map[string]any{"at": time.Now().UTC().Format(time.RFC3339Nano), "test": t.Name(), "phase": phase, "count": len(keys), "digest": fmt.Sprintf("%x", hash.Sum(nil)), "missing": missing, "changed": changed})
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(filepath.Dir(path), "public-agent-input-events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.Write(append(record, '\n'))
	closeError := file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeError != nil {
		t.Fatal(closeError)
	}
	if len(missing) > 0 || len(changed) > 0 {
		t.Fatalf("physical public CLI inputs changed at %s: missing%v changed%v", phase, missing, changed)
	}
}
