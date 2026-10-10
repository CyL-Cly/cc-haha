package searchapi

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
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

func decoded(v any) any { var x any; _ = json.Unmarshal(raw(v), &x); return x }
func sourceSearch(t *testing.T, input map[string]any) []json.RawMessage {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Source Bun unavailable")
	}
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	_ = os.MkdirAll(filepath.Join(home, "workspace"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-search.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", "run", fixture)
	cmd.Dir = home
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "NODE_ENV=test", "USER_TYPE=external", "CLAUDE_CODE_ENTRYPOINT=claude-desktop", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_SEARCH_FIXTURE=1"}
	cmd.Stdin = bytes.NewReader(raw(input))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source Search %v %s", e, stderr.String())
	}
	var result struct{ Observations []json.RawMessage }
	if e = json.Unmarshal(data, &result); e != nil {
		t.Fatalf("Source JSON %v %s", e, data)
	}
	return result.Observations
}
func TestPortableSearchActualSourceReadlineCarriageReturns(t *testing.T) {
	first := `{"type":"user","message":{"content":"needle first"}}`
	second := `{"type":"assistant","message":{"content":"needle second"}}`
	files := []map[string]any{{"path": "$CONFIG/projects/p/carriage.jsonl", "content": "\r" + first + "\r" + second + "\r\n" + first + "\n"}}
	cases := []map[string]any{{"kind": "sessions", "query": "needle"}, {"kind": "sessions", "query": "second"}, {"kind": "sessions", "query": "first", "options": map[string]any{"matchesPerSession": 1}}}
	source := sourceSearch(t, map[string]any{"files": files, "cases": cases})
	f := newSearchFixture(t, files, "", false, false)
	for n, row := range cases {
		value, e := observeService(f, row)
		if e != nil {
			t.Fatal(e)
		}
		observed := map[string]any{"result": f.reduce(value)}
		if !reflect.DeepEqual(decoded(observed), decoded(source[n])) {
			t.Fatalf("Source readline case %d\nNative=%s\nSource=%s", n, raw(observed), source[n])
		}
	}
}

type fixture struct {
	s                       *Service
	c                       *localindex.ContentCoordinator
	home, config, workspace string
}

func newSearchFixture(t *testing.T, files []map[string]any, rg string, grep, index bool) *fixture {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	f := &fixture{home: home, config: filepath.Join(home, ".claude"), workspace: filepath.Join(home, "workspace")}
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	_ = os.MkdirAll(f.workspace, 0700)
	_ = os.MkdirAll(f.config, 0700)
	for _, file := range files {
		p := f.expand(file["path"].(string))
		_ = os.MkdirAll(filepath.Dir(p), 0700)
		_ = os.WriteFile(p, []byte(file["content"].(string)), 0600)
		when := 1791244800.0
		if x, ok := file["mtime"].(float64); ok {
			when = x
		}
		_ = os.Chtimes(p, time.Unix(int64(when), 0), time.Unix(int64(when), 0))
	}
	var e error
	f.c, e = localindex.NewContentCoordinator(localindex.ContentCoordinatorOptions{Scope: f.config, Budget: sessionstore.NewBudget(), SpoolDir: filepath.Join(home, "tmp")})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = f.c.Stop(context.Background()) })
	environment := map[string]string{"HOME": home, "TMPDIR": filepath.Join(home, "tmp"), "CLAUDE_CONFIG_DIR": f.config, "PATH": ""}
	if grep {
		environment["PATH"] = os.Getenv("PATH")
	}
	f.s, e = New(Options{ConfigDir: f.config, WorkDir: f.workspace, Environment: environment, Content: f.c, Ripgrep: func() Command { return Command{Path: rg, Args: []string{"--no-config", "--threads", "1"}} }, Metadata: func(context.Context, []string) (map[string]Metadata, error) { return nil, nil }, ReadEntriesAtLines: func(context.Context, string, map[int64]bool) (*TargetedEntries, error) { return nil, nil }, ParseDate: func(s string) (float64, bool, error) { return sessionprojection.ParseDateUTC(utf16.Encode([]rune(s))) }, Now: func() time.Time { return time.UnixMilli(1791244800000) }})
	if e != nil {
		t.Fatal(e)
	}
	if index {
		if e = f.c.Start(context.Background()); e != nil {
			t.Fatal(e)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if e = f.c.WaitInitial(ctx); e != nil {
			t.Fatal(e)
		}
		if f.c.Status().State != "ready" {
			t.Fatalf("Real native index not ready: %+v", f.c.Status())
		}
	}
	return f
}
func (f *fixture) expand(s string) string {
	return strings.NewReplacer("$HOME", f.home, "$CONFIG", f.config, "$WORKSPACE", f.workspace).Replace(s)
}
func (f *fixture) reduce(v any) any {
	var out any
	_ = json.Unmarshal([]byte(strings.ReplaceAll(string(raw(v)), f.home, "$HOME")), &out)
	return out
}
func sourceError(e error) map[string]any {
	result := map[string]any{"error": e.Error(), "status": nil, "code": nil}
	if api, ok := e.(*APIError); ok {
		result["status"] = api.Status
		result["code"] = api.Code
	}
	return result
}
func observeService(f *fixture, row map[string]any) (any, error) {
	ctx := context.Background()
	query, _ := row["query"].(string)
	var opts protocol.JSONObject
	_ = json.Unmarshal([]byte(f.expand(string(raw(row["options"])))), &opts)
	switch row["kind"] {
	case "sessions":
		return f.s.SearchSessions(ctx, query, opts)
	case "workspace":
		return f.s.SearchWorkspace(ctx, query, opts)
	case "suggest":
		var deadline *float64
		if x := opts.Get("deadlineMs"); len(x) > 0 {
			n := number(x, 0)
			deadline = &n
		}
		return f.s.Suggest(ctx, query, actualNumber(opts.Get("limit")), deadline)
	case "snippet":
		sensitive, _ := row["caseSensitive"].(bool)
		snippet, highlights := buildSnippet(row["text"].(string), query, sensitive)
		return map[string]any{"snippet": snippet, "highlights": highlights}, nil
	}
	return nil, nil
}
func TestActualSourceSearchCanonicalAndReadyIndex(t *testing.T) {
	line := func(kind, id, text string) string {
		return string(raw(map[string]any{"type": kind, "uuid": id, "timestamp": "2026-10-06T00:00:00Z", "message": map[string]any{"content": text}})) + "\n"
	}
	files := []map[string]any{{"path": "$CONFIG/projects/p/a.jsonl", "content": line("user", "u1", "one needle 中文 😄") + line("assistant", "a1", "second needle") + `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"needle","input":{}},{"type":"thinking","thinking":"needle"}]}}` + "\n" + line("user", "cmd", "<command-name>help</command-name><command-args>needle</command-args>") + "bad-json needle\n", "mtime": 1791244800.0}, {"path": "$CONFIG/projects/p/b.jsonl", "content": line("user", "u2", "Needle upper") + line("user", "u3", `escaped "quote" needle`) + line("user", "u4", "line\nbreak needle"), "mtime": 1791244900.0}, {"path": "$CONFIG/projects/p/a/subagents/worker.jsonl", "content": line("assistant", "w1", "nested needle"), "mtime": 1791245000.0}, {"path": "$CONFIG/projects/q/c.jsonl", "content": line("user", "u5", "final needle"), "mtime": 1791244700.0}, {"path": "$CONFIG/projects/q/noise.jsonl", "content": `{"type":"progress","name":"needle"}` + "\n"}}
	cases := []map[string]any{}
	for _, q := range []string{"needle", "Needle", "中文", "😄", "\"quote\"", "line\nbreak", "no match", ".*", " help", "help"} {
		cases = append(cases, map[string]any{"kind": "sessions", "query": q}, map[string]any{"kind": "sessions", "query": q, "options": map[string]any{"caseSensitive": true}})
	}
	for _, o := range []map[string]any{{"limit": 1, "matchesPerSession": 1}, {"limit": 0, "matchesPerSession": 0}, {"limit": -1, "matchesPerSession": -1}, {"limit": 1.9, "matchesPerSession": 1.9}, {"limit": "2", "matchesPerSession": true}, {"project": "p"}, {"project": "q"}, {"project": "missing"}, {"project": "../escape"}, {"project": nil}, {"modifiedAfter": "2026-10-06T00:00:00Z"}, {"modifiedBefore": "2026-10-06T00:00:00Z"}, {"modifiedAfter": "2026-99-99"}, {"modifiedAfter": "2026-10-06T00:01:00Z", "modifiedBefore": "2026-10-06T00:02:00Z"}} {
		cases = append(cases, map[string]any{"kind": "sessions", "query": "needle", "options": o})
	}
	cases = append(cases, map[string]any{"kind": "sessions", "query": "\ufeff \n"}, map[string]any{"kind": "suggest", "query": "needle"}, map[string]any{"kind": "suggest", "query": "  "})
	for _, index := range []bool{false, true} {
		t.Run(map[bool]string{false: "canonical", true: "readyIndex"}[index], func(t *testing.T) {
			source := sourceSearch(t, map[string]any{"files": files, "cases": cases, "index": index})
			f := newSearchFixture(t, files, "", false, index)
			for n, row := range cases {
				result, e := observeService(f, row)
				observation := map[string]any{"result": result}
				if e != nil {
					observation = sourceError(e)
				}
				if !reflect.DeepEqual(f.reduce(observation), decoded(source[n])) {
					t.Fatalf("Search Source %d %s\nNative=%s\nSource=%s", n, raw(row), raw(f.reduce(observation)), source[n])
				}
			}
		})
	}
}
func TestActualSourceWorkspaceTransportsAndUTF16Snippets(t *testing.T) {
	files := []map[string]any{{"path": "$WORKSPACE/a.txt", "content": "before\nhello Needle\nafter\nHELLO again\nlast\n"}, {"path": "$WORKSPACE/sub/b.ts", "content": "hello in typescript\n中文全文\n"}, {"path": "$WORKSPACE/node_modules/noise.txt", "content": "hello package\n"}, {"path": "$WORKSPACE/.git/noise.txt", "content": "hello git\n"}, {"path": "$WORKSPACE/binary", "content": "hello\x00binary\n"}}
	cases := []map[string]any{}
	for _, o := range []map[string]any{{}, {"caseSensitive": false}, {"caseSensitive": true}, {"maxResults": 1}, {"maxResults": 0}, {"maxResults": -1}, {"glob": "*.txt"}, {"glob": "*.ts"}, {"cwd": "$WORKSPACE/sub"}} {
		cases = append(cases, map[string]any{"kind": "workspace", "query": "hello", "options": o})
	}
	for _, q := range []string{"中文", "no match", "hel.o", "["} {
		cases = append(cases, map[string]any{"kind": "workspace", "query": q})
	}
	rg, _ := exec.LookPath("rg")
	for _, transport := range []struct {
		name, rg string
		grep     bool
	}{{"portable", "", false}, {"grep", "", true}, {"ripgrep", rg, false}} {
		if transport.name == "ripgrep" && rg == "" {
			continue
		}
		t.Run(transport.name, func(t *testing.T) {
			source := sourceSearch(t, map[string]any{"files": files, "cases": cases, "rg": transport.rg, "grep": transport.grep})
			f := newSearchFixture(t, files, transport.rg, transport.grep, false)
			for n, row := range cases {
				result, e := observeService(f, row)
				observation := map[string]any{"result": result}
				if e != nil {
					observation = sourceError(e)
				}
				if !reflect.DeepEqual(f.reduce(observation), decoded(source[n])) {
					t.Fatalf("Workspace Source %d %s\nNative=%s\nSource=%s", n, raw(row), raw(f.reduce(observation)), source[n])
				}
			}
		})
	}
	snippetCases := []map[string]any{}
	for _, text := range []string{"before\n\t needle \ufeff after", strings.Repeat("😄", 200) + "needle" + strings.Repeat("猫", 200), strings.Repeat("x", 500), "İ needle", "ΟΣ needle", "a\u0085b needle"} {
		for _, q := range []string{"needle", "missing", "ΟΣ", "i\u0307"} {
			snippetCases = append(snippetCases, map[string]any{"kind": "snippet", "query": q, "text": text})
		}
	}
	source := sourceSearch(t, map[string]any{"cases": snippetCases})
	f := newSearchFixture(t, nil, "", false, false)
	for n, row := range snippetCases {
		result, _ := observeService(f, row)
		if !reflect.DeepEqual(decoded(map[string]any{"result": result}), decoded(source[n])) {
			t.Fatalf("UTF16 snippet Source %d Native=%s Source=%s", n, raw(result), source[n])
		}
	}
}
