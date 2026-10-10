package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/activitystatsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

func sessionConsumerSource(t *testing.T, mode string, ops any) []json.RawMessage {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Source Bun unavailable")
	}
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	home, _ = filepath.EvalSymlinks(home)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-local-index.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", "run", fixture)
	cmd.Dir = home
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "NODE_ENV=test", "USER_TYPE=external", "CLAUDE_CODE_ENTRYPOINT=claude-desktop", "BUN_OPTIONS=--no-env-file", "TZ=UTC", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_LOCAL_INDEX_FIXTURE=1"}
	data, _ := json.Marshal(map[string]any{"action": "sessionConsumer", "mode": mode, "operations": ops})
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatalf("Actual Source consumer %v %s", e, stderr.String())
	}
	var result struct{ Observations []json.RawMessage }
	if e = json.Unmarshal(output, &result); e != nil {
		t.Fatalf("Source JSON %v %s", e, output)
	}
	return result.Observations
}
func consumerFixture(t *testing.T, mode string) (*Server, *localindex.Coordinator, string, *time.Time) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	work := filepath.Join(home, "workspace")
	_ = os.MkdirAll(work, 0700)
	home, _ = filepath.EvalSymlinks(home)
	work = filepath.Join(home, "workspace")
	config := filepath.Join(home, ".claude")
	now := time.UnixMilli(1791331200000)
	parser := activitystatsapi.NewIndexDateParser(time.UTC)
	compare := collate.New(language.English)
	budget := sessionstore.NewBudget()
	primary, e := localindex.NewCoordinator(localindex.CoordinatorOptions{Scope: config, Mode: mode, Budget: budget, Now: func() time.Time { return now }, SourceMetadata: NativeIndexSourceMetadata, Reduction: localindex.ReductionOptions{ParseDate: parser.Parse, Compare: compare.CompareString, Location: time.UTC, Platform: runtimeIndexPlatformTest()}, WatcherOptions: localindex.WatchOptions{Debounce: time.Hour, MaxWait: time.Hour, SafetySweep: time.Hour}})
	if e != nil {
		t.Fatal(e)
	}
	s, e := New(Options{HomeDir: home, ConfigDir: config, WorkDir: work, HistoryBudget: budget, LocalIndex: primary, Factory: func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil }})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()); _ = primary.Stop(context.Background()) })
	return s, primary, home, &now
}
func runtimeIndexPlatformTest() string {
	if runtime.GOOS == "windows" {
		return "win32"
	}
	return runtime.GOOS
}
func plainConsumer(v any, home string) any {
	data, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal([]byte(strings.ReplaceAll(string(data), home, "$HOME")), &out)
	return out
}
func (s *Server) consumerList(ctx context.Context, project *string, limit, offset float64) ([]sessionInfo, int, error) {
	o := localindex.SessionListOptions{Project: project, Limit: &limit, Offset: &offset}
	if rows, total, ok := s.indexedSessionList(ctx, o); ok {
		return rows, total, nil
	}
	all, e := s.allSessionsFromFiles(ctx)
	if e != nil {
		return nil, 0, e
	}
	filtered := []sessionInfo{}
	for _, row := range all {
		if project == nil || row.ProjectPath == *project {
			filtered = append(filtered, row)
		}
	}
	start := min(len(filtered), max(0, int(offset)))
	end := min(len(filtered), start+max(0, int(limit)))
	return filtered[start:end], len(filtered), nil
}
func TestSessionIndexConsumersAgainstActualSessionService(t *testing.T) {
	id1, id2, id3 := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"
	path1 := "$CONFIG/projects/p/" + id1 + ".jsonl"
	path2 := "$CONFIG/projects/p/" + id2 + ".jsonl"
	path3 := "$CONFIG/projects/q/" + id3 + ".jsonl"
	user := func(text, stamp string) string {
		return fmt.Sprintf(`{"type":"user","timestamp":%q,"cwd":"$WORKSPACE","message":{"role":"user","content":%q}}`, stamp, text) + "\n"
	}
	worker := `{"type":"system","timestamp":"2026-10-06T00:00:00Z","entrypoint":"claude-desktop-team-worker","message":{"role":"system","content":"worker"}}` + "\n"
	meta := `{"type":"session-meta","timestamp":"2026-10-06T00:00:00Z","workDir":"$WORKSPACE"}` + "\n"
	ops := []map[string]any{{"method": "write", "path": path1, "write": user("first", "2026-10-06T00:00:00Z")}, {"method": "write", "path": path2, "write": user("second", "2026-10-06T00:02:00Z")}, {"method": "write", "path": path3, "write": worker}, {"method": "list"}, {"method": "start"}, {"method": "list"}, {"method": "list", "options": map[string]any{"limit": 1, "offset": 1}}, {"method": "list", "options": map[string]any{"limit": 0}}, {"method": "list", "options": map[string]any{"project": "missing"}}, {"method": "summary", "id": id1}, {"method": "find", "id": id1}, {"method": "sidebar", "limit": 1}, {"method": "write", "path": "$CONFIG/projects/duplicate/" + id1 + ".jsonl", "write": meta, "mtime": 1791420000}, {"method": "sweep"}, {"method": "find", "id": id1}, {"method": "summary", "id": id1}, {"method": "notify", "path": path1, "append": `{"type":"custom-title","customTitle":"renamed exact"}` + "\n", "mutation": true, "patchTitle": "renamed exact", "id": id1}, {"method": "list"}, {"method": "summary", "id": id1}, {"method": "notify", "path": path2, "append": `{"type":"user","runtimeModelId":"` + strings.Repeat("x", 4097) + `"}` + "\n"}, {"method": "list"}, {"method": "notify", "path": path2, "write": user("rewritten", "2026-10-06T00:04:00Z")}, {"method": "list"}, {"method": "notify", "path": path1, "remove": true, "mutation": true}, {"method": "list"}, {"method": "find", "id": id1}, {"method": "summary", "id": id1}, {"method": "sidebar", "limit": 2}, {"method": "stop"}, {"method": "list"}, {"method": "start"}, {"method": "list"}}

	sql := func(query string, bindings ...any) map[string]any {
		return map[string]any{"query": query, "bindings": bindings}
	}
	ops = append(ops, map[string]any{"method": "list", "sql": sql("UPDATE sessions SET project_path='forged' WHERE session_id=?", id2)}, map[string]any{"method": "list", "sql": sql("UPDATE sessions SET project_path='p',work_dir=NULL WHERE session_id=?", id2)}, map[string]any{"method": "list", "sql": sql("UPDATE sessions SET work_dir=? WHERE session_id=?", "$WORKSPACE", id2)})
	for _, mode := range []string{"on", "shadow", "off"} {
		t.Run(mode, func(t *testing.T) {
			source := sessionConsumerSource(t, mode, ops)
			s, index, home, clock := consumerFixture(t, mode)
			expand := func(value string) string {
				return strings.NewReplacer("$HOME", home, "$CONFIG", s.options.ConfigDir, "$WORKSPACE", s.options.WorkDir).Replace(value)
			}
			ctx := context.Background()
			seen := 0
			for _, row := range ops {
				*clock = clock.Add(time.Second)
				path, _ := row["path"].(string)
				path = expand(path)
				if data, ok := row["write"].(string); ok {
					_ = os.MkdirAll(filepath.Dir(path), 0700)
					_ = os.WriteFile(path, []byte(expand(data)), 0600)
					stamp := int64(1791244800)
					if value, ok := row["mtime"].(int); ok {
						stamp = int64(value)
					}
					_ = os.Chtimes(path, time.Unix(stamp, 0), time.Unix(stamp, 0))
				}
				if data, ok := row["append"].(string); ok {
					f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
					_, _ = f.WriteString(expand(data))
					_ = f.Close()
					_ = os.Chtimes(path, time.Unix(1791331200, 0), time.Unix(1791331200, 0))
				}
				if row["remove"] == true {
					_ = os.RemoveAll(path)
				}
				if row["mutation"] == true {
					s.noteSessionMutation(path)
				}
				if title, ok := row["patchTitle"].(string); ok {
					_, _ = index.UpdateTitle(ctx, row["id"].(string), title)
				}
				method := row["method"].(string)
				switch method {
				case "start":
					if e := index.Start(ctx); e != nil {
						t.Fatal(e)
					}
					_ = index.WaitInitial(ctx)
				case "stop":
					_ = index.Stop(ctx)
				case "notify":
					if mode != "off" {
						_ = index.Reconcile(ctx, localindex.ReconciliationBatch{Paths: []string{path}})
					}
				case "sweep":
					if mode != "off" {
						_ = index.Reconcile(ctx, localindex.ReconciliationBatch{FullSweep: true})
					}
				}
				if operation, ok := row["sql"].(map[string]any); ok {
					database, e := localindex.OpenDatabase(ctx, localindex.DatabaseOptions{Path: filepath.Join(s.options.ConfigDir, "cc-haha", "db", "index-v1.sqlite"), Kind: localindex.Primary})
					if e != nil {
						t.Fatal(e)
					}
					bindings := []any{}
					for _, binding := range operation["bindings"].([]any) {
						if value, ok := binding.(string); ok {
							binding = expand(value)
						}
						bindings = append(bindings, binding)
					}
					e = database.Write(ctx, func(op *localindex.Operation) error { return op.Exec(operation["query"].(string), bindings...) })
					_ = database.Close()
					if e != nil {
						t.Fatal(e)
					}
				}
				var value any
				switch method {
				case "list":
					var o struct {
						Project       *string
						Limit, Offset *float64
					}
					_ = json.Unmarshal(rawLocal(row["options"]), &o)
					limit, offset := 50.0, 0.0
					if o.Limit != nil {
						limit = *o.Limit
					}
					if o.Offset != nil {
						offset = *o.Offset
					}
					rows, total, e := s.consumerList(ctx, o.Project, limit, offset)
					if e != nil {
						t.Fatal(e)
					}
					value = map[string]any{"sessions": rows, "total": total}
				case "summary":
					info, e := s.findSession(ctx, row["id"].(string))
					if e == nil {
						value = info
					}
				case "find":
					info, e := s.findSession(ctx, row["id"].(string))
					if e == nil {
						value = map[string]any{"filePath": info.Path, "projectDir": info.ProjectPath}
					}
				case "sidebar":
					all, e := s.allSessions(ctx)
					if e != nil {
						t.Fatal(e)
					}
					writer := httptest.NewRecorder()
					limit := row["limit"].(int)
					request := httptest.NewRequest("GET", "/api/sessions?view=sidebar&perProjectLimit="+strconv.Itoa(limit), nil)
					if e = s.sidebarHTTP(writer, request, all); e != nil {
						t.Fatal(e)
					}
					var fields map[string]json.RawMessage
					_ = json.Unmarshal(writer.Body.Bytes(), &fields)
					delete(fields, "index")
					value = fields
				default:
					continue
				}
				actual := map[string]any{"result": plainConsumer(value, home)}
				var expected any
				if e := json.Unmarshal(source[seen], &expected); e != nil {
					t.Fatal(e)
				}
				if !reflect.DeepEqual(plainConsumer(actual, home), expected) {
					t.Fatalf("Session consumer step %d %s %s\nNative=%s\nSource=%s", seen, method, mode, rawLocal(actual), source[seen])
				}
				seen++

			}
			if seen != len(source) {
				t.Fatalf("Compared %d of %d actual Source observations", seen, len(source))
			}
			t.Logf("Compared all %d actual Source consumer observations", seen)
		})
	}
}
func rawLocal(value any) json.RawMessage { data, _ := json.Marshal(value); return data }

func TestSessionIndexActualHTTPMutationsAndPrivateMemory(t *testing.T) {
	s, index, _, _ := consumerFixture(t, "on")
	ctx := context.Background()
	if e := index.Start(ctx); e != nil {
		t.Fatal(e)
	}
	_ = index.WaitInitial(ctx)
	host := httptest.NewServer(s)
	defer host.Close()
	epoch := index.Epoch()
	id := createTestSession(t, host, s.options.WorkDir)
	if index.Epoch() == epoch || index.ConsumerAvailable() {
		t.Fatal("Real create did not invalidate the committed snapshot")
	}
	if e := index.Reconcile(ctx, localindex.ReconciliationBatch{FullSweep: true}); e != nil {
		t.Fatal(e)
	}
	info, e := s.findSession(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	appendRows(t, info.Path, `{"type":"user","uuid":"real-user","timestamp":"2026-10-06T00:00:00Z","message":{"role":"user","content":"real consumer"}}`)
	callback := s.sessionOptions(info).OnCanonicalTranscriptMutation
	if callback == nil {
		t.Fatal("Physical record notification not exposed")
	}
	if e = callback(info.Path); e != nil {
		t.Fatal(e)
	}
	if index.ConsumerAvailable() {
		t.Fatal("Unreconciled actual append used as a ready index")
	}
	_ = index.Reconcile(ctx, localindex.ReconciliationBatch{Paths: []string{info.Path}})
	raw := requestJSON(t, host, "GET", "/api/sessions?limit=1", "", 200)
	var page struct {
		Sessions []sessionInfo
		Total    int
		Index    localindex.Status
	}
	_ = json.Unmarshal(raw, &page)
	if page.Total != 1 || len(page.Sessions) != 1 || page.Sessions[0].Title != "real consumer" || page.Index.State != "ready" {
		t.Fatalf("Actual primary sidebar not consumed %s", raw)
	}
	epoch = index.Epoch()
	requestJSON(t, host, "PATCH", "/api/sessions/"+id, `{"title":"real rename"}`, 200)
	if index.Epoch() == epoch || index.ConsumerAvailable() {
		t.Fatal("Real title mutation did not invalidate")
	}
	row, e := index.Get(ctx, id)
	if e != nil || row == nil || indexedValue(*row, "title") != "real rename" {
		t.Fatal("Authoritative tiny title not actually patched")
	}
	_ = index.Reconcile(ctx, localindex.ReconciliationBatch{Paths: []string{info.Path}})
	requestJSON(t, host, "GET", "/api/sessions?limit=1.9suffix&offset=0suffix", "", 200)
	epoch = index.Epoch()
	requestJSON(t, host, "DELETE", "/api/sessions/"+id, "", 200)
	if index.Epoch() == epoch || index.ConsumerAvailable() {
		t.Fatal("Actual deletion did not invalidate")
	}
	_ = index.Reconcile(ctx, localindex.ReconciliationBatch{Paths: []string{info.Path}})
	requestJSON(t, host, "GET", "/api/sessions/"+id+"/summary", "", 404)
	s.options.PersistSession = func(context.Context) (bool, error) { return false, nil }
	memoryID := createTestSession(t, host, s.options.WorkDir)
	memory, e := s.findSession(ctx, memoryID)
	if e != nil || !memory.memory {
		t.Fatal("Retention zero lost live memory session")
	}
	if _, e = os.Stat(memory.Path); !os.IsNotExist(e) {
		t.Fatal("Private memory created canonical transcript")
	}
	requestJSON(t, host, "PATCH", "/api/sessions/"+memoryID, `{"title":"private rename"}`, 200)
	requestJSON(t, host, "GET", "/api/sessions/"+memoryID+"/messages", "", 200)
	requestJSON(t, host, "GET", "/api/sessions/"+memoryID+"/summary", "", 404)
	rows, total, ok := s.indexedSessionList(ctx, localindex.SessionListOptions{})
	if !ok || total != 0 || len(rows) != 0 {
		t.Fatal("Memory-only session inserted into SQLite projection")
	}
	requestJSON(t, host, "GET", "/api/sessions?view=sidebar", "", 200)
}
func TestSessionIndexMissingUnrelatedTranscriptDoesNotBlockPage(t *testing.T) {
	s, index, _, _ := consumerFixture(t, "on")
	ctx := context.Background()
	id1, id2 := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	dir := filepath.Join(s.options.ConfigDir, "projects", "p")
	_ = os.MkdirAll(dir, 0700)
	for n, id := range []string{id1, id2} {
		data := fmt.Sprintf(`{"type":"user","cwd":%q,"timestamp":"2026-10-06T00:0%d:00Z","message":{"role":"user","content":"actual"}}`, s.options.WorkDir, n) + "\n"
		_ = os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(data), 0600)
	}
	_ = index.Start(ctx)
	_ = index.WaitInitial(ctx)
	_ = os.Remove(filepath.Join(dir, id1+".jsonl"))
	limit := 1.0
	rows, total, ok := s.indexedSessionList(ctx, localindex.SessionListOptions{Limit: &limit})
	if !ok || total != 2 || len(rows) != 1 || rows[0].ID != id2 {
		t.Fatalf("Missing unrelated row forced global scan %v %d %+v", ok, total, rows)
	}
}
