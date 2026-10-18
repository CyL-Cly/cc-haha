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
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestTerminalLocalJSXActualSourceCanonicalNilEmptyDisplayCaveatAndNextHarness(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned original Source required")
	}
	type inputRow struct {
		Name        string `json:"name"`
		Args        string `json:"args"`
		HasOutput   bool   `json:"hasOutput"`
		Output      string `json:"output"`
		Display     string `json:"display"`
		ShouldQuery bool   `json:"shouldQuery"`
	}
	rows := []inputRow{{Name: "mobile"}, {Name: "mobile", HasOutput: true}, {Name: "mobile", HasOutput: true, Output: "actual result"}, {Name: "plan", ShouldQuery: true}, {Name: "plan", HasOutput: true, ShouldQuery: true}, {Name: "plan", HasOutput: true, Output: "actual result", ShouldQuery: true}, {Name: "plan", HasOutput: true, Output: "actual result", Display: "user"}, {Name: "plan", Display: "system"}, {Name: "plan", HasOutput: true, Display: "system"}, {Name: "plan", HasOutput: true, Output: "actual result", Display: "system"}, {Name: "plan", Display: "skip"}, {Name: "plan", HasOutput: true, Output: "actual result", Display: "system", ShouldQuery: true}}
	// These concrete callbacks use their declaration's canonical name even
	// when the original invocation was /fork or /continue.
	rows = append(rows,
		inputRow{Name: "branch", HasOutput: true, Output: "Created a branch of this conversation.", Display: "system"},
		inputRow{Name: "branch", HasOutput: true, Output: "Failed to branch conversation: owned failure"},
		inputRow{Name: "resume", Display: "skip"},
		inputRow{Name: "resume", HasOutput: true, Output: "Resume cancelled", Display: "system"},
		inputRow{Name: "resume", HasOutput: true, Output: "Session owned-missing was not found."},
		inputRow{Name: "diff", HasOutput: true, Output: "Diff dialog dismissed"},
	)
	for i := range rows {
		rows[i].Args = "kept canonical args"
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-local-jsx.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_LOCAL_JSX_SOURCE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=100", "LINES=35"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home, "rows": rows}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Actual full Source local JSX %v %.6000s", err, output)
	}
	var source struct {
		Results []struct {
			Messages    []protocol.JSONObject
			ShouldQuery bool
		}
		Caps struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Results) != len(rows) || len(source.Caps.Blocked) != 0 {
		t.Fatal(err, string(output))
	}
	f := worktreeMainFixture(t)
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte("actual result")) || !bytes.Contains(body, []byte("<command-name>/mobile")) {
			t.Error("Actual next query lost canonical JSX history", string(body))
		}
		emitWorktreeModel(w, 1, "", "", "LOCAL_JSX_NEXT_DONE")
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	seedNativeReleaseWarmFixture(t, f, "fixture")
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Interactive: true, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "Actual local JSX next Harness", Tools: []string{"Read"}, MaxTurns: 2, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx := context.Background()
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	semantic := func(values []protocol.JSONObject) []map[string]any {
		out := []map[string]any{}
		for _, value := range values {
			envelope, _ := value.Object("message")
			out = append(out, map[string]any{"type": value.String("type"), "subtype": value.String("subtype"), "isMeta": string(value.Get("isMeta")), "role": envelope.String("role"), "messageContent": envelope.String("content"), "content": value.String("content"), "level": value.String("level")})
		}
		return out
	}
	for i, row := range rows {
		before := len(m.Controller.History())
		var value *string
		if row.HasOutput {
			copy := row.Output
			value = &copy
		}
		if err = m.CompleteTerminalLocalJSX(ctx, row.Name, row.Args, value, row.Display, row.ShouldQuery); err != nil {
			t.Fatal(i, err)
		}
		actual := m.Controller.History()[before:]
		if !reflect.DeepEqual(semantic(actual), semantic(source.Results[i].Messages)) {
			t.Fatalf("Case%d %+v actual%+v Source%+v", i, row, semantic(actual), semantic(source.Results[i].Messages))
		}
		if calls.Load() != 0 {
			t.Fatal("Completion started model")
		}
	}
	if err = m.CompleteTerminalLocalJSX(ctx, "unowned", "", nil, "", false); err == nil {
		t.Fatal("Arbitrary command admitted")
	}
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": "LOCAL_JSX_NEXT_HUMAN"})})
	if err = m.RunTurn(ctx, user); err != nil || calls.Load() != 1 {
		t.Fatal("Actual next Harness", err, calls.Load())
	}
}
