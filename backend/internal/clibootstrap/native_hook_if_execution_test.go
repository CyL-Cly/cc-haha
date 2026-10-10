package clibootstrap

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualNativeConditionalHookFiltersBeforeUnsupportedKindAndUsesOriginalInput(t *testing.T) {
	for _, row := range []struct {
		condition string
		match     bool
	}{{"Write(*/original.txt)", true}, {"Write(*/selected.txt)", false}, {"Read(*)", false}, {"Write", true}} {
		t.Run(row.condition, func(t *testing.T) {
			var original, selected, marker string
			var posts atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if posts.Add(1) == 1 {
					emitNativeTaskTool(w, "if-write", "Write", map[string]any{"file_path": original, "content": "original-file"})
					return
				}
				if row.match && !strings.Contains(string(body), selected) {
					t.Error("actual changed tool feedback missing")
				}
				emitPlanFixture(w, 2, "", "IF_DONE")
			}))
			defer peer.Close()
			m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
				delete(n.Environment, "CLAUDE_CODE_SIMPLE")
				original = filepath.Join(n.CWD, "original.txt")
				selected = filepath.Join(n.CWD, "selected.txt")
				marker = filepath.Join(n.CWD, "actual-if-marker")
				script := filepath.Join(n.CWD, "conditional.py")
				code := fmt.Sprintf("import sys,json\nx=json.load(sys.stdin)\nwith open(%q,'w') as f:f.write(x['tool_input']['file_path'])\nprint(json.dumps({'hookSpecificOutput':{'hookEventName':'PreToolUse','permissionDecision':'allow','updatedInput':{'file_path':%q,'content':'changed'}}}))\n", marker, selected)
				if err := os.WriteFile(script, []byte(code), 0600); err != nil {
					t.Fatal(err)
				}
				hooks := map[string]any{"PreToolUse": []any{map[string]any{"matcher": "Write", "hooks": []any{map[string]any{"type": "prompt", "prompt": "unmatched kind cannot run", "if": "Read(*)"}, map[string]any{"type": "command", "command": "/usr/bin/python3 " + strconv.Quote(script), "if": row.condition}}}}}
				if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), rawMain(map[string]any{"hooks": hooks}), 0600); err != nil {
					t.Fatal(err)
				}
			}, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"Write"} })
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "actual conditional Hook"})})); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(marker)
			if row.match {
				if err != nil || string(data) != original {
					t.Fatal("if consumed changed input instead of original", string(data), err)
				}
				data, err = os.ReadFile(selected)
				if err != nil || string(data) != "changed" {
					t.Fatal(err, string(data))
				}
				if _, err = os.Stat(original); !os.IsNotExist(err) {
					t.Fatal("original executed", err)
				}
			} else {
				if !os.IsNotExist(err) {
					t.Fatal("unmatched hook executed", err)
				}
				data, err = os.ReadFile(original)
				if err != nil || string(data) != "original-file" {
					t.Fatal(err, string(data))
				}
			}
		})
	}
}
