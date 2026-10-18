package ccrsessions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
)

func ccrFixture(t *testing.T, peer *httptest.Server) *Service {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"home", "work", "config", "temp"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	s, err := New(Options{BaseURL: peer.URL, CWD: filepath.Join(root, "work"), ConfigDir: filepath.Join(root, "config"), TempDir: filepath.Join(root, "temp"), SessionID: "owned-local", Model: "fixture-model", Environment: map[string]string{"HOME": filepath.Join(root, "home"), "PATH": "/usr/bin:/bin", "ANTHROPIC_BASE_URL": peer.URL, "GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_NOSYSTEM": "1", "GIT_AUTHOR_NAME": "Owned fixture", "GIT_AUTHOR_EMAIL": "owned@fixture.invalid", "GIT_COMMITTER_NAME": "Owned fixture", "GIT_COMMITTER_EMAIL": "owned@fixture.invalid"}, Credentials: func(context.Context, bool) (Credentials, error) {
		return Credentials{Token: "owned-oauth", Organization: "owned-org", Scopes: []string{"user:inference"}}, nil
	}, Settings: func(context.Context) (protocol.JSONObject, error) { return protocol.NewJSONObject(), nil }, GenerateTitle: func(context.Context, string) (Title, error) { return Title{"Owned title", "claude/owned-task"}, nil }, Wait: func(ctx context.Context, _ time.Duration) error { return ctx.Err() }, HTTP: peer.Client()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}
func objectJSON(value string) protocol.JSONObject {
	var out protocol.JSONObject
	_ = out.UnmarshalJSON([]byte(value))
	return out
}
func ccrJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}
func ccrGit(t *testing.T, s *Service, args ...string) string {
	t.Helper()
	result, err := s.git(context.Background(), args...)
	if err != nil || result.Code != 0 {
		t.Fatal(args, result, err)
	}
	return strings.TrimSpace(result.Stdout)
}

func TestCCRRepositoryParserHostPortsAliasesAndRepositoryValidation(t *testing.T) {
	for _, row := range []struct{ input, host, owner, name string }{
		{"git@github.com:Owner/Repo.git", "github.com", "Owner", "Repo"},
		{"https://ghe.example.com:8443/a/b.with.dot.git", "ghe.example.com:8443", "a", "b.with.dot"},
		{"ssh://git@ghe.example.com:2222/a/b", "ghe.example.com", "a", "b"},
		{"git://github.com/a/b", "github.com", "a", "b"},
		{"git@github.com-work:a/b", "", "", ""}, {"owner/repo", "", "", ""}, {"file:///private/a/b", "", "", ""},
	} {
		actual := ParseRepository(row.input)
		if row.host == "" {
			if actual != nil {
				t.Fatal(row, actual)
			}
		} else if actual == nil || *actual != (Repository{row.host, row.owner, row.name}) {
			t.Fatal(row, actual)
		}
	}
	peer := httptest.NewServer(http.NotFoundHandler())
	defer peer.Close()
	s := ccrFixture(t, peer)
	ccrGit(t, s, "init", "--initial-branch=main")
	ccrGit(t, s, "remote", "add", "origin", "git@ghe.example.com:A/B.git")
	for _, row := range []struct{ remote, want string }{{"https://ghe.example.com:8443/a/b", "match"}, {"https://github.com/a/b", "mismatch"}, {"https://ghe.example.com/a/c", "mismatch"}, {"unparsed", "no_repo_required"}} {
		body := objectJSON(`{"session_context":{"sources":[{"type":"git_repository","url":"` + row.remote + `"}]}}`)
		actual := s.ValidateRepository(context.Background(), body)
		if actual.Status != row.want {
			t.Fatal(row, actual)
		}
		if (actual.Error("remote-session") == nil) != (row.want == "match" || row.want == "no_repo_required") {
			t.Fatal(actual)
		}
	}
	if e := (RepositoryValidation{Status: "not_in_repo", SessionRepo: "a/b", SessionHost: "ghe.example.com"}).Error("session"); !strings.Contains(e.Error(), "ghe.example.com/a/b") {
		t.Fatal(e)
	}
}

func TestCCRRemoteCreateConsumesActualGitOAuthPreflightEnvironmentAndEvents(t *testing.T) {
	var posted protocol.JSONObject
	var titleCalls, preflight atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer owned-oauth" || r.Header.Get("x-organization-uuid") != "owned-org" || r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Error("wrong OAuth credentials", r.Header)
		}
		switch r.URL.Path {
		case "/api/oauth/organizations/owned-org/code/repos/a/b":
			preflight.Add(1)
			ccrJSON(w, map[string]any{"status": map[string]bool{"app_installed": false}})
		case "/v1/environment_providers":
			ccrJSON(w, map[string]any{"environments": []any{map[string]string{"environment_id": "bridge", "kind": "bridge"}, map[string]string{"environment_id": "cloud", "kind": "anthropic_cloud"}, map[string]string{"environment_id": "configured", "kind": "byoc"}}})
		case "/v1/sessions":
			if r.Method != "POST" || r.Header.Get("anthropic-beta") != "ccr-byoc-2025-07-29" {
				t.Error(r.Method, r.Header)
			}
			data, _ := io.ReadAll(r.Body)
			_ = posted.UnmarshalJSON(data)
			w.WriteHeader(201)
			ccrJSON(w, map[string]string{"id": "remote-created", "title": "Server accepted title"})
		default:
			t.Error("unexpected actual endpoint", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer peer.Close()
	s := ccrFixture(t, peer)
	s.options.Settings = func(context.Context) (protocol.JSONObject, error) {
		return objectJSON(`{"remote":{"defaultEnvironmentId":"configured"}}`), nil
	}
	s.options.GenerateTitle = func(_ context.Context, description string) (Title, error) {
		titleCalls.Add(1)
		if description != "actual remote work" {
			t.Error(description)
		}
		return Title{"Generated title", "claude/generated"}, nil
	}
	ccrGit(t, s, "init", "--initial-branch=main")
	ccrGit(t, s, "remote", "add", "origin", "https://github.com/a/b.git")
	before := ccrGit(t, s, "branch", "--show-current")
	created, err := s.CreateRemote(context.Background(), "actual remote work")
	if err != nil || created.ID != "remote-created" || created.Title != "Server accepted title" || titleCalls.Load() != 1 || preflight.Load() != 1 {
		t.Fatal(created, err, titleCalls.Load(), preflight.Load())
	}
	ctx, _ := posted.Object("session_context")
	var sources, outcomes, events []protocol.JSONObject
	_ = json.Unmarshal(ctx.Get("sources"), &sources)
	_ = json.Unmarshal(ctx.Get("outcomes"), &outcomes)
	_ = json.Unmarshal(posted.Get("events"), &events)
	if posted.String("environment_id") != "configured" || posted.String("title") != "Generated title" || len(sources) != 1 || sources[0].String("url") != "https://github.com/a/b" || sources[0].String("revision") != "main" || ctx.String("model") != "fixture-model" || len(events) != 1 || len(outcomes) != 1 {
		t.Fatal(posted)
	}
	event, _ := events[0].Object("data")
	message, _ := event.Object("message")
	if event.String("type") != "user" || message.String("content") != "actual remote work" || event.String("uuid") == "" || string(event.Get("parent_tool_use_id")) != "null" || ccrGit(t, s, "branch", "--show-current") != before {
		t.Fatal("remote user event or local branch changed", posted)
	}
}

func TestCCRRemoteBundleCapturesTrackedWIPLeavesWorkingTreeAndCleansSeedRefs(t *testing.T) {
	var uploaded []byte
	var posted protocol.JSONObject
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/files":
			if r.Header.Get("anthropic-beta") != "files-api-2025-04-14,oauth-2025-04-20" || r.Header.Get("x-organization-uuid") != "" {
				t.Error(r.Header)
			}
			file, _, err := r.FormFile("file")
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			uploaded, _ = io.ReadAll(file)
			_ = file.Close()
			ccrJSON(w, map[string]string{"id": "file_seed"})
		case "/v1/environment_providers":
			ccrJSON(w, map[string]any{"environments": []any{map[string]string{"environment_id": "cloud", "kind": "anthropic_cloud"}}})
		case "/v1/sessions":
			data, _ := io.ReadAll(r.Body)
			_ = posted.UnmarshalJSON(data)
			ccrJSON(w, map[string]string{"id": "bundle-session"})
		default:
			t.Error(r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer peer.Close()
	s := ccrFixture(t, peer)
	s.options.Environment["CCR_ENABLE_BUNDLE"] = "1"
	ccrGit(t, s, "init", "--initial-branch=main")
	tracked := filepath.Join(s.options.CWD, "tracked.txt")
	_ = os.WriteFile(tracked, []byte("committed"), 0600)
	ccrGit(t, s, "add", "tracked.txt")
	ccrGit(t, s, "commit", "-m", "owned initial")
	_ = os.WriteFile(tracked, []byte("tracked WIP retained"), 0600)
	_ = os.WriteFile(filepath.Join(s.options.CWD, "untracked.txt"), []byte("untracked remains"), 0600)
	head := ccrGit(t, s, "rev-parse", "HEAD")
	status := ccrGit(t, s, "status", "--porcelain")
	created, err := s.CreateRemote(context.Background(), "bundle current local work")
	if err != nil || created.ID != "bundle-session" || !bytes.HasPrefix(uploaded, []byte("# v2 git bundle")) {
		t.Fatal(created, err, len(uploaded))
	}
	ctx, _ := posted.Object("session_context")
	if ctx.String("seed_bundle_file_id") != "file_seed" || string(ctx.Get("sources")) != "[]" || ccrGit(t, s, "rev-parse", "HEAD") != head || ccrGit(t, s, "status", "--porcelain") != status || ccrGit(t, s, "for-each-ref", "refs/seed/") != "" {
		t.Fatal("bundle did not retain local state/clean owned refs", posted)
	}
	data, err := os.ReadFile(tracked)
	if err != nil || string(data) != "tracked WIP retained" {
		t.Fatal(err, string(data))
	}
	if _, err := s.createAndUploadBundle(context.Background(), Credentials{Token: "owned-oauth"}, 1); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatal("oversized bundle was not rejected", err)
	}
	if ccrGit(t, s, "for-each-ref", "refs/seed/") != "" {
		t.Fatal("fallback left seed refs")
	}
}

func TestCCRTeleportPagingFiltersSidechainsFallbackAndAuthenticationErrors(t *testing.T) {
	for _, mode := range []string{"paged", "legacy", "expired", "mid404"} {
		t.Run(mode, func(t *testing.T) {
			var legacy atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "teleport-events") {
					if mode == "legacy" {
						w.WriteHeader(404)
						return
					}
					if mode == "expired" {
						w.WriteHeader(401)
						return
					}
					if r.URL.Query().Get("cursor") == "next cursor" {
						if mode == "mid404" {
							w.WriteHeader(404)
							return
						}
						ccrJSON(w, map[string]any{"data": []any{map[string]any{"payload": map[string]any{"type": "assistant", "uuid": "a"}}, map[string]any{"payload": nil}}, "next_cursor": nil})
						return
					}
					if r.URL.Query().Get("limit") != "1000" {
						t.Error(r.URL)
					}
					ccrJSON(w, map[string]any{"data": []any{map[string]any{"payload": map[string]any{"type": "user", "uuid": "u"}}, map[string]any{"payload": map[string]any{"type": "user", "isSidechain": true}}, map[string]any{"payload": map[string]any{"type": "progress"}}}, "next_cursor": "next cursor"})
					return
				}
				legacy.Add(1)
				ccrJSON(w, map[string]any{"loglines": []any{map[string]any{"type": "user", "uuid": "old"}, map[string]any{"type": "attachment", "uuid": "file"}}})
			}))
			defer peer.Close()
			s := ccrFixture(t, peer)
			rows, err := s.FetchTranscript(context.Background(), "remote-id")
			if mode == "expired" {
				if err == nil || !strings.Contains(err.Error(), "expired") || legacy.Load() != 0 {
					t.Fatal(rows, err, legacy.Load())
				}
				return
			}
			want := 2
			if mode == "mid404" {
				want = 1
			}
			if err != nil || len(rows) != want || (legacy.Load() != 0) != (mode == "legacy") {
				t.Fatal(rows, err, legacy.Load())
			}
		})
	}
}

func TestCCRPolicyOldFixtureETagStaleDenialEligibilityAndCacheUpdates(t *testing.T) {
	for _, mode := range []string{"304", "failure", "ineligible", "404", "fresh"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/api/claude_code/policy_limits" {
					t.Error(r.URL)
				}
				if !strings.HasPrefix(r.Header.Get("If-None-Match"), `"sha256:`) {
					t.Error("Source cache checksum missing", r.Header)
				}
				switch mode {
				case "304":
					w.WriteHeader(304)
				case "failure":
					w.WriteHeader(503)
				case "404":
					w.WriteHeader(404)
				default:
					ccrJSON(w, map[string]any{"restrictions": map[string]any{"allow_remote_sessions": map[string]bool{"allowed": true}}})
				}
			}))
			defer peer.Close()
			s := ccrFixture(t, peer)
			s.options.Credentials = func(context.Context, bool) (Credentials, error) {
				return Credentials{Token: "owned-oauth", Organization: "owned-org", Subscription: "team", Scopes: []string{"user:inference"}}, nil
			}
			if mode != "ineligible" {
				delete(s.options.Environment, "ANTHROPIC_BASE_URL")
			}
			path := filepath.Join(s.options.ConfigDir, "policy-limits.json")
			_ = os.WriteFile(path, []byte(`{"restrictions":{"allow_remote_sessions":{"allowed":false},"other":{"allowed":true}}}`), 0600)
			allowed, err := s.PolicyRemoteAllowed(context.Background())
			want := mode == "fresh" || mode == "404" || mode == "ineligible"
			if err != nil || allowed != want || mode == "ineligible" && calls.Load() != 0 || mode == "failure" && calls.Load() != 6 {
				t.Fatal(allowed, err, calls.Load())
			}
			if mode == "404" {
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("Source empty cache not removed", err)
				}
			}
			initialCalls := calls.Load()
			if again, failure := s.PolicyRemoteAllowed(context.Background()); failure != nil || again != allowed || calls.Load() != initialCalls {
				t.Fatal("one startup policy promise refetched between remote stages", again, failure, calls.Load(), initialCalls)
			}
		})
	}
}

func TestCCRActualHTTPRuntimeCloseCancelsAndJoinsInflightRequest(t *testing.T) {
	started, closed := make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(closed) }))
	defer peer.Close()
	s := ccrFixture(t, peer)
	done := make(chan error, 1)
	go func() { _, err := s.FetchSession(context.Background(), "pending"); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("HTTP request never started")
	}
	s.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("closed CCR request succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not join request")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("HTTP peer did not see cancel")
	}
}

func TestCCRPolicyPollingRefreshesSnapshotAndRuntimeCloseJoinsHTTP(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			ccrJSON(w, map[string]any{"restrictions": map[string]any{"allow_remote_sessions": map[string]bool{"allowed": false}}})
			return
		}
		close(started)
		<-r.Context().Done()
		close(cancelled)
	}))
	defer peer.Close()
	s := ccrFixture(t, peer)
	delete(s.options.Environment, "ANTHROPIC_BASE_URL")
	s.options.PolicyPollInterval = 10 * time.Millisecond
	s.options.Credentials = func(context.Context, bool) (Credentials, error) {
		return Credentials{Token: "owned", Subscription: "team", Scopes: []string{"user:inference"}}, nil
	}
	if allowed, err := s.PolicyRemoteAllowed(context.Background()); err != nil || allowed {
		t.Fatal(allowed, err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Source hourly producer did not invoke real HTTP under substituted clock interval")
	}
	s.Close()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("polling HTTP did not observe runtime close")
	}
	if _, err := s.PolicyRemoteAllowed(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatal("closed policy snapshot stayed usable", err)
	}
}

func TestCCRMissingAccountAndNoDescriptionCannotCreateSessionOrCallTitle(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unauthorized request", r.URL.Path) }))
	defer peer.Close()
	s := ccrFixture(t, peer)
	s.options.Credentials = func(context.Context, bool) (Credentials, error) { return Credentials{}, nil }
	if _, err := s.CreateRemote(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "requires a description") {
		t.Fatal(err)
	}
	if _, err := s.CreateRemote(context.Background(), "task"); err == nil || err.Error() != AuthenticationRequired {
		t.Fatal(err)
	}
	if _, err := s.Teleport(context.Background(), "", false); err == nil || !strings.Contains(err.Error(), "No session ID") {
		t.Fatal(err)
	}
	if _, err := New(Options{}); err == nil {
		t.Fatal("missing external authorities admitted")
	}
}
