package builtintools

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionrewind"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeActivityClassificationAndSharedSplitterMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	cwd := filepath.Join(home, "project")
	temp := filepath.Join(home, "tmp")
	if e := os.MkdirAll(cwd, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.MkdirAll(temp, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.MkdirAll(filepath.Join(home, ".git"), 0700); e != nil {
		t.Fatal(e)
	}
	commands := []string{"", " ", "echo only", "ls dir", "cat file | grep term", "ls dir && echo marker && ls second", "cat > output", "cat 2>&1", "cat > out other", "ls && rm -rf temp", "'cat' file", `cat "a && b" | wc -l`, "cat <<'EOF'\na | b\nEOF\n", "cat <<<value", "cat < file", "rg pattern\ncat file", "echo \\\ncat file", "echo \\\\\ncat file", "cat ${bad substitution}", "cat; # quoted \"comment\"", `cat $(rm file)`, "tr\\\naceroute", "ls & cat", "cat | jq '.a'", "find . | sort | uniq", "cat && true && rg test", "rg test || false", "du .; printf marker", "cat | bq", "StructuredOutput"}
	rows := []map[string]any{}
	for _, command := range commands {
		rows = append(rows, map[string]any{"name": "Bash", "input": map[string]any{"command": command}})
	}
	for _, in := range []any{map[string]any{}, map[string]any{"command": 3}, map[string]any{"command": "cat", "unknown": true}, map[string]any{"command": "cat", "timeout": "true"}, map[string]any{"command": "cat", "run_in_background": "true"}} {
		rows = append(rows, map[string]any{"name": "Bash", "input": in})
	}
	for _, name := range []string{"Read", "Grep", "Glob", "Write", "Edit", "Unknown", "REPL", "StructuredOutput"} {
		rows = append(rows, map[string]any{"name": name, "input": map[string]any{"file_path": "ordinary.txt"}})
	}
	for _, command := range []string{"", "Get-ChildItem", "LS | CAT", "echo marker; Get-Content file", "Get-Content | Select-String term", "Get-Process | Stop-Process", "SLS test", "cat.exe file", "Get-Content 'a|b'", "where.exe program", `scripts\cat.exe file`, "Get-FileHash file", "Write-Host; Get-Acl file", "dir && cat", "findstr test", "cat.ps1 file"} {
		rows = append(rows, map[string]any{"name": "PowerShell", "input": map[string]any{"command": command}})
	}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "cwd": cwd, "classifications": rows, "splits": commands})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-agent-progress.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CLAUDE_CODE_TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_AGENT_PROGRESS_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(payload)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.4500s", e, out)
	}
	var source struct {
		Classification []protocol.JSONObject
		Splits         [][]string
		Capabilities   struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Classification) != len(rows) || len(source.Capabilities.Blocked) != 0 {
		t.Fatalf("Source %v %.4500s", e, out)
	}
	for i, row := range rows {
		in, _ := json.Marshal(row["input"])
		var input protocol.JSONObject
		_ = input.UnmarshalJSON(in)
		search, read, e := NativeActivityClassification(row["name"].(string), input)
		got, _ := json.Marshal(protocol.NewJSONObject(protocol.JSONField{Name: "isSearch", Value: raw(search)}, protocol.JSONField{Name: "isRead", Value: raw(read)}))
		want := source.Classification[i]
		if e != nil || string(protocol.StringifyValidatedJavaScriptJSON(got)) != string(protocol.StringifyValidatedJavaScriptJSON(raw(want))) {
			t.Errorf("classification%d %v Go%s/%v Source%s", i, row, got, e, raw(want))
		}
	}
	classifier, e := sessionrewind.NewRecordedCommandClassifier("darwin", "")
	if e != nil {
		t.Fatal(e)
	}
	for i, command := range commands {
		got, e := classifier.SplitWithOperators(command)
		g, _ := json.Marshal(got)
		want, _ := json.Marshal(source.Splits[i])
		if e != nil || !bytes.Equal(g, want) {
			t.Errorf("split%q Go%s/%v Source%s", command, g, e, want)
		}
	}
}
