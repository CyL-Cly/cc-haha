package terminaldiff

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func originalDiff(t *testing.T, input any) protocol.JSONObject {
	return originalDiffHome(t, "", input)
}
func originalDiffHome(t *testing.T, home string, input any) protocol.JSONObject {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned Source Bun required")
	}
	if home == "" {
		home, _ = filepath.EvalSymlinks(t.TempDir())
	}
	temp := filepath.Join(home, "tmp")
	if e := os.MkdirAll(temp, 0700); e != nil {
		t.Fatal(e)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-diff.fixture.ts")
	var object map[string]any
	b, _ := json.Marshal(input)
	_ = json.Unmarshal(b, &object)
	object["home"] = home
	b, _ = json.Marshal(object)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_DIFF_SOURCE=1", "PATH=" + filepath.Join(home, "bin") + ":/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "NODE_ENV=production"}
	cmd.Stdin = bytes.NewReader(b)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("actual complete Source %v %s", e, stderr.String())
	}
	var result protocol.JSONObject
	if e = json.Unmarshal(out, &result); e != nil {
		t.Fatalf("Source JSON %v %.500s", e, out)
	}
	return result
}
func jsonSame(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	var left, right any
	_ = json.Unmarshal(x, &left)
	_ = json.Unmarshal(y, &right)
	return reflect.DeepEqual(left, right)
}

func TestActualSourceGitDiffParsersLimitsAndTurnAccumulation(t *testing.T) {
	type parser struct{ Numstat, Shortstat, Diff string }
	cases := []parser{
		{"2\t1\tz.txt\n-\t-\tb.bin\n0\t0\ta.txt\n", "3 files changed, 2 insertions(+), 1 deletion(-)", "diff --git a/z.txt b/z.txt\nindex abc..def\n--- a/z.txt\n+++ b/z.txt\n@@ -1,2 +1,3 @@ section\n unchanged\n-old\n+new\n+more\n"},
		{"malformed\n1\t2\ttab\tname\n", "1 file changed, 2 deletions(-)", "diff --git a/tab b/tab\n@@ -0,0 +1 @@\n+last"},
	}
	var num, diff strings.Builder
	for i := 0; i < 55; i++ {
		name := strings.Repeat("a", i+1)
		num.WriteString("1\t0\t" + name + "\n")
		diff.WriteString("diff --git a/" + name + " b/" + name + "\n@@ -0,0 +1,401 @@\n" + strings.Repeat("+line\n", 401))
	}
	cases = append(cases, parser{num.String(), "55 files changed, 55 insertions(+)", diff.String()}, parser{"1\t0\tbig\n", "", "diff --git a/big b/big\n@@ -1 +1 @@\n+" + strings.Repeat("🙂", 500001)})
	histories := [][]protocol.JSONObject{}
	decode := func(s string) protocol.JSONObject {
		var v protocol.JSONObject
		if e := json.Unmarshal([]byte(s), &v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	history := []protocol.JSONObject{decode(`{"type":"user","message":{"content":"first prompt"},"timestamp":"one"}`), decode(`{"type":"user","message":{"content":[{"type":"tool_result"}]},"toolUseResult":{"type":"create","filePath":"new.txt","content":"a\nb\n","structuredPatch":[]}}`), decode(`{"type":"assistant","message":{"content":[]}}`)}
	histories = append(histories, append([]protocol.JSONObject{}, history...))
	history = append(history, decode(`{"type":"user","isMeta":true,"message":{"content":"do not start turn"}}`), decode(`{"type":"user","message":{"content":"second prompt that is longer than thirty characters"}}`), decode(`{"type":"user","toolUseResult":{"filePath":"edited.txt","structuredPatch":[{"oldStart":1,"oldLines":1,"newStart":1,"newLines":1,"lines":["-before","+after"]}]},"message":{"content":[{"type":"tool_result"}]}}`))
	histories = append(histories, history, history[:1])
	unicodePrompt := strings.Repeat("a", 28) + "😀z"
	unicodeUser := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: json.RawMessage(`"user"`)}, protocol.JSONField{Name: "message", Value: func() json.RawMessage { b, _ := json.Marshal(map[string]string{"content": unicodePrompt}); return b }()})
	// A different conversation remounts DiffDialog. Reset the hook cache by
	// its actual shrink boundary before this independent Unicode history.
	histories = append(histories, []protocol.JSONObject{}, []protocol.JSONObject{unicodeUser, history[1]})
	inputs := []map[string]string{}
	for _, c := range cases {
		inputs = append(inputs, map[string]string{"numstat": c.Numstat, "shortstat": c.Shortstat, "diff": c.Diff})
	}
	source := originalDiff(t, map[string]any{"parsers": inputs, "histories": histories})
	var rows []protocol.JSONObject
	_ = json.Unmarshal(source.Get("parsed"), &rows)
	for i, row := range rows {
		var parsed struct {
			Stats   Stats `json:"stats"`
			PerFile map[string]struct {
				Added, Removed int
				IsBinary       bool
			} `json:"perFileStats"`
		}
		_ = json.Unmarshal(row.Get("stats"), &parsed)
		data, e := Current("", cases[i].Numstat, "", cases[i].Diff)
		if e != nil {
			t.Fatal(e)
		}
		if !jsonSame(data.Stats, parsed.Stats) {
			t.Fatalf("stats %d %+v %+v", i, data.Stats, parsed.Stats)
		}
		var expected map[string][]Hunk
		_ = json.Unmarshal(row.Get("hunks"), &expected)
		if !jsonSame(ParseHunks(cases[i].Diff), expected) {
			t.Fatalf("hunks %d differ", i)
		}
		var expectedShort *Stats
		_ = json.Unmarshal(row.Get("short"), &expectedShort)
		if !jsonSame(Shortstat(cases[i].Shortstat), expectedShort) {
			t.Fatalf("shortstat %d", i)
		}
	}
	var turns [][]protocol.JSONObject
	_ = json.Unmarshal(source.Get("turns"), &turns)
	for i, history := range histories {
		actual, e := Turns(history)
		if e != nil {
			t.Fatal(e)
		}
		if len(actual) != len(turns[i]) {
			t.Fatalf("turn count %d %d %d", i, len(actual), len(turns[i]))
		}
		for j, turn := range actual {
			original := turns[i][j]
			encoded, e := turn.MarshalJSON()
			if e != nil {
				t.Fatal(e)
			}
			var exact protocol.JSONObject
			_ = exact.UnmarshalJSON(encoded)
			left, _ := protocol.DecodeJavaScriptJSONString(exact.Get("userPromptPreview"))
			right, _ := protocol.DecodeJavaScriptJSONString(original.Get("userPromptPreview"))
			if !reflect.DeepEqual(left, right) {
				t.Fatal("original preview UTF16 code units changed", string(exact.Get("userPromptPreview")), string(original.Get("userPromptPreview")))
			}
			var files map[string]protocol.JSONObject
			_ = json.Unmarshal(original.Get("files"), &files)
			if turn.TurnIndex != int(numberJSON(original.Get("turnIndex"))) || turn.UserPromptPreview != original.String("userPromptPreview") || len(turn.Data.Files) != len(files) {
				t.Fatalf("turn %d/%d %+v Source %s", i, j, turn, original.Get("files"))
			}
			for _, file := range turn.Data.Files {
				expected := files[file.Path]
				var hunks []Hunk
				_ = json.Unmarshal(expected.Get("hunks"), &hunks)
				if file.LinesAdded != int(numberJSON(expected.Get("linesAdded"))) || file.LinesRemoved != int(numberJSON(expected.Get("linesRemoved"))) || file.IsNewFile != (string(expected.Get("isNewFile")) == "true") || !jsonSame(file.Hunks, hunks) {
					t.Fatalf("file %d/%d %+v Source %s", i, j, file, expected.Get("hunks"))
				}
			}
		}
	}
}
func numberJSON(b []byte) float64 { var v float64; _ = json.Unmarshal(b, &v); return v }
