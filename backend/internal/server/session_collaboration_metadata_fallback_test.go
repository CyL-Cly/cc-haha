package server

// Exercise the real Coordinator, transcript projection, private home, and
// public collaboration route when metadata indexing cannot serve a request.
// No provider is called. SQL below changes only the disposable index fixture;
// HTTP responses always come from the production collaboration route.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
)

type collaborationFallbackRow struct {
	ID          string `json:"id"`
	SessionID   string `json:"sessionId"`
	Title       string `json:"title"`
	ProjectPath string `json:"projectPath"`
}

type collaborationFallbackPage struct {
	Sessions []collaborationFallbackRow `json:"sessions"`
	Total    int                        `json:"total"`
}

type collaborationFallbackHTTP struct {
	server *Server
	host   *httptest.Server
	id     string
	token  string
}

func collaborationFallbackHost(t *testing.T, server *Server) *collaborationFallbackHTTP {
	t.Helper()
	opened := make(chan SessionOptions, 1)
	server.options.Factory = func(_ context.Context, options SessionOptions, _ Emit) (LiveSession, error) {
		opened <- options
		return &transportSession{}, nil
	}
	// Obtain the actual factory credential without adding a transcript that
	// would contaminate the candidate list or the real index's fixture rows.
	server.options.PersistSession = func(context.Context) (bool, error) { return false, nil }
	host := httptest.NewServer(server)
	t.Cleanup(host.Close)
	server.setCollaborationEndpoint(host.Listener.Addr())
	id := createTestSession(t, host, server.options.WorkDir)
	if _, err := server.live(id); err != nil {
		t.Fatal(err)
	}
	var options SessionOptions
	select {
	case options = <-opened:
	case <-time.After(5 * time.Second):
		t.Fatal("actual session factory did not publish its bridge credential")
	}
	token := options.Environment["CC_HAHA_SESSION_COLLABORATION_TOKEN"]
	if token == "" || options.Environment["CC_HAHA_DESKTOP_SERVER_URL"] != host.URL {
		t.Fatal("actual session factory lacks its owned collaboration bridge")
	}
	return &collaborationFallbackHTTP{server: server, host: host, id: id, token: token}
}

func (fixture *collaborationFallbackHTTP) request(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, fixture.host.URL+"/api/session-collaboration/list", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Session-Id", fixture.id)
	request.Header.Set("Authorization", "Bearer "+fixture.token)
	return request
}

func (fixture *collaborationFallbackHTTP) list(t *testing.T, body string, status int) json.RawMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, fixture.host.URL+"/api/session-collaboration/list", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header = fixture.request(body).Header.Clone()
	response, err := fixture.host.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != status {
		t.Fatalf("collaboration list: status=%d body=%s error=%v", response.StatusCode, data, err)
	}
	return data
}

func collaborationFallbackDecode(t *testing.T, data json.RawMessage) collaborationFallbackPage {
	t.Helper()
	var page collaborationFallbackPage
	if err := json.Unmarshal(data, &page); err != nil || page.Sessions == nil {
		t.Fatalf("missing actual candidate array: %s, error=%v", data, err)
	}
	return page
}

func collaborationFallbackWrite(t *testing.T, server *Server, project, id, title, work, stamp string) string {
	t.Helper()
	path := filepath.Join(server.options.ConfigDir, "projects", project, id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	user, err := json.Marshal(map[string]any{"type": "user", "timestamp": stamp, "cwd": work, "message": map[string]any{"role": "user", "content": title}})
	if err != nil {
		t.Fatal(err)
	}
	custom, err := json.Marshal(map[string]any{"type": "custom-title", "customTitle": title})
	if err != nil {
		t.Fatal(err)
	}
	data := append(append(append([]byte{}, user...), '\n'), append(custom, '\n')...)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	modified, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
	return path
}

func collaborationFallbackStart(t *testing.T, index *localindex.Coordinator) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := index.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := index.WaitInitial(ctx); err != nil {
		t.Fatal(err)
	}
	if !index.ConsumerReadAllowed() {
		t.Fatal("real initial index did not become readable")
	}
}

func collaborationFallbackSQL(t *testing.T, server *Server, statement string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	database, err := localindex.OpenDatabase(ctx, localindex.DatabaseOptions{Path: filepath.Join(server.options.ConfigDir, "cc-haha", "db", "index-v1.sqlite"), Kind: localindex.Primary})
	if err != nil {
		t.Fatal(err)
	}
	err = database.Write(ctx, func(operation *localindex.Operation) error { return operation.Exec(statement) })
	closeErr := database.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("disposable index fixture SQL: write=%v close=%v", err, closeErr)
	}
}

func TestCollaborationMetadataPublicMissingOrDisabledIndexUsesOnlyShallowTranscripts(t *testing.T) {
	for _, mode := range []string{"on", "off", "shadow"} {
		t.Run(mode, func(t *testing.T) {
			server, index, _, _ := consumerFixture(t, mode)
			fixture := collaborationFallbackHost(t, server)
			page := collaborationFallbackDecode(t, fixture.list(t, `{}`, 200))
			if len(page.Sessions) != 0 || page.Total != 0 {
				t.Fatalf("missing projects directory must be an empty list: %+v", page)
			}
			path := collaborationFallbackWrite(t, server, "p", "included", "actual disk title", server.options.WorkDir, "2026-10-01T00:00:00Z")
			// A root-level transcript, a deeper transcript and a symlink must
			// not become candidates through this fallback's shallow boundary.
			root := filepath.Join(server.options.ConfigDir, "projects")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, ignored := range []string{filepath.Join(root, "root-only.jsonl"), filepath.Join(root, "p", "nested", "deep.jsonl")} {
				if err := os.MkdirAll(filepath.Dir(ignored), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(ignored, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(path, filepath.Join(root, "p", "linked.jsonl")); err != nil {
				t.Fatal(err)
			}
			if mode == "shadow" {
				collaborationFallbackStart(t, index)
				// Prove shadow reads the real transcript instead of returning
				// an otherwise valid but deliberately distinguishable SQL row.
				collaborationFallbackSQL(t, server, "UPDATE sessions SET title='shadow index title'")
			}
			page = collaborationFallbackDecode(t, fixture.list(t, `{}`, 200))
			if page.Total != 1 || len(page.Sessions) != 1 || page.Sessions[0].ID != "included" || page.Sessions[0].Title != "actual disk title" {
				t.Fatalf("shallow disk fallback returned foreign/stale candidates: %+v", page)
			}
		})
	}
}

func TestCollaborationMetadataPublicFallbackRanksAndPaginatesDeterministically(t *testing.T) {
	server, _, home, _ := consumerFixture(t, "off")
	fixture := collaborationFallbackHost(t, server)
	workMatch := filepath.Join(home, "needle-workspace")
	for _, row := range []struct{ project, id, title, work, stamp string }{
		{"p", "older-exact", "needle", server.options.WorkDir, "2026-10-01T00:00:00Z"},
		{"p", "newer-exact", "NEEDLE", server.options.WorkDir, "2026-10-02T00:00:00Z"},
		{"p", "needle-prefix", "other topic", server.options.WorkDir, "2026-10-03T00:00:00Z"},
		{"p", "contains", "my needle topic", server.options.WorkDir, "2026-10-04T00:00:00Z"},
		{"p", "a-work", "unrelated", workMatch, "2026-10-05T00:00:00Z"},
		{"p", "b-work", "unrelated", workMatch, "2026-10-05T00:00:00Z"},
		{"p", "same", "unrelated", workMatch, "2026-10-05T00:00:00Z"},
		{"q", "same", "unrelated", workMatch, "2026-10-05T00:00:00Z"},
	} {
		collaborationFallbackWrite(t, server, row.project, row.id, row.title, row.work, row.stamp)
	}
	// Literal expectations independently express exact > prefix > substring >
	// workDir-only matches, then timestamp, ID and project tie-breaks. A full
	// one-item metadata page intentionally does not require a Search provider.
	want := []string{"p/newer-exact", "p/older-exact", "p/needle-prefix", "p/contains", "p/a-work", "p/b-work", "p/same", "q/same"}
	got := []string{}
	for offset := range want {
		body, err := json.Marshal(map[string]any{"query": "\uFEFF NeEdLe \u00A0", "limit": 1, "offset": offset})
		if err != nil {
			t.Fatal(err)
		}
		page := collaborationFallbackDecode(t, fixture.list(t, string(body), 200))
		if page.Total != len(want) || len(page.Sessions) != 1 {
			t.Fatalf("rank page %d: %+v", offset, page)
		}
		got = append(got, page.Sessions[0].ProjectPath+"/"+page.Sessions[0].ID)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rank/page boundary: got=%v want=%v", got, want)
	}
	page := collaborationFallbackDecode(t, fixture.list(t, `{"limit":2,"offset":1}`, 200))
	if page.Total != 8 || len(page.Sessions) != 2 || page.Sessions[0].ID != "b-work" || page.Sessions[1].ID != "same" || page.Sessions[1].ProjectPath != "p" {
		t.Fatalf("empty-query date/ID/project pagination: %+v", page)
	}
	page = collaborationFallbackDecode(t, fixture.list(t, `{"limit":2,"offset":99}`, 200))
	if page.Total != 8 || len(page.Sessions) != 0 {
		t.Fatalf("offset past end changed the total or leaked rows: %+v", page)
	}
}

func TestCollaborationMetadataPublicInvalidatedIndexFallsBackThenReconciles(t *testing.T) {
	server, index, _, _ := consumerFixture(t, "on")
	fixture := collaborationFallbackHost(t, server)
	path := collaborationFallbackWrite(t, server, "p", "changing", "indexed old title", server.options.WorkDir, "2026-10-01T00:00:00Z")
	collaborationFallbackStart(t, index)
	before := collaborationFallbackDecode(t, fixture.list(t, `{}`, 200))
	if len(before.Sessions) != 1 || before.Sessions[0].Title != "indexed old title" {
		t.Fatalf("initial real indexed row missing: %+v", before)
	}
	epoch := index.Epoch()
	collaborationFallbackWrite(t, server, "p", "changing", "new disk title after mutation", server.options.WorkDir, "2026-10-02T00:00:00Z")
	server.noteSessionMutation(path)
	if index.Epoch() == epoch || index.ConsumerAvailable() {
		t.Fatal("canonical transcript mutation did not invalidate the real index")
	}
	page := collaborationFallbackDecode(t, fixture.list(t, `{}`, 200))
	if page.Total != 1 || len(page.Sessions) != 1 || page.Sessions[0].Title != "new disk title after mutation" {
		t.Fatalf("invalidated index exposed its old title: %+v", page)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := index.Reconcile(ctx, localindex.ReconciliationBatch{Paths: []string{path}}); err != nil {
		t.Fatal(err)
	}
	if !index.ConsumerAvailable() {
		t.Fatal("reconciliation did not restore real index availability")
	}
	after := collaborationFallbackDecode(t, fixture.list(t, `{}`, 200))
	if !reflect.DeepEqual(after, page) {
		t.Fatalf("reconciled index disagrees with its disk fallback: before=%+v after=%+v", page, after)
	}
}

func TestCollaborationMetadataPublicActualIndexReadErrorFallsBackToDisk(t *testing.T) {
	server, index, _, _ := consumerFixture(t, "on")
	fixture := collaborationFallbackHost(t, server)
	collaborationFallbackWrite(t, server, "p", "disk-survivor", "disk survivor title", server.options.WorkDir, "2026-10-01T00:00:00Z")
	collaborationFallbackStart(t, index)
	collaborationFallbackSQL(t, server, "UPDATE sessions SET title='index-only marker'")
	indexed := collaborationFallbackDecode(t, fixture.list(t, `{}`, 200))
	if len(indexed.Sessions) != 1 || indexed.Sessions[0].Title != "index-only marker" {
		t.Fatalf("test failed to reach the actual index reader: %+v", indexed)
	}
	// Break only the disposable database's query schema. The transcript and
	// coordinator state are untouched, so the next HTTP call must encounter
	// a real SQL read error before it can degrade and scan canonical files.
	collaborationFallbackSQL(t, server, "ALTER TABLE sessions RENAME TO fixture_unavailable_sessions")
	page := collaborationFallbackDecode(t, fixture.list(t, `{}`, 200))
	if page.Total != 1 || len(page.Sessions) != 1 || page.Sessions[0].ID != "disk-survivor" || page.Sessions[0].Title != "disk survivor title" {
		t.Fatalf("real index read failure hid its canonical disk row: %+v", page)
	}
	if status := index.Status(); status.State != "degraded" || status.LastErrorCode == nil {
		t.Fatalf("real index error was not recorded: %+v", status)
	}
}

func TestCollaborationMetadataCancellationDeadlineAndUnavailableSearchAreExplicit(t *testing.T) {
	server, _, _, _ := consumerFixture(t, "off")
	fixture := collaborationFallbackHost(t, server)
	collaborationFallbackWrite(t, server, "p", "retained", "retained disk title", server.options.WorkDir, "2026-10-01T00:00:00Z")
	before := collaborationFallbackDecode(t, fixture.list(t, `{}`, 200))
	if len(before.Sessions) != 1 || before.Sessions[0].ID != "retained" {
		t.Fatalf("cancellation fixture missing: %+v", before)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("owned collaboration request cancelled")
	cancel(cause)
	request := fixture.request(`{}`).WithContext(ctx)
	recorder := httptest.NewRecorder()
	// Exercise the actual public handler with a cancelled request; a TCP
	// client cancelled before sending cannot reliably observe server's 499.
	server.ServeHTTP(recorder, request)
	if recorder.Code != 499 || recorder.Body.Len() != 0 {
		t.Fatalf("cancelled public list: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	// The deadline is internal to the metadata producer, not a public option.
	// Test it directly against the real directory without inventing a clock
	// seam or requiring a real scan to finish within a scheduling window.
	limited, err := server.searchCollaborationMetadata(context.Background(), "", 10, 0, float64(time.Now().UnixMilli()-1))
	if err != nil || string(limited.Get("truncated")) != "true" || string(limited.Get("sessions")) != "[]" || string(limited.Get("total")) != "0" {
		t.Fatalf("past deadline must explicitly truncate, not return complete data: result=%v error=%v", limited, err)
	}
	_, err = server.searchCollaborationMetadata(ctx, "", 10, 0, float64(time.Now().UnixMilli()+10000))
	if !errors.Is(err, cause) {
		t.Fatalf("metadata scan discarded cancellation cause: %v", err)
	}
	failed := fixture.list(t, `{"query":"absent-body-only-term"}`, 501)
	if !strings.Contains(string(failed), "actual collaboration full-text suggestions") || strings.Contains(string(failed), `"sessions"`) {
		t.Fatalf("unconfigured full-text search became a successful empty list: %s", failed)
	}
	after := collaborationFallbackDecode(t, fixture.list(t, `{}`, 200))
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("cancelled/truncated/error reads mutated retained candidates: before=%+v after=%+v", before, after)
	}
}
