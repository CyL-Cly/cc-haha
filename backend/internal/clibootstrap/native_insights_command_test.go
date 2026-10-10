package clibootstrap

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestActualInsightsCommandScansCanonicalHistoryCachesFacetsWritesHTMLAndResumesSameHarness(t *testing.T) {
	f := worktreeMainFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"agentTeamsEnabled":false,"alwaysThinkingEnabled":false,"enabledPlugins":{"sample@mkt":false}}`)
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	f.n.Environment["ANTHROPIC_DEFAULT_OPUS_MODEL"] = "actual-insights-analysis"
	sid := "11111111-1111-4111-8111-111111111111"
	path := filepath.Join(f.config, "projects", "private-old-project", sid+".jsonl")
	fixture := `{"type":"user","uuid":"u","parentUuid":null,"sessionId":"11111111-1111-4111-8111-111111111111","cwd":"/private/project","timestamp":"2026-01-01T00:00:00Z","message":{"content":"PRIVATE_INSIGHTS_CANONICAL_831 initial"}}
{"type":"assistant","uuid":"a","parentUuid":"u","sessionId":"11111111-1111-4111-8111-111111111111","timestamp":"2026-01-01T00:01:00Z","message":{"id":"one","content":[{"type":"tool_use","name":"Read","input":{"file_path":"real.go"}}]}}
{"type":"user","uuid":"u2","parentUuid":"a","sessionId":"11111111-1111-4111-8111-111111111111","timestamp":"2026-01-01T00:02:00Z","message":{"content":"PRIVATE_INSIGHTS_CANONICAL_831 continue"}}
{"type":"assistant","uuid":"a2","parentUuid":"u2","sessionId":"11111111-1111-4111-8111-111111111111","timestamp":"2026-01-01T00:03:00Z","message":{"id":"two","content":[{"type":"text","text":"actual done"}]}}
`
	f.write(t, path, fixture)
	var facets, sections, glance, mainCalls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		text := ""
		switch {
		case bytes.Contains(body, []byte("matching this schema:")):
			facets.Add(1)
			if !bytes.Contains(body, []byte("PRIVATE_INSIGHTS_CANONICAL_831")) || !bytes.Contains(body, []byte("actual-insights-analysis")) {
				t.Error("facet did not consume actual canonical/model")
			}
			text = `{"underlying_goal":"actual file task","goal_categories":{"understand_codebase":1},"outcome":"fully_achieved","brief_summary":"PRIVATE_INSIGHTS_REPORT_831","user_satisfaction_counts":{"satisfied":1},"friction_counts":{},"claude_helpfulness":"essential","primary_success":"correct_code_edits","session_type":"single_task"}`
		case bytes.Contains(body, []byte("You're writing an")):
			glance.Add(1)
			text = `{"whats_working":"PRIVATE_INSIGHTS_REPORT_831","quick_wins":"try real skills"}`
		case bytes.Contains(body, []byte("The user just ran /insights")):
			mainCalls.Add(1)
			if !bytes.Contains(body, []byte("file://")) || !bytes.Contains(body, []byte("PRIVATE_INSIGHTS_REPORT_831")) {
				t.Error("final report prompt missing")
			}
			text = "Your shareable insights report is ready: file://" + filepath.Join(f.config, "usage-data", "report.html")
		case bytes.Contains(body, []byte("DATA:")):
			sections.Add(1)
			text = `{}`
		default:
			mainCalls.Add(1)
			text = "actual normal turn after report"
		}
		emitWorktreeModel(w, 1, "", "", text)
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	disabled := false
	var diagnostics bytes.Buffer
	m, err := NewMainPrint(MainPrintOptions{Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: &diagnostics, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "Actual report main Harness", Tools: []string{"Read"}, MaxTurns: 2, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	user := func(text string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": text})})
	}
	if err = m.RunTurn(ctx, user("/insights ignored external arguments")); err != nil {
		t.Fatal(err, diagnostics.String())
	}
	if facets.Load() != 1 || sections.Load() != 7 || glance.Load() != 1 || mainCalls.Load() != 1 {
		t.Fatal("actual phases", facets.Load(), sections.Load(), glance.Load(), mainCalls.Load(), diagnostics.String())
	}
	report, err := os.ReadFile(filepath.Join(f.config, "usage-data", "report.html"))
	if err != nil || !bytes.Contains(report, []byte("PRIVATE_INSIGHTS_REPORT_831")) {
		t.Fatal("physical HTML", err)
	}
	for _, part := range []string{"session-meta", "facets"} {
		p := filepath.Join(f.config, "usage-data", part, sid+".json")
		info, err := os.Stat(p)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("real cache", p, info, err)
		}
	}
	if err = m.RunTurn(ctx, user("/insights")); err != nil {
		t.Fatal(err, diagnostics.String())
	}
	if facets.Load() != 1 || sections.Load() != 14 || glance.Load() != 2 || mainCalls.Load() != 2 {
		t.Fatal("warm cache phases", facets.Load(), sections.Load(), glance.Load(), mainCalls.Load())
	}
	if err = m.RunTurn(ctx, user("continue normally")); err != nil {
		t.Fatal(err)
	}
	if mainCalls.Load() != 3 {
		t.Fatal(mainCalls.Load())
	}
}
