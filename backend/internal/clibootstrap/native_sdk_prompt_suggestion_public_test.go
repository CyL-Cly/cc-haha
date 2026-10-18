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
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

// The public initialize/input/result path owns admission. The only model is a
// physical loopback peer. A Write request must reach the actual deny callback,
// preserve the advertised catalog, and continue to the next text response.
func TestActualSDKPromptSuggestionForkDeniesToolsAfterResultWithoutHistory(t *testing.T) {
	f := newNativePluginFixture(t)
	journal := filepath.Join(f.home, "suggestion-stop-hook.jsonl")
	f.write(t, filepath.Join(f.config, "settings.json"), string(rawMain(map[string]any{"enabledPlugins": map[string]bool{"sample@mkt": false}, "hooks": map[string]any{"SubagentStop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "IFS= read -r input; printf '%s\\n' \"$input\" >> \"$HOME/suggestion-stop-hook.jsonl\""}}}}}})))
	for key, value := range map[string]string{"ANTHROPIC_API_KEY": "fake-suggestion", "CLAUDE_CODE_ENTRYPOINT": "sdk-cli", "CLAUDE_CODE_DISABLE_FAST_MODE": "1", "CLAUDE_CODE_MAX_RETRIES": "0", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"} {
		f.n.Environment[key] = value
	}
	forbidden := filepath.Join(f.cwd, "suggestion-must-not-write.txt")
	var mu sync.Mutex
	var parentRequests, suggestionRequests []protocol.JSONObject
	var actualHTTPBodies []string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var request protocol.JSONObject
		if err = request.UnmarshalJSON(body); err != nil {
			t.Error(err)
			return
		}
		suggestion := bytes.Contains(body, []byte("[SUGGESTION MODE:"))
		mu.Lock()
		actualHTTPBodies = append(actualHTTPBodies, string(body))
		var round int
		if suggestion {
			suggestionRequests = append(suggestionRequests, request)
			round = len(suggestionRequests)
		} else {
			parentRequests = append(parentRequests, request)
			round = len(parentRequests)
		}
		mu.Unlock()
		if !suggestion {
			emitPlanFixture(w, round, "", fmt.Sprintf("PARENT_RESULT_%d", round))
			return
		}
		if round == 1 {
			emitNativeTaskTool(w, "suggestion-denied-write", "Write", map[string]any{"file_path": forbidden, "content": "MUST_NOT_WRITE"})
			return
		}
		if !bytes.Contains(body, []byte("No tools needed for suggestion")) {
			t.Error("actual tool denial was not consumed by the next model request")
		}
		emitPlanFixture(w, round, "", "run the tests")
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	results := make(chan struct{}, 4)
	suggestionReady := make(chan struct{}, 1)
	var history []byte
	inputPhase, historyAdmissionError := "created", ""
	var frames []protocol.JSONObject
	var diagnostics bytes.Buffer
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "SDK_SUGGESTION_CACHE_PREFIX", Tools: []string{"Write"}, MaxTurns: 6, PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: reader, Output: io.Discard, Error: &diagnostics, OnOutput: func(data []byte) error {
		var frame protocol.JSONObject
		if err := frame.UnmarshalJSON(data); err != nil {
			return err
		}
		mu.Lock()
		frames = append(frames, frame)
		mu.Unlock()
		if frame.String("type") == "result" {
			results <- struct{}{}
		}
		if frame.String("type") == "prompt_suggestion" {
			suggestionReady <- struct{}{}
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	inputDone := make(chan error, 1)
	setInputPhase := func(phase string) { mu.Lock(); inputPhase = phase; mu.Unlock() }
	progress := func() map[string]any {
		mu.Lock()
		defer mu.Unlock()
		types := []string{}
		for _, frame := range frames {
			types = append(types, frame.String("type"))
		}
		return map[string]any{"inputPhase": inputPhase, "historyAdmissionError": historyAdmissionError, "parentRequests": len(parentRequests), "suggestionRequests": len(suggestionRequests), "frameTypes": types}
	}
	go func() {
		// An input failure closes its actual pipe with that same error, rather
		// than leaving MainPrint.Run waiting until the outer deadline.
		finish := func(failure error) {
			if failure != nil {
				_ = writer.CloseWithError(failure)
			} else {
				failure = writer.Close()
			}
			inputDone <- failure
		}
		write := func(text string) error { _, err := io.WriteString(writer, text+"\n"); return err }
		setInputPhase("initialize-write")
		if err := write(`{"type":"control_request","request_id":"suggestion-init","request":{"subtype":"initialize","promptSuggestions":true}}`); err != nil {
			finish(err)
			return
		}
		for _, text := range []string{"PARENT_FIRST", "PARENT_SECOND"} {
			setInputPhase("write-" + text)
			if err := write(string(rawMain(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}}))); err != nil {
				finish(err)
				return
			}
			setInputPhase("wait-result-" + text)
			select {
			case <-results:
			case <-ctx.Done():
				finish(ctx.Err())
				return
			}
		}
		setInputPhase("wait-suggestion")
		select {
		case <-suggestionReady:
		case <-ctx.Done():
			finish(ctx.Err())
			return
		}
		setInputPhase("history-read-admission")
		// RunStream already owns the one ASK stack. This observational Read
		// is an allowed callback pumped on that same live Owner, not a new ASK.
		receipt, failure := m.Owner.Submit(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
			messages, err := m.Host.OriginalHistoryOnOwner(scope)
			if err != nil {
				return err
			}
			history = rawMain(messages)
			return nil
		})
		if failure == nil {
			setInputPhase("history-read-admitted")
			failure = receipt.Wait()
		}
		if failure != nil {
			mu.Lock()
			historyAdmissionError = failure.Error()
			mu.Unlock()
			finish(failure)
			return
		}
		setInputPhase("history-read-complete-before-EOF")
		finish(nil)
	}()
	if _, err = m.Run(ctx); err != nil {
		t.Fatal("actual initialize-to-suggestion", err, diagnostics.String(), string(rawMain(progress())))
	}
	if err = <-inputDone; err != nil {
		t.Fatal(err, string(rawMain(progress())))
	}
	t.Logf("actual_prompt_input_lifecycle=%s", rawMain(progress()))
	mu.Lock()
	actualFrames := append([]protocol.JSONObject{}, frames...)
	parents := append([]protocol.JSONObject{}, parentRequests...)
	suggestions := append([]protocol.JSONObject{}, suggestionRequests...)
	httpBodies := append([]string{}, actualHTTPBodies...)
	mu.Unlock()
	resultAt, suggestionAt, resultCount, suggestionCount := -1, -1, 0, 0
	for index, frame := range actualFrames {
		if frame.String("type") == "result" {
			resultAt = index
			resultCount++
			if string(frame.Get("is_error")) != "false" {
				t.Fatal("ordinary result failed", string(rawMain(frame)))
			}
		}
		if frame.String("type") == "prompt_suggestion" {
			suggestionAt = index
			suggestionCount++
			if frame.String("suggestion") != "run the tests" || frame.String("uuid") == "" || frame.String("session_id") != m.Controller.SessionID() {
				t.Fatal("SDK suggestion shape", string(rawMain(frame)))
			}
		}
	}
	if resultCount != 2 || suggestionCount != 1 || suggestionAt <= resultAt || len(parents) != 2 || len(suggestions) != 2 {
		t.Fatal("result-before-suggestion and actual requests", resultCount, suggestionCount, resultAt, suggestionAt, len(parents), len(suggestions))
	}
	if _, err = os.Stat(forbidden); !os.IsNotExist(err) {
		t.Fatal("deny callback permitted physical Write", err)
	}
	hookData, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal("actual fork SubagentStop missing", err)
	}
	hookLines := bytes.Split(bytes.TrimSpace(hookData), []byte("\n"))
	if len(hookLines) != 1 {
		t.Fatal("fork hook count", string(hookData))
	}
	var hookInput protocol.JSONObject
	if err = hookInput.UnmarshalJSON(hookLines[0]); err != nil {
		t.Fatal(err)
	}
	if hookInput.String("hook_event_name") != "SubagentStop" || hookInput.String("agent_id") == "" || !strings.HasSuffix(hookInput.String("agent_transcript_path"), "agent-"+hookInput.String("agent_id")+".jsonl") || hookInput.String("last_assistant_message") != "run the tests" {
		t.Fatal("temporary query context identity / no-transcript hook path", string(hookData))
	}
	if _, err = os.Stat(hookInput.String("agent_transcript_path")); !os.IsNotExist(err) {
		t.Fatal("skipTranscript fork hook path unexpectedly exists", err)
	}
	for _, request := range suggestions {
		for _, field := range []string{"tools", "model", "thinking", "max_tokens"} {
			if !bytes.Equal(request.Get(field), parents[1].Get(field)) {
				t.Fatalf("fork changed cache-key parameter %s parent=%s suggestion=%s", field, parents[1].Get(field), request.Get(field))
			}
		}
		parentSystem, err := sdkSuggestionComparableSystem(rawMain(parents[1]))
		if err != nil {
			t.Fatal(err)
		}
		suggestionSystem, err := sdkSuggestionComparableSystem(rawMain(request))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(parentSystem, suggestionSystem) {
			t.Fatalf("fork changed system beyond unique request billing CCH parent=%s suggestion=%s", parentSystem, suggestionSystem)
		}
		var messages []protocol.JSONObject
		if err = json.Unmarshal(request.Get("messages"), &messages); err != nil {
			t.Fatal(err)
		}
		var markers []int
		for index, message := range messages {
			if bytes.Contains(rawMain(message), []byte("cache_control")) {
				markers = append(markers, index)
			}
		}
		if len(markers) != 1 || markers[0] != len(messages)-2 {
			t.Fatal("skipCacheWrite shared-prefix cache marker", markers, len(messages))
		}
	}
	verifySDKSuggestionBillingWithActualSource(t, ctx, f.home, httpBodies)
	for _, marker := range []string{"SUGGESTION MODE", "run the tests", "suggestion-denied-write", "MUST_NOT_WRITE"} {
		if bytes.Contains(history, []byte(marker)) {
			t.Fatal("fork contaminated parent history", marker)
		}
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(f.config, "projects", "*", "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatal("parent transcript", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("PARENT_FIRST")) || !bytes.Contains(data, []byte("PARENT_SECOND")) {
		t.Fatal("ordinary transcript missing")
	}
	for _, marker := range []string{"SUGGESTION MODE", "run the tests", "suggestion-denied-write", "MUST_NOT_WRITE"} {
		if strings.Contains(string(data), marker) {
			t.Fatal("skipTranscript wrote fork to canonical transcript", marker)
		}
	}
	agents, err := filepath.Glob(filepath.Join(f.config, "projects", "*", "*", "subagents", "*.jsonl"))
	if err != nil || len(agents) != 0 {
		t.Fatal("skipTranscript created Agent transcript", agents, err)
	}
}

var sdkSuggestionCCH = regexp.MustCompile(`\bcch=[0-9a-f]{5};`)

// Source signs exactly one structured billing text field at final fetch. Only
// that field's request-body checksum may be normalized for cache comparison.
// The raw body/system bytes outside those five hex digits remain unchanged.
func sdkSuggestionUnsignedBilling(body []byte) ([]byte, []byte, error) {
	var request protocol.JSONObject
	if err := request.UnmarshalJSON(body); err != nil {
		return nil, nil, err
	}
	system := request.Get("system")
	var blocks []protocol.JSONObject
	if err := json.Unmarshal(system, &blocks); err != nil || len(blocks) == 0 {
		return nil, nil, fmt.Errorf("missing structured billing system")
	}
	billingCount := 0
	for _, block := range blocks {
		if block.String("type") == "text" && strings.HasPrefix(block.String("text"), "x-anthropic-billing-header:") {
			billingCount++
		}
	}
	first := blocks[0]
	if billingCount != 1 || first.String("type") != "text" || !strings.HasPrefix(first.String("text"), "x-anthropic-billing-header: ") || len(first.OwnProperties()) != 2 || first.Get("cache_control") != nil {
		return nil, nil, fmt.Errorf("unique uncached first billing text required")
	}
	text := first.String("text")
	matches := sdkSuggestionCCH.FindAllStringIndex(text, -1)
	if len(matches) != 1 {
		return nil, nil, fmt.Errorf("one exact five-hex billing CCH required")
	}
	token := []byte(text[matches[0][0]:matches[0][1]])
	originalJSON := first.Get("text")
	if bytes.Count(body, originalJSON) != 1 || bytes.Count(system, originalJSON) != 1 {
		return nil, nil, fmt.Errorf("billing field literal must occur exactly once")
	}
	if bytes.Count(originalJSON, token) != 1 {
		return nil, nil, fmt.Errorf("billing CCH must have its exact literal encoding")
	}
	unsignedJSON := bytes.Replace(originalJSON, token, []byte("cch=00000;"), 1)
	return bytes.Replace(body, originalJSON, unsignedJSON, 1), bytes.Replace(system, originalJSON, unsignedJSON, 1), nil
}
func sdkSuggestionComparableSystem(body []byte) ([]byte, error) {
	_, system, err := sdkSuggestionUnsignedBilling(body)
	return system, err
}

func verifySDKSuggestionBillingWithActualSource(t *testing.T, ctx context.Context, home string, bodies []string) {
	t.Helper()
	if len(bodies) != 4 {
		t.Fatal("actual parent/fork HTTP bodies", len(bodies))
	}
	// Negative controls cannot normalize missing/duplicate/nonmatching billing,
	// and an unrelated user CCH literal must remain byte-for-byte intact.
	valid := `{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=fixture; cch=abcde;"},{"type":"text","text":"cached prefix","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"literal cch=abcde;"}]}`
	for _, bad := range []string{
		`{}`, `{"system":null}`, `{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=fixture;"}]}`, `{"system":[{"type":"text","text":"unrelated cch=abcde;"}]}`,
		`{"system":[{"type":"text","text":"x-anthropic-billing-header: cch=abcde; cch=12345;"}]}`,
		`{"system":[{"type":"text","text":"x-anthropic-billing-header: cch=abcde;"},{"type":"text","text":"x-anthropic-billing-header: cch=12345;"}]}`,
		`{"system":[{"type":"text","text":"x-anthropic-billing-header: cch=abcde;","cache_control":{"type":"ephemeral"}}]}`,
		`{"system":[{"type":"text","text":"x-anthropic-billing-header: cch=abcdeg;"}]}`,
	} {
		if _, _, err := sdkSuggestionUnsignedBilling([]byte(bad)); err == nil {
			t.Fatal("invalid billing was normalized", bad)
		}
	}
	control, _, err := sdkSuggestionUnsignedBilling([]byte(valid))
	if err != nil || !bytes.Contains(control, []byte(`"content":"literal cch=abcde;"`)) || bytes.Count(control, []byte("cch=00000;")) != 1 {
		t.Fatal("normalization escaped unique billing field", string(control), err)
	}
	home, err = filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	unsigned := []string{}
	for _, body := range bodies {
		value, _, err := sdkSuggestionUnsignedBilling([]byte(body))
		if err != nil {
			t.Fatal("actual request billing", err)
		}
		unsigned = append(unsigned, string(value))
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual pinned Source Bun required for per-body CCH")
	}
	fixture := os.Getenv("CC_HAHA_PROMPT_SUGGESTION_SOURCE")
	if fixture != "" {
		fixture = filepath.Join(filepath.Dir(fixture), "native-sdk-prompt-suggestion-billing.fixture.ts")
	} else {
		fixture, _ = filepath.Abs("../../../scripts/quality-gate/go-parity/native-sdk-prompt-suggestion-billing.fixture.ts")
	}
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	command := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_SUGGESTION_BILLING_FIXTURE=1", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USER_TYPE=external", "ANTHROPIC_API_KEY=fake-billing", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(rawMain(unsigned))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	data, err := command.Output()
	if err != nil {
		t.Fatal("actual Source body signing/cache split", err, stderr.String())
	}
	var source struct {
		Signed []string
		Blocks [][]struct {
			Text       string
			CacheScope *string
		}
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(data, &source); err != nil || len(source.Signed) != len(bodies) || len(source.Blocks) != len(bodies) || len(source.Capabilities.Blocked) != 0 {
		t.Fatal("Source billing evidence", err, string(data))
	}
	for i, body := range bodies {
		if source.Signed[i] != body || provider.SignClaudeCodeCCHInString(unsigned[i]) != body {
			t.Fatal("per-body CCH diverged from original Source", i)
		}
		var request protocol.JSONObject
		_ = request.UnmarshalJSON([]byte(unsigned[i]))
		var system []protocol.JSONObject
		_ = json.Unmarshal(request.Get("system"), &system)
		if len(source.Blocks[i]) != len(system) {
			t.Fatal("Source split changed system block boundaries", i)
		}
		for j, block := range source.Blocks[i] {
			if block.Text != system[j].String("text") {
				t.Fatal("Source system text projection", i, j)
			}
			if j == 0 {
				if block.CacheScope != nil || system[j].Get("cache_control") != nil {
					t.Fatal("Source billing entered cache prefix", i)
				}
			} else {
				if block.CacheScope == nil || *block.CacheScope != "org" || string(system[j].Get("cache_control")) != `{"type":"ephemeral"}` {
					t.Fatal("Source cached prefix differs", i, j)
				}
			}
		}
	}
	// The exact signed peer bodies and original Source results survive TempDir
	// cleanup in the original Go JSON log; no mutable output fixture is a proof.
	t.Logf("actual_prompt_billing_evidence=%s", rawMain(map[string]any{"originalHTTPBodies": bodies, "unsignedHTTPBodies": unsigned, "sourceSignedHTTPBodies": source.Signed, "sourceCacheBlocks": source.Blocks, "sourceStdout": string(data)}))
}
