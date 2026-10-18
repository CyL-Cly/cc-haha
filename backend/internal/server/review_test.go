package server

// These regressions exercise the public Go HTTP handler and real disposable Git
// repositories. They neither open an Agent nor call a provider. The first GET
// below is expected to fail with the existing 501 before routing is integrated.
import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Independent wire structs let this test file compile on the original server,
// before any production patch. TestGitReviewPublicStatusRevisionAndExplicitDiff
// can therefore demonstrate the intended HTTP 501 failure rather than a missing
// Go symbol. The remaining tests exercise the integrated implementation.
type reviewTestSource struct {
	Kind         string `json:"kind"`
	BaseRef      string `json:"baseRef,omitempty"`
	Commit       string `json:"commit,omitempty"`
	ResolvedBase string `json:"resolvedBase,omitempty"`
}
type reviewTestRevision struct {
	State    string           `json:"state"`
	Source   reviewTestSource `json:"source"`
	Snapshot string           `json:"snapshot"`
	Error    string           `json:"error,omitempty"`
}
type reviewTestFile struct {
	Path           string `json:"path"`
	OldPath        string `json:"oldPath,omitempty"`
	Status         string `json:"status"`
	Additions      int    `json:"additions"`
	Deletions      int    `json:"deletions"`
	Binary         bool   `json:"binary"`
	Staged         bool   `json:"staged"`
	Unstaged       bool   `json:"unstaged"`
	Conflicted     bool   `json:"conflicted"`
	StatsTruncated bool   `json:"statsTruncated,omitempty"`
}
type reviewTestStatus struct {
	reviewTestRevision
	Files     []reviewTestFile `json:"files"`
	Untracked []string         `json:"untracked"`
}
type reviewTestDiff struct {
	reviewTestRevision
	Path      string  `json:"path"`
	Diff      *string `json:"diff,omitempty"`
	Binary    *bool   `json:"binary,omitempty"`
	Truncated bool    `json:"truncated,omitempty"`
	Bytes     int64   `json:"bytes,omitempty"`
}
type reviewTestWriteResult struct {
	State    string `json:"state"`
	Snapshot string `json:"snapshot"`
	Results  []struct {
		Path   string `json:"path"`
		OK     bool   `json:"ok"`
		Action string `json:"action,omitempty"`
		Error  string `json:"error,omitempty"`
	} `json:"results"`
	Status        *reviewTestStatus `json:"status,omitempty"`
	BackupDir     string            `json:"backupDir,omitempty"`
	RevertedPaths []string          `json:"revertedPaths,omitempty"`
	DeletedPaths  []string          `json:"deletedPaths,omitempty"`
	Error         string            `json:"error,omitempty"`
}

type reviewFixture struct {
	t           *testing.T
	server      *Server
	host        *httptest.Server
	options     Options
	id, base    string
	environment map[string]string
}

func newReviewFixture(t *testing.T, commit bool) *reviewFixture {
	t.Helper()
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		t.Fatal("Git Review opened an Agent")
		return nil, nil
	})
	t.Setenv("TMPDIR", options.HomeDir)
	t.Setenv("TMP", options.HomeDir)
	t.Setenv("TEMP", options.HomeDir)
	env := map[string]string{"PATH": os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": filepath.Join(options.HomeDir, "no-global"), "GIT_AUTHOR_NAME": "Fixture", "GIT_AUTHOR_EMAIL": "fixture@example.invalid", "GIT_COMMITTER_NAME": "Fixture", "GIT_COMMITTER_EMAIL": "fixture@example.invalid", "GIT_AUTHOR_DATE": "2026-01-01T00:00:00+00:00", "GIT_COMMITTER_DATE": "2026-01-01T00:00:00+00:00"}
	options.Environment = func() map[string]string { return env }
	server, host := testHost(t, options)
	f := &reviewFixture{t: t, server: server, host: host, options: options, environment: env}
	f.git("init", "-q", "-b", "fixture")
	if commit {
		f.put("tracked.txt", "one\ntwo\n")
		f.git("add", "--", "tracked.txt")
		f.git("commit", "-q", "-m", "private baseline")
	}
	f.useWorkspace(options.WorkDir)
	return f
}
func (f *reviewFixture) git(args ...string) string {
	f.t.Helper()
	out := f.server.repositoryGit(context.Background(), f.options.WorkDir, args...)
	if out.code != 0 {
		f.t.Fatal(args, out)
	}
	return out.stdout
}
func (f *reviewFixture) put(path, text string) {
	f.t.Helper()
	writeSearchFixture(f.t, f.options.WorkDir, path, text)
}
func (f *reviewFixture) useWorkspace(path string) {
	f.id = createTestSession(f.t, f.host, path)
	f.base = "/api/sessions/" + f.id + "/review"
}
func (f *reviewFixture) request(method, suffix string, body any, status int) json.RawMessage {
	f.t.Helper()
	text := ""
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
		text = string(raw)
	}
	return requestJSON(f.t, f.host, method, f.base+suffix, text, status)
}
func (f *reviewFixture) status(source string) reviewTestStatus {
	f.t.Helper()
	var result reviewTestStatus
	raw := f.request("GET", "?source="+source, nil, 200)
	if err := json.Unmarshal(raw, &result); err != nil {
		f.t.Fatal(err, string(raw))
	}
	return result
}
func (f *reviewFixture) diff(source, path string) reviewTestDiff {
	f.t.Helper()
	var result reviewTestDiff
	raw := f.request("GET", "/diff?source="+source+"&path="+url.QueryEscape(path), nil, 200)
	if err := json.Unmarshal(raw, &result); err != nil {
		f.t.Fatal(err, string(raw))
	}
	return result
}
func (f *reviewFixture) write(resource string, input map[string]any) reviewTestWriteResult {
	f.t.Helper()
	var result reviewTestWriteResult
	raw := f.request("POST", "/"+resource, input, 200)
	if err := json.Unmarshal(raw, &result); err != nil {
		f.t.Fatal(err, string(raw))
	}
	return result
}
func reviewTestFileMap(status reviewTestStatus) map[string]reviewTestFile {
	result := map[string]reviewTestFile{}
	for _, file := range status.Files {
		result[file.Path] = file
	}
	return result
}
func reviewMustRead(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestGitReviewPublicStatusRevisionAndExplicitDiff(t *testing.T) {
	f := newReviewFixture(t, true)
	f.put("tracked.txt", "one\ntwo\nthree\n")
	f.put("fresh.txt", "fresh\n")
	// Intended initial failure: ordinary sessionsHTTP currently returns 501 here.
	status := f.status("unstaged")
	files := reviewTestFileMap(status)
	if status.State != "ok" || status.Snapshot == "" || len(files) != 2 || files["tracked.txt"].Additions != 1 || files["fresh.txt"].Status != "untracked" || len(status.Untracked) != 1 || status.Untracked[0] != "fresh.txt" {
		t.Fatalf("%+v", status)
	}
	var revision map[string]json.RawMessage
	raw := f.request("GET", "/revision?source=unstaged", nil, 200)
	_ = json.Unmarshal(raw, &revision)
	if stringField(revision, "snapshot") != status.Snapshot || stringField(revision, "state") != "ok" || revision["files"] != nil || revision["diff"] != nil {
		t.Fatal(string(raw))
	}
	diff := f.diff("unstaged", "tracked.txt")
	if diff.State != "ok" || diff.Diff == nil || !strings.Contains(*diff.Diff, "+three") || diff.Snapshot != status.Snapshot {
		t.Fatalf("%+v", diff)
	}
	f.request("POST", "/revision?source=unstaged", map[string]any{}, 405)
	f.git("add", "--", "tracked.txt")
	f.put("tracked.txt", "one\ntwo\nthree\nfour\n")
	staged := f.status("staged")
	unstaged := f.status("unstaged")
	combined := f.status("uncommitted")
	for _, status := range []reviewTestStatus{staged, unstaged, combined} {
		file := reviewTestFileMap(status)["tracked.txt"]
		if !file.Staged || !file.Unstaged {
			t.Fatalf("lost index/worktree halves: %+v", status)
		}
	}
	if len(staged.Untracked) != 0 || reviewTestFileMap(staged)["tracked.txt"].Additions != 1 || reviewTestFileMap(unstaged)["tracked.txt"].Additions != 1 || reviewTestFileMap(combined)["tracked.txt"].Additions != 2 {
		t.Fatal(staged, unstaged, combined)
	}
	if diff := f.diff("staged", "fresh.txt"); diff.State != "missing" {
		t.Fatal(diff)
	}
}
func TestGitReviewPublicSourcesHistoricalIsolationAndBranchRevision(t *testing.T) {
	f := newReviewFixture(t, true)
	root := strings.TrimSpace(f.git("rev-parse", "HEAD"))
	f.git("branch", "comparison")
	f.put("tracked.txt", "second\n")
	f.git("commit", "-q", "-a", "-m", "second")
	branch := f.status("branch&baseRef=comparison")
	if branch.State != "ok" || branch.Source.ResolvedBase != root || len(branch.Files) != 1 {
		t.Fatal(branch)
	}
	f.git("branch", "-f", "comparison", "HEAD")
	moved := f.status("branch&baseRef=comparison")
	if len(moved.Files) != 0 || moved.Snapshot == branch.Snapshot {
		t.Fatal(moved)
	}
	f.git("rm", "-q", "--", "tracked.txt")
	f.git("commit", "-q", "-m", "deleted")
	f.put("tracked.txt/child", "unrelated live directory\n")
	history := f.status("commit&commit=" + root)
	diff := f.diff("commit&commit="+root, "tracked.txt")
	if history.State != "ok" || len(history.Files) != 1 || history.Source.Commit != root || diff.Diff == nil || !strings.Contains(*diff.Diff, "+one") || strings.Contains(*diff.Diff, "unrelated") {
		t.Fatal(history, diff)
	}
	f.put("outside.txt", "new unrelated\n")
	if next := f.status("commit&commit=" + root); next.Snapshot != history.Snapshot {
		t.Fatal("historical revision followed working tree", history, next)
	}
	for _, source := range []string{"branch&baseRef=missing-ref", "commit&commit=missing-ref", "branch&baseRef=-bad"} {
		if status := f.status(source); status.State != "error" || status.Error == "" {
			t.Fatal(status)
		}
	}
}
func TestGitReviewPublicStageUnstageStaleAndBackedUpRevert(t *testing.T) {
	f := newReviewFixture(t, true)
	f.put("tracked.txt", "first edit\n")
	before := f.status("unstaged")
	staged := f.write("stage", map[string]any{"paths": []string{"tracked.txt"}, "snapshot": before.Snapshot, "source": reviewTestSource{Kind: "unstaged"}})
	if staged.State != "ok" || staged.Status == nil || len(staged.Results) != 1 || staged.Results[0].Action != "staged" || f.git("show", ":tracked.txt") != "first edit\n" {
		t.Fatal(staged)
	}
	index := f.status("staged")
	unstaged := f.write("unstage", map[string]any{"paths": []string{"tracked.txt"}, "snapshot": index.Snapshot, "source": reviewTestSource{Kind: "staged"}})
	if unstaged.State != "ok" || f.git("diff", "--cached") != "" || reviewMustRead(t, filepath.Join(f.options.WorkDir, "tracked.txt")) != "first edit\n" {
		t.Fatal(unstaged)
	}
	old := f.status("unstaged")
	f.put("tracked.txt", "a second, different size edit\n")
	stale := f.write("stage", map[string]any{"paths": []string{"tracked.txt"}, "snapshot": old.Snapshot})
	if stale.State != "stale" || len(stale.Results) != 0 || stale.Snapshot == old.Snapshot || f.git("diff", "--cached") != "" {
		t.Fatal(stale)
	}
	f.put("fresh.txt", "never stored by Git\n")
	status := f.status("unstaged")
	reverted := f.write("revert", map[string]any{"paths": []string{"tracked.txt", "fresh.txt"}, "snapshot": status.Snapshot, "source": reviewTestSource{Kind: "unstaged"}})
	if reverted.State != "ok" || len(reverted.RevertedPaths) != 1 || reverted.RevertedPaths[0] != "tracked.txt" || len(reverted.DeletedPaths) != 1 || reverted.DeletedPaths[0] != "fresh.txt" || reverted.BackupDir == "" {
		t.Fatal(reverted)
	}
	if reviewMustRead(t, filepath.Join(reverted.BackupDir, "tracked.txt")) != "a second, different size edit\n" || reviewMustRead(t, filepath.Join(reverted.BackupDir, "fresh.txt")) != "never stored by Git\n" || reviewMustRead(t, filepath.Join(f.options.WorkDir, "tracked.txt")) != "one\ntwo\n" {
		t.Fatal(reverted)
	}
	if _, err := os.Stat(filepath.Join(f.options.WorkDir, "fresh.txt")); !os.IsNotExist(err) {
		t.Fatal("untracked not explicitly deleted", err)
	}
	if runtime.GOOS != "windows" {
		directory, _ := os.Stat(reverted.BackupDir)
		file, _ := os.Stat(filepath.Join(reverted.BackupDir, "fresh.txt"))
		if directory.Mode().Perm() != 0700 || file.Mode().Perm() != 0600 {
			t.Fatal(directory.Mode(), file.Mode())
		}
	}
}
func TestGitReviewPublicLiteralPathsValidationAndReadOnlySources(t *testing.T) {
	f := newReviewFixture(t, true)
	names := []string{"a[1].txt", "a1.txt"}
	if runtime.GOOS != "windows" {
		names = append(names, ":literal.txt", "star*.txt", "star-other.txt")
	}
	for _, name := range names {
		f.put(name, "base\n")
	}
	f.git("add", "-A")
	f.git("commit", "-q", "-m", "literal filenames")
	f.put("a[1].txt", "requested\n")
	f.put("a1.txt", "must survive\n")
	status := f.status("unstaged")
	result := f.write("revert", map[string]any{"paths": []string{"a[1].txt"}, "snapshot": status.Snapshot})
	if result.State != "ok" || reviewMustRead(t, filepath.Join(f.options.WorkDir, "a1.txt")) != "must survive\n" {
		t.Fatal(result)
	}
	for _, path := range []string{"../outside.txt", ".git/config"} {
		f.request("POST", "/stage", map[string]any{"paths": []string{path}, "snapshot": "deliberately-stale"}, 403)
		f.request("GET", "/diff?source=unstaged&path="+url.QueryEscape(path), nil, 403)
	}
	for _, source := range []reviewTestSource{{Kind: "branch", BaseRef: "fixture"}, {Kind: "commit", Commit: "HEAD"}} {
		for _, resource := range []string{"stage", "unstage", "stage-hunk", "unstage-hunk", "revert"} {
			f.request("POST", "/"+resource, map[string]any{"paths": []string{"a1.txt"}, "patch": "x", "snapshot": "x", "source": source}, 400)
		}
	}
	for _, suffix := range []string{"", "?source=turn&turnKey=1", "?source=unknown", "?source=branch", "?source=commit", "/diff?source=unstaged"} {
		f.request("GET", suffix, nil, 400)
	}
	f.request("GET", "/unknown?source=unstaged", nil, 404)
	f.request("GET", "/stage", nil, 405)
	f.request("POST", "?source=unstaged", map[string]any{}, 405)
	f.request("POST", "/stage", []string{"array"}, 400)
	f.request("POST", "/stage", map[string]any{"snapshot": "x", "paths": []string{}}, 400)
	requestJSON(t, f.host, "GET", "/api/sessions/00000000-0000-4000-8000-000000000000/review?source=unstaged", "", 404)
	f.put("nested/file.txt", "ordinary\n")
	status = f.status("unstaged")
	partial := f.write("revert", map[string]any{"paths": []string{"nested", "a1.txt"}, "snapshot": status.Snapshot})
	if partial.State != "partial" || len(partial.Results) != 2 || partial.Results[0].OK || !partial.Results[1].OK || reviewMustRead(t, filepath.Join(f.options.WorkDir, "nested/file.txt")) != "ordinary\n" {
		t.Fatal(partial)
	}
}
func TestGitReviewPublicHunksKeepExactNewFileBytesAndModes(t *testing.T) {
	for _, example := range []struct {
		name, content string
		mode          os.FileMode
	}{{"empty.txt", "", 0644}, {"space name.txt", "without newline", 0644}, {"line\nbreak.txt", "one\r\ntwo\r\n", 0644}, {"文档/café.txt", "one\ntwo\n", 0644}, {"executable.sh", "#!/bin/sh\nprintf ok", 0755}} {
		t.Run(example.name, func(t *testing.T) {
			f := newReviewFixture(t, true)
			if runtime.GOOS == "windows" && strings.ContainsAny(example.name, "\n") {
				t.Skip("filename is not supported by Windows")
			}
			f.put(example.name, example.content)
			if err := os.Chmod(filepath.Join(f.options.WorkDir, example.name), example.mode); err != nil {
				t.Fatal(err)
			}
			diff := f.diff("unstaged", example.name)
			if diff.State != "ok" || diff.Diff == nil {
				t.Fatal(diff)
			}
			staged := f.write("stage-hunk", map[string]any{"patch": *diff.Diff, "snapshot": diff.Snapshot, "source": reviewTestSource{Kind: "unstaged"}})
			if staged.State != "ok" || f.git("show", ":"+example.name) != example.content {
				t.Fatal(staged)
			}
			if runtime.GOOS != "windows" {
				expected := "100644"
				if example.mode == 0755 {
					expected = "100755"
				}
				if !strings.HasPrefix(f.git("ls-files", "--stage", "--", ":(literal)"+example.name), expected+" ") {
					t.Fatal("lost executable bit")
				}
			}
		})
	}
	f := newReviewFixture(t, true)
	f.put("tracked.txt", "one\ntwo\nthree\n")
	diff := f.diff("unstaged", "tracked.txt")
	staged := f.write("stage-hunk", map[string]any{"patch": *diff.Diff, "snapshot": diff.Snapshot, "source": reviewTestSource{Kind: "unstaged"}})
	if staged.State != "ok" {
		t.Fatal(staged)
	}
	diff = f.diff("staged", "tracked.txt")
	result := f.write("unstage-hunk", map[string]any{"patch": *diff.Diff, "snapshot": diff.Snapshot, "source": reviewTestSource{Kind: "staged"}})
	if result.State != "ok" || f.git("diff", "--cached") != "" {
		t.Fatal(result)
	}
	status := f.status("unstaged")
	multi := f.write("stage-hunk", map[string]any{"patch": "diff --git a/a b/a\n--- a/a\n+++ b/a\ndiff --git a/b b/b\n--- a/b\n+++ b/b\n", "snapshot": status.Snapshot})
	if multi.State != "error" || !strings.Contains(multi.Error, "exactly one file") {
		t.Fatal(multi)
	}
	binary := f.write("stage-hunk", map[string]any{"patch": "diff --git a/tracked.txt b/tracked.txt\nGIT binary patch\n", "snapshot": status.Snapshot})
	if binary.State != "error" || !strings.Contains(binary.Error, "Binary file") {
		t.Fatal(binary)
	}
}
func TestGitReviewPublicUntrackedLimitsAndNoHead(t *testing.T) {
	f := newReviewFixture(t, false)
	f.put("fresh.txt", "a\r\nb\rc\n")
	f.put("binary.bin", string([]byte{0, 1, 2}))
	f.put("large.txt", strings.Repeat("line\n", 70000))
	path := filepath.Join(f.options.WorkDir, "too-large.txt")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Truncate(32*1024*1024 + 1); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	status := f.status("unstaged")
	byPath := reviewTestFileMap(status)
	if status.State != "ok" || byPath["fresh.txt"].Additions != 3 || !byPath["binary.bin"].Binary || byPath["large.txt"].Additions != 70000 || !byPath["too-large.txt"].StatsTruncated {
		t.Fatal(status)
	}
	large := f.diff("unstaged", "too-large.txt")
	if !large.Truncated || large.Bytes != 32*1024*1024+1 || large.Diff == nil || strings.Contains(*large.Diff, "@@") {
		t.Fatal(large)
	}
	noHead := f.status("staged")
	var revision reviewTestRevision
	_ = json.Unmarshal(f.request("GET", "/revision?source=staged", nil, 200), &revision)
	if noHead.State != "no_head" || noHead.Error == "" || revision.Snapshot != noHead.Snapshot || revision.State != "no_head" {
		t.Fatal(noHead, revision)
	}
	staged := f.write("stage", map[string]any{"paths": []string{"fresh.txt"}, "snapshot": status.Snapshot})
	if staged.State != "ok" {
		t.Fatal(staged)
	}
	snapshot := f.status("unstaged").Snapshot
	unstaged := f.write("unstage", map[string]any{"paths": []string{"fresh.txt"}, "snapshot": snapshot})
	if unstaged.State != "ok" || f.git("ls-files") != "" {
		t.Fatal(unstaged)
	}
}

func TestGitReviewPublicAppliesOnlySelectedHunkAndRespectsDisplayedSide(t *testing.T) {
	f := newReviewFixture(t, true)
	lines := []string{}
	for n := 0; n < 40; n++ {
		lines = append(lines, strings.Repeat("x", n+1))
	}
	f.put("tracked.txt", strings.Join(lines, "\n")+"\n")
	f.git("commit", "-q", "-a", "-m", "two separated hunk baseline")
	lines[1], lines[35] = "FIRST_SELECTED", "SECOND_LEFT_UNSTAGED"
	f.put("tracked.txt", strings.Join(lines, "\n")+"\n")
	diff := f.diff("unstaged", "tracked.txt")
	if diff.Diff == nil || strings.Count(*diff.Diff, "\n@@ ") != 2 {
		t.Fatal("fixture must contain two separate hunks", diff)
	}
	first := *diff.Diff
	start := strings.Index(first, "\n@@ ")
	second := strings.Index(first[start+1:], "\n@@ ")
	first = first[:start+1+second] + "\n"
	result := f.write("stage-hunk", map[string]any{"patch": first, "snapshot": diff.Snapshot, "source": reviewTestSource{Kind: "unstaged"}})
	index := f.git("show", ":tracked.txt")
	if result.State != "ok" || !strings.Contains(index, "FIRST_SELECTED") || strings.Contains(index, "SECOND_LEFT_UNSTAGED") {
		t.Fatal(result, index)
	}
	staged := f.status("staged")
	for _, resource := range []string{"stage", "revert"} {
		refused := f.write(resource, map[string]any{"paths": []string{"tracked.txt"}, "snapshot": staged.Snapshot, "source": reviewTestSource{Kind: "staged"}})
		if refused.State != "error" || f.git("show", ":tracked.txt") != index || !strings.Contains(reviewMustRead(t, filepath.Join(f.options.WorkDir, "tracked.txt")), "SECOND_LEFT_UNSTAGED") {
			t.Fatal(refused)
		}
	}
	wrongHunk := f.write("unstage-hunk", map[string]any{"patch": first, "snapshot": staged.Snapshot, "source": reviewTestSource{Kind: "unstaged"}})
	if wrongHunk.State != "error" || f.git("show", ":tracked.txt") != index {
		t.Fatal(wrongHunk)
	}
}
func TestGitReviewPublicNestedRenameBoundaryAndWorktree(t *testing.T) {
	f := newReviewFixture(t, true)
	f.put("outside.txt", "one\ntwo\nthree\nfour\n")
	f.put("scope/keep.txt", "scope\n")
	f.git("add", "-A")
	f.git("commit", "-q", "-m", "scope")
	f.git("mv", "outside.txt", "scope/new.txt")
	f.useWorkspace(filepath.Join(f.options.WorkDir, "scope"))
	status := f.status("staged")
	if len(status.Files) != 1 || status.Files[0].Path != "new.txt" {
		t.Fatal(status)
	}
	oldIndex := f.git("ls-files", "--stage")
	result := f.write("unstage", map[string]any{"paths": []string{"new.txt"}, "snapshot": status.Snapshot, "source": reviewTestSource{Kind: "staged"}})
	if result.State != "error" || f.git("ls-files", "--stage") != oldIndex {
		t.Fatal(result)
	}
	f.useWorkspace(f.options.WorkDir)
	status = f.status("staged")
	result = f.write("unstage", map[string]any{"paths": []string{"scope/new.txt"}, "snapshot": status.Snapshot, "source": reviewTestSource{Kind: "staged"}})
	if result.State != "ok" || f.git("diff", "--cached") != "" {
		t.Fatal(result)
	}
	worktree := filepath.Join(f.options.HomeDir, "linked-worktree")
	f.git("worktree", "add", "-q", "-b", "linked", worktree)
	f.useWorkspace(worktree)
	writeSearchFixture(t, worktree, "tracked.txt", "linked edit\n")
	status = f.status("unstaged")
	if status.State != "ok" || len(status.Files) != 1 || status.Files[0].Path != "tracked.txt" {
		t.Fatal(status)
	}
	if err := os.RemoveAll(worktree); err != nil {
		t.Fatal(err)
	}
	missing := f.status("unstaged")
	if missing.State != "missing_workdir" || missing.Error == "" {
		t.Fatal(missing)
	}
}
func TestGitReviewPublicConflictSymlinkAndEnvironmentBoundaries(t *testing.T) {
	f := newReviewFixture(t, true)
	f.git("checkout", "-q", "-b", "other")
	f.put("tracked.txt", "other side\n")
	f.git("commit", "-q", "-a", "-m", "other")
	f.git("checkout", "-q", "fixture")
	f.put("tracked.txt", "our side\n")
	f.git("commit", "-q", "-a", "-m", "ours")
	merge := f.server.repositoryGit(context.Background(), f.options.WorkDir, "merge", "other")
	if merge.code == 0 {
		t.Fatal("fixture did not conflict")
	}
	status := f.status("unstaged")
	if file := reviewTestFileMap(status)["tracked.txt"]; !file.Conflicted || file.Status != "conflicted" {
		t.Fatal(status)
	}
	result := f.write("stage-hunk", map[string]any{"patch": "diff --git a/tracked.txt b/tracked.txt\n--- a/tracked.txt\n+++ b/tracked.txt\n", "snapshot": status.Snapshot})
	if result.State != "error" || !strings.Contains(result.Error, "Unmerged path") {
		t.Fatal(result)
	}
	f.put("tracked.txt", "resolved\n")
	status = f.status("unstaged")
	whole := f.write("stage", map[string]any{"paths": []string{"tracked.txt"}, "snapshot": status.Snapshot})
	if whole.State != "ok" {
		t.Fatal(whole)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(".git", filepath.Join(f.options.WorkDir, "metadata-link")); err != nil {
			t.Fatal(err)
		}
		f.request("POST", "/revert", map[string]any{"paths": []string{"metadata-link/config"}, "snapshot": "x"}, 403)
		if err := os.Symlink("tracked.txt", filepath.Join(f.options.WorkDir, "file-link")); err != nil {
			t.Fatal(err)
		}
		f.request("GET", "/diff?source=unstaged&path=file-link", nil, 500)
	}
	// Poison only the Review request environment; fixture setup uses normal Git.
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_NAMESPACE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_PREFIX", "GIT_CEILING_DIRECTORIES"} {
		f.environment[key] = filepath.Join(f.options.HomeDir, "must-not-retarget")
	}
	f.environment["LC_ALL"] = "missing-locale"
	status = f.status("unstaged")
	if status.State != "ok" {
		t.Fatal(status)
	}
	for key := range f.environment {
		if strings.HasPrefix(key, "GIT_") && key != "GIT_CONFIG_NOSYSTEM" && key != "GIT_CONFIG_GLOBAL" && key != "GIT_AUTHOR_NAME" && key != "GIT_AUTHOR_EMAIL" && key != "GIT_COMMITTER_NAME" && key != "GIT_COMMITTER_EMAIL" && key != "GIT_AUTHOR_DATE" && key != "GIT_COMMITTER_DATE" {
			delete(f.environment, key)
		}
	}
	plain := filepath.Join(f.options.HomeDir, "plain")
	if err := os.Mkdir(plain, 0700); err != nil {
		t.Fatal(err)
	}
	f.useWorkspace(plain)
	if status = f.status("unstaged"); status.State != "not_git_repo" {
		t.Fatal(status)
	}
	f.environment["PATH"] = filepath.Join(f.options.HomeDir, "missing-git")
	if status = f.status("unstaged"); status.State != "error" || status.Error == "" {
		t.Fatal("unavailable Git was masked as a plain directory", status)
	}
}

func TestGitReviewPublicUntrackedReadErrorOmitsDiffFields(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires Unix mode permissions enforced for a non-root process")
	}
	f := newReviewFixture(t, true)
	f.put("unreadable.txt", "owned untracked private fixture\n")
	path := filepath.Join(f.options.WorkDir, "unreadable.txt")
	t.Cleanup(func() {
		if err := os.Chmod(path, 0644); err != nil && !os.IsNotExist(err) {
			t.Errorf("restore fixture permissions: %v", err)
		}
	})
	if err := os.Chmod(path, 0000); err != nil {
		t.Fatal(err)
	}
	// Establish the physical EACCES prerequisite without replacing production
	// filesystem/Git code. Some filesystems do not enforce POSIX permissions.
	if input, err := os.Open(path); err == nil {
		_ = input.Close()
		t.Skip("fixture filesystem does not enforce chmod000 read denial")
	} else if !os.IsPermission(err) {
		t.Fatalf("expected a permission-denied fixture, got %v", err)
	}
	raw := f.request("GET", "/diff?source=unstaged&path=unreadable.txt", nil, 200)
	var response map[string]json.RawMessage
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err, string(raw))
	}
	if stringField(response, "state") != "error" || stringField(response, "error") == "" {
		t.Fatal(string(raw))
	}
	for _, field := range []string{"diff", "binary", "truncated", "bytes"} {
		if _, present := response[field]; present {
			t.Fatalf("untracked read failure unexpectedly carries %s: %s", field, raw)
		}
	}
}
