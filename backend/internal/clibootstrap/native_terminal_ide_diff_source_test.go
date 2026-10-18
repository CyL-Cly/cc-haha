package clibootstrap

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestTerminalIDEDiffActualSourceMountedPredicateRPCPatchUpdatedInputAndResponse(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		Tool, File, Encoding string
		Old                  *string
		OldJSON              json.RawMessage
		Input                map[string]any
		DiffTool             string
		Connected            bool
		HoldCleanup          bool
		Reply                any
	}
	ptr := func(value string) *string { return &value }
	text := func(value string) map[string]any { return map[string]any{"type": "text", "text": value} }
	rows := []row{
		{Tool: "Edit", Old: ptr("one\nold\nthree\n"), Input: map[string]any{"old_string": "old", "new_string": "new", "future": 17}, Reply: []any{text("TAB_CLOSED")}},
		{Tool: "Edit", Old: ptr("one\nold\nthree\n"), Input: map[string]any{"old_string": "old", "new_string": "new"}, Reply: []any{text("FILE_SAVED"), text("one\nuser-modified\nthree\n")}},
		{Tool: "Edit", Old: ptr("one\nold\nthree\n"), Input: map[string]any{"old_string": "old", "new_string": "new"}, Reply: []any{text("DIFF_REJECTED")}},
		{Tool: "Edit", Old: ptr("one\nremove\nlast\n"), Input: map[string]any{"old_string": "remove", "new_string": ""}, Reply: []any{text("TAB_CLOSED")}},
		{Tool: "Write", Old: nil, Input: map[string]any{"content": "中文 😀 & $\n"}, Reply: []any{text("TAB_CLOSED")}},
		{Tool: "Write", Old: ptr(""), Input: map[string]any{"content": ""}, Reply: []any{text("TAB_CLOSED")}},
		{Tool: "Write", Old: ptr("first\r\nold\r\n"), Input: map[string]any{"content": "first\nnew\n"}, Reply: []any{text("FILE_SAVED"), text("first\n保存\n")}},
		{Tool: "Edit", Encoding: "utf16le", Old: ptr("中文\r\nold😀\r\n"), Input: map[string]any{"old_string": "old😀", "new_string": "new🦄"}, Reply: []any{text("TAB_CLOSED")}},
		{Tool: "Edit", Old: ptr("<<:AMPERSAND_TOKEN:>> & $\n"), Input: map[string]any{"old_string": "& $", "new_string": "new & $"}, Reply: []any{text("TAB_CLOSED")}},
		{Tool: "Edit", Old: ptr("old"), Input: map[string]any{"old_string": "old", "new_string": "new"}, Reply: []any{text("FILE_SAVED")}},
		{Tool: "Edit", Old: ptr("old"), Input: map[string]any{"old_string": "old", "new_string": "new"}, Reply: []any{text("UNKNOWN")}},
		{Tool: "Write", Old: ptr(""), Input: map[string]any{"content": json.RawMessage(`"head\n\ud800\nlast\n"`)}, Reply: []any{text("TAB_CLOSED")}},
		{Tool: "Write", Old: ptr("old\n"), Input: map[string]any{"content": "new\n"}, Reply: []any{text("FILE_SAVED"), map[string]any{"type": "text", "text": json.RawMessage(`"head\n\udfff\nlast\n"`)}}},
		{Tool: "Edit", OldJSON: json.RawMessage(`"\ud800\r\n"`), Encoding: "utf16le", Input: map[string]any{"old_string": json.RawMessage(`"\ud800"`), "new_string": json.RawMessage(`"\udfff"`)}, Reply: []any{text("TAB_CLOSED")}},
		{Tool: "Edit", Old: ptr(""), Input: map[string]any{"old_string": "", "new_string": ""}, Reply: []any{text("TAB_CLOSED")}},
		{Tool: "Write", Old: ptr("\U0001d800\n"), Input: map[string]any{"content": json.RawMessage(`"\ud800\u0000\ud836\udc00\n"`)}, Reply: []any{text("TAB_CLOSED")}},
		{Tool: "Edit", Old: ptr("old\n"), Input: map[string]any{"old_string": "old", "new_string": "new"}, HoldCleanup: true, Reply: []any{text("FILE_SAVED"), text("modified\n")}},
	}
	for i := range rows {
		rows[i].Connected = true
		rows[i].DiffTool = "auto"
		rows[i].File = "file-" + string(rune('a'+i)) + ".txt"
	}
	rows = append(rows, row{Tool: "Write", File: "terminal.txt", Old: ptr(""), Input: map[string]any{"content": "new"}, Connected: true, DiffTool: "terminal"}, row{Tool: "Write", File: "notebook.ipynb", Old: ptr("{}"), Input: map[string]any{"content": "new"}, Connected: true, DiffTool: "auto"}, row{Tool: "Write", File: "unbound.txt", Old: ptr(""), Input: map[string]any{"content": "new"}, DiffTool: "auto"})
	inputRows := []any{}
	for _, row := range rows {
		var old any = row.Old
		if len(row.OldJSON) > 0 {
			old = row.OldJSON
		}
		inputRows = append(inputRows, map[string]any{"tool": row.Tool, "file": row.File, "old": old, "input": row.Input, "encoding": row.Encoding, "connected": row.Connected, "diffTool": row.DiffTool, "reply": row.Reply, "holdCleanup": row.HoldCleanup})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-ide-diff.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_IDE_DIFF_SOURCE=1", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home, "rows": inputRows}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("original IDE diff %v %.7000s", err, output)
	}
	var wants []struct {
		Proposal json.RawMessage
		Changes  []struct {
			Option struct{ Type string }
			Input  json.RawMessage
		}
		Calls                      []struct{ Name string }
		ShowingDiffInIDE, HasError bool
	}
	if err = json.Unmarshal(output, &wants); err != nil {
		t.Fatal(err, string(output))
	}
	if len(wants) != len(rows) {
		t.Fatal("original profiles missing", len(wants))
	}
	for i, row := range rows {
		want := wants[i]
		input := mainObj(rawMain(row.Input))
		input.Set("file_path", quoteMain(filepath.Join(home, row.File)))
		active := row.Connected && row.DiffTool == "auto" && !strings.HasSuffix(row.File, ".ipynb")
		if !active {
			if len(want.Calls) != 0 || want.ShowingDiffInIDE {
				t.Fatal("Source inactive mount issued RPC", i, want)
			}
			continue
		}
		old, err := terminalIDEReadContents(&NativeBoundary{Home: home, CWD: home, roots: []string{home}}, filepath.Join(home, row.File))
		if err != nil {
			t.Fatal(err)
		}
		proposal, err := terminalIDEProposal(row.Tool, input, old)
		if err != nil {
			t.Fatal(i, err)
		}
		originalUnits, originalErr := protocol.DecodeJavaScriptJSONString(want.Proposal)
		actualUnits, actualErr := protocol.DecodeJavaScriptJSONString(terminalIDEQuoteText(proposal))
		if originalErr != nil || actualErr != nil || !reflect.DeepEqual(originalUnits, actualUnits) {
			t.Fatal("actual proposal differs", i, actualUnits, originalUnits, actualErr, originalErr)
		}
		status, saved, err := terminalIDEDiffResponse(mainObj(rawMain(map[string]any{"content": row.Reply})))
		if err != nil {
			if !want.HasError || len(want.Changes) != 0 {
				t.Fatal("original malformed response guard differs", i, want)
			}
			continue
		}
		if status == "TAB_CLOSED" {
			saved = proposal
		}
		if status == "DIFF_REJECTED" {
			saved = old
		}
		updated, allowed := terminalIDEUpdatedInput(row.Tool, input, old, saved)
		if len(want.Changes) != 1 || (want.Changes[0].Option.Type == "accept-once") != allowed {
			t.Fatal("original response choice differs", i, want)
		}
		var actual, original any
		_ = json.Unmarshal(rawMain(updated), &actual)
		_ = json.Unmarshal(want.Changes[0].Input, &original)
		if !reflect.DeepEqual(actual, original) {
			t.Fatalf("row%d actual %s Source %s", i, rawMain(actual), rawMain(original))
		}
		for _, field := range []string{"content", "old_string", "new_string"} {
			if len(updated.Get(field)) == 0 {
				continue
			}
			actualUnits, actualErr = protocol.DecodeJavaScriptJSONString(updated.Get(field))
			originalUnits, originalErr = protocol.DecodeJavaScriptJSONString(mainObj(want.Changes[0].Input).Get(field))
			if actualErr != nil || originalErr != nil || !reflect.DeepEqual(actualUnits, originalUnits) {
				t.Fatal("actual updated input lost Source code units", i, field, actualUnits, originalUnits, actualErr, originalErr)
			}
		}
	}
}
