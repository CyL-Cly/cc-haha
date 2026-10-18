package application

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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTerminalPlanCommandActualSourceEntryNoPlanDisplayAndNextHarness(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual pinned Source Bun required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{}
	for _, mode := range []string{"default", "acceptEdits", "plan"} {
		for _, args := range []string{"", "open", " \ufeff Investigate 中文 😀 \u3000"} {
			rows = append(rows, map[string]any{"mode": mode, "args": args, "plan": nil, "editor": ""})
		}
	}
	for _, plan := range []string{"", "First line\nSecond 中文 😀 line"} {
		rows = append(rows, map[string]any{"mode": "plan", "args": "view", "plan": plan, "editor": ""})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-plan.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PLAN_COMMAND_SOURCE=1", "ANTHROPIC_API_KEY=fake-plan-source", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "rows": rows}))
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actualPlancommandSource %v %.4000s", err, data)
	}
	var source struct {
		Declaration protocol.JSONObject
		Rows        []struct {
			Done []json.RawMessage
			Mode string
		}
		PlanPath string
	}
	if err = json.Unmarshal(data, &source); err != nil || len(source.Rows) != len(rows) {
		t.Fatalf("invalid Source %v %.4000s", err, data)
	}
	if source.Declaration.String("name") != "plan" || source.Declaration.String("type") != "local-jsx" {
		t.Fatal(source.Declaration)
	}
	for index, row := range rows {
		t.Run(row["mode"].(string)+"/"+row["args"].(string), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				n := calls.Add(1)
				if n == 1 && row["mode"] != "plan" && strings.Contains(row["args"].(string), "Investigate") && !bytes.Contains(body, []byte("Enabled plan mode")) {
					t.Error("description did not reach same real model input")
				}
				response(w, "plan-command", "", nil, "PLAN_NEXT_HARNESS")
			}))
			defer server.Close()
			o := nativeSessionOptions(t, server.URL)
			o.Interactive = true
			o.Environment["NO_PROXY"] = "127.0.0.1,localhost"
			o.Native.Environment["NO_PROXY"] = "127.0.0.1,localhost"
			o.Arguments.PermissionMode = row["mode"].(string)
			o.Environment["VISUAL"] = ""
			o.Environment["EDITOR"] = ""
			o.Environment["PATH"] = ""
			o.Native.Environment["PATH"] = ""
			o.Native.Environment["EDITOR"] = ""
			o.Native.Environment["VISUAL"] = ""
			s, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			planPath := ""
			if err = s.process.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				var e error
				planPath, e = s.process.Plans().Path(scope, "")
				if e == nil && row["plan"] != nil {
					e = os.WriteFile(planPath, []byte(row["plan"].(string)), 0600)
				}
				return e
			}); err != nil {
				t.Fatal(err)
			}
			var local []tui.LocalProcessedMessage
			var outputMu sync.Mutex
			emit := func(e tui.Event) error {
				outputMu.Lock()
				defer outputMu.Unlock()
				local = append(local, e.LocalMessages...)
				for _, text := range e.UserMessages {
					local = append(local, tui.LocalProcessedMessage{Kind: "user", Text: text})
				}
				return nil
			}
			err = s.Submit(ctx, tui.Input{TurnID: newID(), Text: "/plan " + row["args"].(string), Mode: "prompt"}, emit)
			if err != nil {
				t.Fatal("actual /plan entry", err)
			}
			var result []json.RawMessage
			if len(source.Rows[index].Done) != 1 || json.Unmarshal(source.Rows[index].Done[0], &result) != nil || len(result) == 0 {
				t.Fatal("Source completion", index)
			}
			var expected string
			_ = json.Unmarshal(result[0], &expected)
			if row["plan"] != nil && row["plan"] != "" {
				current := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
				current.Dir = home
				current.Env = cmd.Env
				current.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "rows": []any{row}, "planPath": planPath}))
				fresh, e := current.CombinedOutput()
				if e != nil {
					t.Fatalf("actual same-path PlanDisplay %v %.3000s", e, fresh)
				}
				var exact struct {
					Rows []struct{ Done []json.RawMessage }
				}
				if json.Unmarshal(fresh, &exact) != nil || len(exact.Rows) != 1 || len(exact.Rows[0].Done) != 1 || json.Unmarshal(exact.Rows[0].Done[0], &result) != nil {
					t.Fatalf("invalid same-path frame %.3000s", fresh)
				}
				_ = json.Unmarshal(result[0], &expected)
			}
			outputMu.Lock()
			visible := append([]tui.LocalProcessedMessage{}, local...)
			outputMu.Unlock()
			found := false
			for _, v := range visible {
				found = found || v.Text == "<local-command-stdout>"+expected+"</local-command-stdout>"
			}
			if !found {
				t.Fatalf("same canonical local completion missing; want%q got%+v", expected, visible)
			}
			if err = s.process.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				p, e := s.process.Controller.PlanModeContext(scope)
				if e == nil && p.String("mode") != source.Rows[index].Mode {
					t.Error("actual permission mode", p, source.Rows[index].Mode)
				}
				return e
			}); err != nil {
				t.Fatal(err)
			}
			want := 0
			if row["mode"] != "plan" && strings.Contains(row["args"].(string), "Investigate") {
				want = 1
			}
			if int(calls.Load()) != want {
				t.Fatal("model cardinality", calls.Load(), want)
			}
			if err = s.Submit(ctx, tui.Input{TurnID: newID(), Text: "continue real same harness", Mode: "prompt"}, emit); err != nil {
				t.Fatal(err)
			}
			if int(calls.Load()) != want+1 {
				t.Fatal("next human not admitted", calls.Load(), want)
			}
		})
	}
}
