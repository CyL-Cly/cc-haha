package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
)

func TestActualGuidePluginUTF16PhysicalModelRequestMatchesSource(t *testing.T) {
	for _, mode := range []string{"foreground", "background", "background-warm-resume", "workflow", "inprocess"} {
		t.Run(mode, func(t *testing.T) {
			actualGuideUTF16PhysicalRequest(t, mode)
		})
	}
}
func actualGuideUTF16PhysicalRequest(t *testing.T, mode string) {
	background := mode == "background" || mode == "background-warm-resume"
	resume := mode == "background-warm-resume"
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	f.write(t, filepath.Join(f.home, ".git", "HEAD"), "ref: refs/heads/private\n")
	f.write(t, filepath.Join(f.cwd, ".git", "HEAD"), "ref: refs/heads/private\n")
	dirs := []string{}
	for i, name := range []string{`"actual-high-\ud800"`, `"actual-low-\udc00"`} {
		dir := filepath.Join(f.home, fmt.Sprintf("inline-%d", i))
		dirs = append(dirs, dir)
		f.write(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name":`+name+`}`)
		f.write(t, filepath.Join(dir, "skills", "reader", "SKILL.md"), "---\ndescription: Actual guide raw description\n---\nNever execute.")
	}
	var mu sync.Mutex
	requests := []json.RawMessage{}
	childRequests := make(chan string, 2)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "fake-guide-utf16" {
			t.Error("actual fake credential not sent", r.Header.Get("X-Api-Key"))
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		if !bytes.Contains(body, []byte(`"messages"`)) {
			http.Error(w, "unexpected nonmodel", 500)
			return
		}
		mu.Lock()
		requests = append(requests, append(json.RawMessage(nil), body...))
		n := len(requests)
		mu.Unlock()
		if bytes.Contains(body, []byte("Private guide request")) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"owned-source-guide","type":"message","role":"assistant","content":[{"type":"text","text":"PRIVATE_SOURCE_GUIDE_OK"}],"model":"fixture-model","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
		} else if bytes.Contains(body, []byte("You are the Claude guide agent")) {
			childRequests <- string(body)
			emitPlanFixture(w, n, "", "PRIVATE_GUIDE_OK")
		} else if n == 1 {
			emitNativeTaskTool(w, "actual-guide-tool", "Agent", map[string]any{"subagent_type": "claude-code-guide", "description": "Actual guide request", "prompt": "Explain private plugin Skills without tools", "run_in_background": background})
		} else {
			emitPlanFixture(w, n, "", "PRIVATE_GUIDE_OK")
		}
	}))
	defer peer.Close()
	f.n.Environment["CLAUDE_CODE_ENTRYPOINT"] = "cli"
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	f.n.Environment["ANTHROPIC_API_KEY"] = "fake-guide-utf16"
	f.n.Environment["PATH"] = "/usr/bin:/bin"
	f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	disabled := false
	var diagnostic bytes.Buffer
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "Actual private guide parent", Tools: []string{"Agent"}, PluginDirectories: dirs, MaxTurns: 4, PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: &diagnostic})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	defer m.ShutdownSession(context.Background())
	switch mode {
	case "workflow":
		program, err := workflow.CompileBody(utf16.Encode([]rune(`return await agent('Private workflow guide request',{agentType:'claude-code-guide'})`)))
		if err != nil {
			t.Fatal(err)
		}
		result, err := workflow.Execute(ctx, workflow.ExecutionOptions{Program: program, RunID: "wf_guideUTF16", WorkflowName: "private-guide", Model: "fixture-model", Now: f.n.NowMillis, GetTurnSpent: func() float64 { return 0 }, RunAgent: m.runNativeWorkflowAgent, Emit: func(protocol.JSONObject) error { return nil }})
		if err != nil || result.Error != "" || string(result.Result) != `"PRIVATE_GUIDE_OK"` {
			t.Fatal("actual workflow guide child", err, result.Error, string(result.Result))
		}
	case "inprocess":
		var selected *agentcatalog.Definition
		if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			catalog, err := m.agents.List(scope)
			if err != nil {
				return err
			}
			for _, definition := range catalog.Active {
				if definition.Name == "claude-code-guide" {
					copy := definition
					selected = &copy
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if selected == nil {
			t.Fatal("actual CLI guide definition missing")
		}
		identity := sessioncontroller.InProcessTeammateIdentity{TeamName: "PrivateGuideTeam", AgentName: "guide", AgentID: "guide@PrivateGuideTeam", ParentSessionID: m.Controller.SessionID()}
		run, err := m.runNativeInProcessTurn(ctx, nativeInProcessTurn{Identity: identity, Definition: selected, Prompt: teamspawn.TeammateMessage("team-lead", "Private inprocess guide request", "", "raw"), PermissionMode: "bypassPermissions"})
		if err != nil || run.AgentID == "" || len(run.Messages) == 0 {
			t.Fatal("actual inprocess guide child", err, run.AgentID)
		}
	default:
		user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Use the actual guide Agent"})})
		if err = m.RunTurn(ctx, user); err != nil {
			t.Fatal(err, diagnostic.String())
		}
	}
	var goBody string
	select {
	case goBody = <-childRequests:
	case <-ctx.Done():
		t.Fatal("actual guide child did not request", ctx.Err())
	}
	if background {
		mu.Lock()
		agentID := ""
		for _, body := range requests {
			match := regexp.MustCompile(`agentId: ([a-zA-Z0-9-]+)`).FindStringSubmatch(string(body))
			if len(match) > 0 {
				agentID = match[1]
			}
		}
		mu.Unlock()
		if agentID == "" {
			t.Fatal("actual background tool response lacked identity")
		}
		join := func() {
			var task *sessioncontroller.BackgroundTask
			if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				var err error
				task, err = m.nativeBackgroundBindings().Lookup(scope, agentID)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if task == nil {
				t.Fatal("actual task producer lacked spawned guide")
			}
			if err := task.Join(ctx); err != nil {
				t.Fatal(err)
			}
		}
		join()
		if resume {
			response, err := m.SendAgentMessage(ctx, agentID, "Continue actual guide request after completed background task")
			if err != nil || response.String("delivery") != "resumed" {
				t.Fatal("actual guide resume", err, string(rawMain(response)))
			}
			select {
			case goBody = <-childRequests:
			case <-ctx.Done():
				t.Fatal("actual resumed guide did not request", ctx.Err())
			}
			join()
		}
	}
	fixture, _ := filepath.Abs("testdata/guide-prompt-utf16-source.fixture.ts")
	sandbox, _ := filepath.Abs("../../testdata/loopback-only.sb")
	child := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	child.Dir = f.home
	child.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_GUIDE_UTF16_FIXTURE=1", "ANTHROPIC_API_KEY=fake-guide-utf16", "ANTHROPIC_BASE_URL=" + peer.URL, "CLAUDE_CODE_ENTRYPOINT=cli", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file"}
	child.Stdin = bytes.NewReader(rawMain(map[string]any{"home": f.home, "cwd": f.cwd, "directories": dirs, "goRequestJSON": goBody}))
	var stderr bytes.Buffer
	child.Stderr = &stderr
	data, err := child.Output()
	if err != nil {
		t.Fatal("actual Source formatter/model transport", err, stderr.String())
	}
	var actual protocol.JSONObject
	if err = actual.UnmarshalJSON(data); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	sourceBody := ""
	sourceCount, guideCount, launchCount, notificationCount := 0, 0, 0, 0
	for index, body := range requests {
		switch {
		case bytes.Contains(body, []byte("Private guide request")):
			sourceCount++
			sourceBody = string(body)
		case bytes.Contains(body, []byte("You are the Claude guide agent")):
			guideCount++
		case index == 0:
			launchCount++
		default:
			// Actual background completion may add a parent query. It must retain
			// the real launching tool history; no unclassified producer is admitted.
			if !bytes.Contains(body, []byte("actual-guide-tool")) {
				mu.Unlock()
				t.Fatal("unclassified physical parent request", string(body))
			}
			if bytes.Contains(body, []byte("task-notification")) {
				notificationCount++
			}
		}
	}
	actualRequests := len(requests)
	mu.Unlock()
	wantGuideCount := 1
	if resume {
		wantGuideCount = 2
	}
	wantLaunchCount := 1
	if mode == "workflow" || mode == "inprocess" {
		wantLaunchCount = 0
	}
	if sourceCount != 1 || launchCount != wantLaunchCount || guideCount != wantGuideCount {
		t.Fatal("actual consumer request identities", sourceCount, launchCount, guideCount)
	}
	if background && notificationCount == 0 {
		t.Fatal("actual background guide completion was not consumed by parent")
	}
	// Feed the physical Source request body back to the same pinned JSON consumer.
	command := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", "-e", `const input=await Bun.stdin.json();const lines=(body)=>JSON.parse(body).system.flatMap(block=>(block.text??'').split('\n')).filter(line=>line.startsWith('- /actual-')).map(line=>Array.from({length:line.length},(_,i)=>line.charCodeAt(i)));process.stdout.write(JSON.stringify({sourceUnits:lines(input.source),goUnits:lines(input.go)}))`)
	command.Dir = f.home
	command.Env = child.Env
	command.Stdin = bytes.NewReader(rawMain(map[string]string{"source": sourceBody, "go": goBody}))
	units, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	var result struct{ SourceUnits, GoUnits any }
	if err = json.Unmarshal(units, &result); err != nil {
		t.Fatal(err)
	}
	evidence := rawMain(map[string]any{"actualSource": actual, "physicalGoChildRequestJSON": goBody, "physicalSourceRequestJSON": sourceBody, "finalJSONConsumer": json.RawMessage(units), "physicalRequests": actualRequests, "sourceRequests": sourceCount, "guideRequests": guideCount, "launchRequests": launchCount, "parentNotificationRequests": notificationCount, "mode": mode, "background": background, "resume": resume, "fakeLoopbackOnly": true})
	if dir := os.Getenv("CC_HAHA_GUIDE_EVIDENCE_DIR"); dir != "" {
		stage, _ := filepath.Abs("../../..")
		if dir != stage {
			t.Fatal("guide evidence escaped owned stage")
		}
		if err = os.WriteFile(filepath.Join(dir, "guide-utf16-physical-request-"+mode+".json"), append(evidence, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var expectedLines [][]uint16
	if err = json.Unmarshal(rawMain(result.SourceUnits), &expectedLines); err != nil || len(expectedLines) != 4 {
		t.Fatal("Source actual guide plugin sections absent", err, len(expectedLines))
	}
	if !reflect.DeepEqual(result.SourceUnits, result.GoUnits) {
		t.Fatalf("actual guide formatter → Agent → physical model request lost UTF16\nSource%s\nGo%s", rawMain(result.SourceUnits), rawMain(result.GoUnits))
	}
}
