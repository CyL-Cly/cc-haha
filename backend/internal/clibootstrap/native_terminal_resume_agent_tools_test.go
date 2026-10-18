package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// This exercises the original public idle-REPL resume consumer and captures
// actual loopback Messages requests. It does not call a tool-filter helper or
// inject a definition, prepared history, task, or Host map into the live graph.
func TestActualTerminalResumeReappliesAgentToolsAndRestoresPlainTools(t *testing.T) {
	const (
		readerSeed = "RESUME_TOOLS_READER_SEED_6084"
		denySeed   = "RESUME_TOOLS_DENY_SEED_6084"
		plainSeed  = "RESUME_TOOLS_PLAIN_SEED_6084"
		baseline   = "RESUME_TOOLS_LIVE_BASELINE_6084"
		readerAsk  = "RESUME_TOOLS_READER_ASK_6084"
		denyAsk    = "RESUME_TOOLS_DENY_ASK_6084"
		plainAsk   = "RESUME_TOOLS_PLAIN_ASK_6084"
	)
	labels := []string{readerSeed, denySeed, plainSeed, baseline, readerAsk, denyAsk, plainAsk}
	type observedRequest struct {
		label string
		tools []string
		body  []byte
	}
	var mu sync.Mutex
	var requests []observedRequest
	var peerErrors []string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		data, err := io.ReadAll(r.Body)
		var body struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		if err == nil {
			err = json.Unmarshal(data, &body)
		}
		if err != nil {
			mu.Lock()
			peerErrors = append(peerErrors, err.Error())
			mu.Unlock()
			http.Error(w, "invalid private request", http.StatusBadRequest)
			return
		}
		label := ""
		for _, message := range body.Messages {
			if message.Role != "user" {
				continue
			}
			for _, candidate := range labels {
				if bytes.Contains(message.Content, []byte(candidate)) {
					label = candidate
				}
			}
		}
		names := []string{}
		for _, tool := range body.Tools {
			names = append(names, tool.Name)
		}
		sort.Strings(names)
		mu.Lock()
		requests = append(requests, observedRequest{label: label, tools: names, body: append([]byte(nil), data...)})
		mu.Unlock()
		emitPlanFixture(w, 1, "", "RESUME_TOOLS_PRIVATE_ANSWER_6084")
	}))
	t.Cleanup(peer.Close)
	f := newNativePluginFixture(t)
	// Match the existing main-thread Agent consumer's private executable and
	// release fixtures; do not discover or use the caller's real config.
	rg, err := exec.LookPath("rg")
	if err != nil {
		t.Fatal(err)
	}
	rgBytes, err := os.ReadFile(rg)
	if err != nil {
		t.Fatal(err)
	}
	rgPath := filepath.Join(f.home, "resume-tools-private-rg")
	if err = os.WriteFile(rgPath, rgBytes, 0700); err != nil {
		t.Fatal(err)
	}
	f.n.Environment["CC_HAHA_RIPGREP_PATH"] = rgPath
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	f.n.Environment["ANTHROPIC_API_KEY"] = "private-only-loopback-resume-tools"
	f.n.Environment["ENABLE_TOOL_SEARCH"] = "false"
	f.n.Environment["CLAUDE_CODE_DISABLE_FAST_MODE"] = "1"
	f.write(t, filepath.Join(f.config, "settings.json"), `{"disableAllHooks":true,"enabledPlugins":{"sample@mkt":false}}`)
	f.write(t, filepath.Join(f.cwd, ".git", "HEAD"), "ref: refs/heads/private\n")
	f.write(t, filepath.Join(f.cwd, ".claude", "agents", "resume-reader.md"), "---\nname: resume-reader\ndescription: Private read-only resumed role\ntools: Read\ndisallowedTools: Write\nmodel: inherit\n---\nRESUME_READER_PROMPT_6084")
	f.write(t, filepath.Join(f.cwd, ".claude", "agents", "resume-deny.md"), "---\nname: resume-deny\ndescription: Private resumed wildcard role\ntools: '*'\ndisallowedTools: Write\nmodel: inherit\n---\nRESUME_DENY_PROMPT_6084")
	seedNativeReleaseWarmFixture(t, f, "fixture")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	turn := func(label string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": label})})
	}
	newMain := func(agent, sid string, interactive bool) (*MainPrint, func()) {
		t.Helper()
		disabled := false
		m, err := NewMainPrint(MainPrintOptions{
			Native: f.n.agentScopedBoundary(f.cwd), PID: os.Getpid(), Version: "fixture", Platform: "darwin", Interactive: interactive, ForegroundShellProgress: interactive,
			Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard,
			Arguments: PrintArguments{ComputerUse: &disabled, Agent: agent, SessionID: sid, Model: "fixture-model", Tools: []string{"Bash", "Read", "Write"}, PermissionMode: "bypassPermissions", MaxTurns: 8, Sources: DefaultState()},
		})
		if err != nil {
			t.Fatal(err)
		}
		closed := false
		closeMain := func() {
			if closed {
				return
			}
			closeCtx, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer closeCancel()
			if err := m.ShutdownSession(closeCtx); err != nil {
				t.Error("private MainPrint shutdown", err)
			}
			closed = true
		}
		t.Cleanup(closeMain)
		if err = m.StartSession(ctx); err != nil {
			t.Fatal(err)
		}
		return m, closeMain
	}
	seed := func(agent, sid, label string) {
		t.Helper()
		m, closeMain := newMain(agent, sid, false)
		if err := m.RunTurn(ctx, turn(label)); err != nil {
			t.Fatal("actual saved session", err)
		}
		var physical string
		if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			var err error
			physical, err = m.Controller.Writer().ComputedTranscriptPath(scope)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		closeMain()
		data, err := os.ReadFile(physical)
		if err != nil || !bytes.Contains(data, []byte(label)) {
			t.Fatal("actual saved transcript absent", err, string(data))
		}
		if agent != "" && (!bytes.Contains(data, []byte(`"agentSetting":"`+agent+`"`))) {
			t.Fatal("actual selected Agent metadata not persisted", string(data))
		}
	}
	const readerSID = "71717171-7171-4171-8171-717171717171"
	const denySID = "72727272-7272-4272-8272-727272727272"
	const plainSID = "73737373-7373-4373-8373-737373737373"
	seed("resume-reader", readerSID, readerSeed)
	seed("resume-deny", denySID, denySeed)
	seed("", plainSID, plainSeed)
	m, _ := newMain("", "74747474-7474-4474-8474-747474747474", true)
	if err := m.RunTurn(ctx, turn(baseline)); err != nil {
		t.Fatal(err)
	}
	resume := func(sid, label string) {
		t.Helper()
		selected, message, err := m.ResolveTerminalResumeArgument(ctx, sid)
		if err != nil || selected == nil || message != "" {
			t.Fatal("actual physical resume selection", sid, message, err)
		}
		result, err := m.ResumeTerminalSelection(ctx, selected)
		if err != nil || result.String("sessionId") != sid {
			t.Fatal("actual idle-REPL resume", err, string(rawMain(result)))
		}
		if err = m.RunTurn(ctx, turn(label)); err != nil {
			t.Fatal("actual next resumed ASK", err)
		}
	}
	resume(readerSID, readerAsk)
	resume(denySID, denyAsk)
	resume(plainSID, plainAsk)
	mu.Lock()
	got := append([]observedRequest(nil), requests...)
	failures := append([]string(nil), peerErrors...)
	mu.Unlock()
	if len(failures) != 0 {
		t.Fatal("private peer decode failures", failures)
	}
	if len(got) != len(labels) {
		t.Errorf("total actual ASK requests=%d want=%d; unknown or duplicate requests are forbidden", len(got), len(labels))
	}
	wantAll := []string{"Bash", "Read", "Write"}
	want := map[string][]string{
		readerSeed: wantAll, denySeed: wantAll, plainSeed: wantAll,
		baseline: wantAll, readerAsk: {"Read"}, denyAsk: {"Bash", "Read"}, plainAsk: wantAll,
	}
	for label, expected := range want {
		matches := []observedRequest{}
		for _, row := range got {
			if row.label == label {
				matches = append(matches, row)
			}
		}
		if len(matches) != 1 {
			t.Errorf("actual ASK count %s: %d, want 1", label, len(matches))
			continue
		}
		if !reflect.DeepEqual(matches[0].tools, expected) {
			t.Errorf("actual ASK %s tools=%v want=%v; physical request=%s", label, matches[0].tools, expected, matches[0].body)
		}
	}
}
