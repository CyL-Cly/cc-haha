package server

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func writeSearchFixture(t *testing.T, root, name, body string) {
	t.Helper()
	target := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestFilesystemSearchNativeFallbackIgnoreContextsRankingAndLimits(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	respect := true
	options.RespectGitignore = func(context.Context) (bool, error) { return respect, nil }
	_, host := testHost(t, options)
	root := options.WorkDir
	for _, sample := range []struct{ name, body string }{
		{".gitignore", "*.log\nblocked/\nsecret.txt\n"}, {".ignore", "*.tmp\n*.drop\n"}, {".rgignore", "rg-hidden.txt\n"},
		{"src/.ignore", "!kept.drop\n"}, {"src/kept.drop", "actual"}, {"src/hidden.drop", "hidden"}, {"query.txt", "exact"},
		{"nested/query.txt", "nested"}, {"query-dir/file.bin", "ancestor"}, {"query.log", "ignored"},
		{"blocked/query.txt", "ignored parent"}, {".git/query.txt", "metadata"}, {"query.tmp", "ignore even optout"},
		{"secret.txt", "gitignored"}, {"rg-hidden.txt", "rgignored"},
	} {
		writeSearchFixture(t, root, sample.name, sample.body)
	}
	if err := os.MkdirAll(filepath.Join(root, "empty-dir"), 0700); err != nil {
		t.Fatal(err)
	}
	external := t.TempDir()
	writeSearchFixture(t, external, "query-external.txt", "not followed")
	_ = os.Symlink(external, filepath.Join(root, "query-symlink"))
	search := func(query, extra string) []filesystemEntry {
		raw := requestJSON(t, host, "GET", "/api/filesystem/browse?path="+url.QueryEscape(root)+"&includeFiles=true&search="+url.QueryEscape(query)+extra, "", 200)
		var result struct {
			Entries []filesystemEntry
			Query   string
		}
		if err := json.Unmarshal(raw, &result); err != nil || result.Query != query {
			t.Fatal(string(raw), err)
		}
		return result.Entries
	}
	entries := search("@./query/", "")
	paths := []string{}
	for _, entry := range entries {
		paths = append(paths, entry.RelativePath)
		if entry.Path != filepath.Join(canonicalAccessPath(root), filepath.FromSlash(entry.RelativePath)) {
			t.Fatal(entry)
		}
	}
	if !reflect.DeepEqual(paths, []string{"query.txt", "query-dir", "query-dir/file.bin", "nested/query.txt"}) {
		t.Fatal("ranking/ignore/symlink", paths)
	}
	if len(search("query", "&maxResults=1tail")) != 1 || len(search("query", "&maxResults=nan")) != 0 || len(search("query", "&maxResults=-1")) != len(entries)-1 {
		t.Fatal("Source parseInt/slice semantics changed")
	}
	entries = search("kept", "")
	if len(entries) != 1 || entries[0].RelativePath != "src/kept.drop" {
		t.Fatal("nested negation", entries)
	}
	if len(search("secret", "")) != 0 {
		t.Fatal("gitignore not applied")
	}
	respect = false
	entries = search("secret", "")
	if len(entries) != 1 || entries[0].RelativePath != "secret.txt" {
		t.Fatal("respectGitignore optout failed", entries)
	}
	if len(search("rg-hidden", "")) != 0 {
		t.Fatal("rgignore was disabled with gitignore")
	}
	if len(search("empty-dir", "")) != 0 {
		t.Fatal("empty directories invented as search results")
	}
	if len(search("@./ /", "")) != 0 {
		t.Fatal("normalization-only query was not empty")
	}
}

func TestFilesystemFallbackBudgetsAndCancellationAreActual(t *testing.T) {
	root := t.TempDir()
	writeSearchFixture(t, root, "a/file.txt", "a")
	writeSearchFixture(t, root, "b/file.txt", "b")
	writeSearchFixture(t, root, "miss.txt", "miss")
	files, err := filesUsingFilesystem(context.Background(), root, true, filesystemScanOptions{timeout: time.Second, maxDirectories: 2, maxFiles: 20, query: "file"})
	if err != nil || !reflect.DeepEqual(files, []string{"a/file.txt"}) {
		t.Fatal("directory budget", files, err)
	}
	files, err = filesUsingFilesystem(context.Background(), root, true, filesystemScanOptions{timeout: time.Second, maxDirectories: 20, maxFiles: 1, query: "file"})
	if err != nil || !reflect.DeepEqual(files, []string{"a/file.txt"}) {
		t.Fatal("matching file budget", files, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = filesUsingFilesystem(ctx, root, true, filesystemScanOptions{timeout: time.Second, maxDirectories: 20, maxFiles: 20, query: "file"})
	if err != context.Canceled {
		t.Fatal("canceled scan continued", err)
	}
	files, err = filesUsingFilesystem(context.Background(), root, true, filesystemScanOptions{timeout: -time.Second, maxDirectories: 20, maxFiles: 20, query: "file"})
	if err != nil || len(files) != 0 {
		t.Fatal("expired deadline", files, err)
	}
}

func TestFilesystemInteractiveSearchSupersedesSameRoot(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	started := make(chan struct{}, 2)
	firstCanceled := make(chan struct{})
	release := make(chan struct{})
	var calls int
	options.RespectGitignore = func(ctx context.Context) (bool, error) {
		calls++
		index := calls
		started <- struct{}{}
		if index == 1 {
			<-ctx.Done()
			close(firstCanceled)
			return true, ctx.Err()
		}
		select {
		case <-release:
			return true, nil
		case <-ctx.Done():
			return true, ctx.Err()
		}
	}
	// Both callbacks execute in admission order because the first announces entry
	// before the second starts; the only shared writes remain under this test lock.
	server, host := testHost(t, options)
	target := host.URL + "/api/filesystem/browse?path=" + url.QueryEscape(options.WorkDir) + "&search=query"
	first, second := make(chan int, 1), make(chan int, 1)
	get := func(result chan<- int) {
		response, err := host.Client().Get(target)
		if err != nil {
			result <- 0
			return
		}
		response.Body.Close()
		result <- response.StatusCode
	}
	go get(first)
	<-started
	go get(second)
	<-started
	select {
	case <-firstCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("superseded scan did not cancel")
	}
	close(release)
	if <-first != 500 || <-second != 200 {
		t.Fatal("superseded/current response mismatch")
	}
	server.mu.Lock()
	remaining := len(server.filesystemSearches)
	server.mu.Unlock()
	if remaining != 0 {
		t.Fatal("search owner leaked", remaining)
	}
}

func TestFilesystemSearchUsesNativeGitAndRootScopedIgnore(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	options.Environment = func() map[string]string {
		return map[string]string{"PATH": os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": filepath.Join(options.HomeDir, "no-global-git-config")}
	}
	server, _ := testHost(t, options)
	for _, name := range []string{".git/objects", ".git/refs/heads"} {
		if err := os.MkdirAll(filepath.Join(options.WorkDir, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, sample := range []struct{ name, body string }{
		{".git/HEAD", "ref: refs/heads/fixture\n"}, {".git/config", "[core]\n repositoryformatversion = 0\n bare = false\n"},
		{".gitignore", "ignored.txt\n"}, {".ignore", "hidden.txt\n"}, {"sub/.rgignore", "rg-hidden.txt\n"},
		{"sub/visible.txt", "actual"}, {"sub/ignored.txt", "ignored"}, {"sub/hidden.txt", "hidden"}, {"sub/rg-hidden.txt", "hidden"}, {"outside.txt", "outside"},
	} {
		writeSearchFixture(t, options.WorkDir, sample.name, sample.body)
	}
	root := filepath.Join(options.WorkDir, "sub")
	files, err := server.filesUsingGit(context.Background(), root, true)
	if err != nil || !reflect.DeepEqual(files, []string{".rgignore", "visible.txt"}) {
		t.Fatal("native Git + subtree/root ignore", files, err)
	}
	files, err = server.filesUsingGit(context.Background(), root, false)
	if err != nil || !reflect.DeepEqual(files, []string{".rgignore", "ignored.txt", "visible.txt"}) {
		t.Fatal("native Git respectGitignore false", files, err)
	}
	if _, err := os.Stat(filepath.Join(options.WorkDir, ".git/index")); !os.IsNotExist(err) {
		t.Fatal("read-only Git search created index", err)
	}
}
