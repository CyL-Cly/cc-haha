package memoryapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
)

type observed struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}
type operation struct {
	Method string  `json:"method"`
	Path   string  `json:"path"`
	Body   *string `json:"body,omitempty"`
}

func body(text string) *string { return &text }
func newFixture(t *testing.T) (*Service, string, string, string) {
	t.Helper()
	home := t.TempDir()
	home, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	config, cwd := filepath.Join(home, ".claude"), filepath.Join(home, "work")
	for _, p := range []string{config, cwd, filepath.Join(home, "tmp")} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	s, err := New(Options{Home: home, ConfigDir: config, CWD: cwd, Environment: func(key string) string {
		if key == "HOME" || key == "USERPROFILE" {
			return home
		}
		return ""
	}})
	if err != nil {
		t.Fatal(err)
	}
	return s, home, config, cwd
}
func writeFixture(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 10, 1, 2, 3, 4, 567000000, time.UTC)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}
func request(s *Service, op operation) observed {
	text := ""
	if op.Body != nil {
		text = *op.Body
	}
	r := httptest.NewRequest(op.Method, op.Path, strings.NewReader(text))
	w := httptest.NewRecorder()
	s.HandleHTTP(w, r, strings.Split(strings.Trim(r.URL.Path, "/"), "/"))
	return observed{w.Code, w.Body.String()}
}
func decode(t *testing.T, text string) any {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		t.Fatal(err, text)
	}
	return value
}
func fileURL(id, path string) string {
	return "/api/memory/file?" + url.Values{"projectId": {id}, "path": {path}}.Encode()
}

func TestMemoryDiscoveryMetadataAndLegacyFiles(t *testing.T) {
	s, _, config, cwd := newFixture(t)
	id := projectpath.Sanitize(cwd)
	root := filepath.Join(config, "projects", id, "memory")
	for name, text := range map[string]string{
		"MEMORY.md": "index", "user.md": "---\ntype: user\ndescription: \"\"\n---\nuser",
		"Éclair.md": "---\ntype: feedback\ndescription: café\n---\n", "eclair.md": "legacy",
		"子/记忆.md": "---\ntype: reference\ndescription: 42\n---\n", "unknown.md": "---\ntype: invalid\ndescription: description\n---\n",
		".hidden.md": "skip", ".hidden/x.md": "skip", "node_modules/module.md": "counted but not listed", "upper.MD": "skip",
	} {
		writeFixture(t, filepath.Join(root, filepath.FromSlash(name)), []byte(text))
	}
	if err := os.Symlink(filepath.Join(root, "user.md"), filepath.Join(root, "link.md")); err != nil {
		t.Fatal(err)
	}
	projects, err := s.Projects("")
	if err != nil || len(projects) != 1 || projects[0].FileCount != 7 || !projects[0].IsCurrent || projects[0].Label != cwd {
		t.Fatalf("projects=%+v err=%v", projects, err)
	}
	files, err := s.Files(id)
	if err != nil || len(files) != 6 || files[0].Path != "MEMORY.md" {
		t.Fatalf("files=%+v err=%v", files, err)
	}
	for _, f := range files {
		switch f.Path {
		case "user.md":
			if f.Type == nil || *f.Type != "user" || f.Description == nil || *f.Description != "" {
				t.Fatalf("raw string metadata lost %+v", f)
			}
		case "子/记忆.md":
			if f.Type == nil || *f.Type != "reference" || f.Description != nil {
				t.Fatalf("nonstring metadata %+v", f)
			}
		case "unknown.md":
			if f.Type != nil {
				t.Fatalf("invalid type retained %+v", f)
			}
		}
	}
	for _, op := range []operation{{Method: "GET", Path: "/api/memory/projects"}, {Method: "GET", Path: "/api/memory/files?projectId=" + id}, {Method: "GET", Path: fileURL(id, "子/记忆.md")}} {
		compareSource(t, s, config, cwd, []operation{op})
	}
}

func TestMemoryValidationMatchesSource(t *testing.T) {
	s, _, config, cwd := newFixture(t)
	// Invalid revision saves create parent directories before the conflict.
	// Both runtimes must begin with the same project state for the earlier GET.
	if err := os.MkdirAll(filepath.Join(config, "projects", "p", "memory"), 0700); err != nil {
		t.Fatal(err)
	}
	ops := []operation{{Method: "POST", Path: "/api/memory/projects"}, {Method: "PUT", Path: "/api/memory/files"}, {Method: "DELETE", Path: "/api/memory/file"}, {Method: "GET", Path: "/api/memory"}, {Method: "GET", Path: "/api/memory/unknown"}, {Method: "GET", Path: "/api/memory/files"}, {Method: "GET", Path: "/api/memory/files?projectId=.."}, {Method: "GET", Path: "/api/memory/file?projectId=p"}}
	for _, path := range []string{"/a.md", "C:\\a.md", "\\\\server\\a.md", "a/../x.md", "./a.md", "a//x.md", "a/.md", "a.MD", "a\x00.md", strings.Repeat("é", 128) + ".md", strings.Repeat("x/", 2050) + "a.md"} {
		// a/.md is deliberately valid and reaches missing project, all other
		// malformed branches retain their exact validation error precedence.
		ops = append(ops, operation{Method: "GET", Path: fileURL("p", path)})
	}
	for _, text := range []string{``, "{", `null`, `[]`, `3`, `true`, `{"extra":true}`, `{"path":"a.md"}`, `{"path":"a.md","content":null}`, `{"projectId":"p","path":"a.md","content":"","expectedBytes":1}`, `{"projectId":"p","path":"a.md","content":"","expectedUpdatedAt":null,"expectedBytes":0}`, `{"projectId":"p","path":"a.md","content":"","expectedUpdatedAt":"x","expectedBytes":-1}`, `{"projectId":"p","path":"a.md","content":"","expectedUpdatedAt":"x","expectedBytes":0.5}`, `{"projectId":"p","path":"a.md","content":"","expectedUpdatedAt":"x","expectedBytes":9007199254740992}`, `{"projectId":"p","path":"a.md","content":"","expectedUpdatedAt":"x","expectedBytes":0}`, `{"projectId":"p","path":"a.md","content":"","expectedUpdatedAt":"x","expectedBytes":"0"}`} {
		ops = append(ops, operation{Method: "PUT", Path: "/api/memory/file", Body: body(text)})
	}
	compareSource(t, s, config, cwd, ops)
	for _, op := range ops {
		if response := request(s, op); response.Status < 400 {
			t.Errorf("invalid request was accepted: %+v %+v", op, response)
		}
	}
}

func TestMemoryExactLimitsUTF8RevisionAndProjectBoundary(t *testing.T) {
	s, home, config, cwd := newFixture(t)
	id := projectpath.Sanitize(cwd)
	root := filepath.Join(config, "projects", id, "memory")
	writeFixture(t, filepath.Join(root, "exact.md"), bytes.Repeat([]byte("x"), MaxFileBytes))
	writeFixture(t, filepath.Join(root, "invalid.md"), []byte{'a', 0xff, 0xed, 0xa0, 0x80})
	stamp := time.Unix(-1, 999500000)
	writeFixture(t, filepath.Join(root, "before-epoch.md"), []byte("old"))
	if err := os.Chtimes(filepath.Join(root, "before-epoch.md"), stamp, stamp); err != nil {
		t.Fatal(err)
	}
	for _, op := range []operation{{Method: "GET", Path: fileURL(id, "exact.md")}, {Method: "GET", Path: fileURL(id, "invalid.md")}, {Method: "GET", Path: fileURL(id, "before-epoch.md")}} {
		response := request(s, op)
		if response.Status != 200 {
			t.Fatal(response)
		}
		compareSource(t, s, config, cwd, []operation{op})
	}
	out := filepath.Join(home, "external-project")
	writeFixture(t, filepath.Join(out, "memory", "outside.md"), []byte("outside"))
	if err := os.Symlink(out, filepath.Join(config, "projects", "project-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(config, "projects", "memory-link"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(out, "memory"), filepath.Join(config, "projects", "memory-link", "memory")); err != nil {
		t.Fatal(err)
	}
	ops := []operation{{Method: "GET", Path: "/api/memory/files?projectId=project-link"}, {Method: "GET", Path: "/api/memory/files?projectId=memory-link"}, {Method: "GET", Path: "/api/memory/files?projectId=" + url.QueryEscape(strings.Repeat("x", maxProjectIDBytes+1))}, {Method: "GET", Path: "/api/memory/files?projectId=" + url.QueryEscape("a\x01")}}
	compareSource(t, s, config, cwd, ops)
	for _, op := range ops {
		if response := request(s, op); response.Status != 400 {
			t.Fatal(response)
		}
	}
	for _, content := range []string{strings.Repeat("x", MaxFileBytes), `\ud800`} {
		var raw string
		if content == `\ud800` {
			raw = `{"projectId":"` + id + `","path":"surrogate.md","content":"\ud800"}`
		} else {
			bytes, _ := json.Marshal(map[string]string{"projectId": id, "path": "saved-exact.md", "content": content})
			raw = "\ufeff" + string(bytes)
		}
		response := request(s, operation{Method: "PUT", Path: "/api/memory/file", Body: body(raw)})
		if response.Status != 200 {
			t.Fatal(response)
		}
	}
	compareSource(t, s, config, cwd, []operation{{Method: "GET", Path: fileURL(id, "saved-exact.md")}, {Method: "GET", Path: fileURL(id, "surrogate.md")}})
	raw, _ := json.Marshal(map[string]string{"projectId": id, "path": "exceed.md", "content": strings.Repeat("x", MaxFileBytes+1)})
	response := request(s, operation{Method: "PUT", Path: "/api/memory/file", Body: body(string(raw))})
	if response.Status != 400 || !strings.Contains(response.Body, "Memory file content exceeds 512 KB") {
		t.Fatal(response)
	}
	compareSource(t, s, config, cwd, []operation{{Method: "PUT", Path: "/api/memory/file", Body: body(string(raw))}})
}

func TestMemoryConstructionRejectsAmbientState(t *testing.T) {
	for _, o := range []Options{{}, {Home: "/", ConfigDir: "/", CWD: "relative", Environment: func(string) string { return "" }}, {Home: "/", ConfigDir: "/", CWD: "/"}} {
		if _, err := New(o); err == nil {
			t.Fatal("ambient or relative Memory configuration accepted")
		}
	}
	var err error = badRequest("explicit boundary")
	if err.Error() != "explicit boundary" {
		t.Fatal(err)
	}
}

func TestMemoryAtomicWritesRevisionAndQueuedConcurrency(t *testing.T) {
	s, _, config, cwd := newFixture(t)
	id := projectpath.Sanitize(cwd)
	root := filepath.Join(config, "projects", id, "memory")
	writeFixture(t, filepath.Join(root, "nested", "file.md"), []byte("original"))
	initial := request(s, operation{Method: "GET", Path: fileURL(id, "nested/file.md")})
	var read struct{ File FileDetail }
	if err := json.Unmarshal([]byte(initial.Body), &read); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan observed, 2)
	for _, content := range []string{"first", "second"} {
		wg.Add(1)
		go func(content string) {
			defer wg.Done()
			raw, _ := json.Marshal(map[string]any{"projectId": id, "path": "nested\\file.md", "content": content, "expectedUpdatedAt": read.File.UpdatedAt, "expectedBytes": read.File.Bytes})
			results <- request(s, operation{Method: "PUT", Path: "/api/memory/file", Body: body(string(raw))})
		}(content)
	}
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for response := range results {
		switch response.Status {
		case 200:
			success++
		case 409:
			conflicts++
		default:
			t.Fatalf("unexpected write %v", response)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("concurrent revision writes succeeded=%d conflicts=%d", success, conflicts)
	}
	writeQueues.Lock()
	queues := len(writeQueues.paths)
	writeQueues.Unlock()
	if queues != 0 {
		t.Fatal("write queue leaked")
	}
	stat, err := os.Stat(filepath.Join(root, "nested", "file.md"))
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatalf("private atomic file: %v %v", stat, err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "nested"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("atomic temp leaked: %v %v", entries, err)
	}
	compareSource(t, s, config, cwd, []operation{{Method: "GET", Path: fileURL(id, "nested/file.md")}})
	// Native saved revision must equal real file stat, including exact mtime.
	text, _ := json.Marshal(map[string]string{"projectId": id, "path": "created/new.md", "content": "中文 😀"})
	saved := request(s, operation{Method: "PUT", Path: "/api/memory/file", Body: body(string(text))})
	stat, err = os.Stat(filepath.Join(root, "created", "new.md"))
	if err != nil {
		t.Fatal(err)
	}
	expected, _ := json.Marshal(map[string]any{"ok": true, "file": map[string]any{"path": "created/new.md", "updatedAt": iso(stat.ModTime()), "bytes": stat.Size()}})
	if saved.Status != 200 || !reflect.DeepEqual(decode(t, saved.Body), decode(t, string(expected))) {
		t.Fatalf("full saved response=%s expected=%s", saved.Body, expected)
	}
	compareSource(t, s, config, cwd, []operation{{Method: "GET", Path: fileURL(id, "created/new.md")}})
	writeFixture(t, filepath.Join(root, "directory.md", "child"), []byte("child"))
	text, _ = json.Marshal(map[string]string{"projectId": id, "path": "directory.md", "content": "fail"})
	failed := request(s, operation{Method: "PUT", Path: "/api/memory/file", Body: body(string(text))})
	if failed.Status != 500 {
		t.Fatal(failed)
	}
	entries, err = os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatal("failed atomic temp leaked")
		}
	}
}

func TestMemoryBoundariesLargeFilesUTF8AndLimits(t *testing.T) {
	s, home, config, cwd := newFixture(t)
	id := projectpath.Sanitize(cwd)
	root := filepath.Join(config, "projects", id, "memory")
	writeFixture(t, filepath.Join(root, "huge.md"), bytes.Repeat([]byte("x"), MaxFileBytes+1))
	writeFixture(t, filepath.Join(root, "invalid-utf8.md"), bytes.Repeat([]byte{255}, MaxFileBytes/2))
	writeFixture(t, filepath.Join(root, "outside-target.md"), []byte("inside"))
	outside := filepath.Join(home, "outside", "target.md")
	writeFixture(t, outside, []byte("protected"))
	if err := os.Symlink(outside, filepath.Join(root, "escape.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(root, "escape-dir")); err != nil {
		t.Fatal(err)
	}
	ops := []operation{{Method: "GET", Path: fileURL(id, "huge.md")}, {Method: "GET", Path: fileURL(id, "invalid-utf8.md")}, {Method: "GET", Path: fileURL(id, "escape.md")}, {Method: "GET", Path: fileURL(id, "missing.md")}}
	for _, path := range []string{"escape.md", "escape-dir/target.md"} {
		raw, _ := json.Marshal(map[string]string{"projectId": id, "path": path, "content": "mutate"})
		ops = append(ops, operation{Method: "PUT", Path: "/api/memory/file", Body: body(string(raw))})
	}
	compareSource(t, s, config, cwd, ops)
	if value, err := os.ReadFile(outside); err != nil || string(value) != "protected" {
		t.Fatalf("escape mutated: %s %v", value, err)
	}
	for i := 0; i < 505; i++ {
		writeFixture(t, filepath.Join(root, "many", fmt.Sprintf("%03d.md", i)), []byte("---\ntype: project\n---\n"))
	}
	files, err := s.Files(id)
	if err != nil || len(files) != MaxFiles {
		t.Fatalf("bounded traversal len=%d err=%v", len(files), err)
	}
	projects, err := s.Projects("")
	if err != nil || projects[0].FileCount != MaxFiles {
		t.Fatalf("bounded count=%+v err=%v", projects, err)
	}
	compareSource(t, s, config, cwd, []operation{{Method: "GET", Path: "/api/memory/files?projectId=" + id}, {Method: "GET", Path: "/api/memory/projects"}})
}

func TestMemoryProjectLabelsSessionsFilesystemAndCanonicalWorktrees(t *testing.T) {
	s, home, config, cwd := newFixture(t)
	main := filepath.Join(home, "main-repo")
	worktree := filepath.Join(cwd, "tree")
	metadata := filepath.Join(main, ".git", "worktrees", "tree")
	writeFixture(t, filepath.Join(worktree, ".git"), []byte("gitdir: "+metadata+"\n"))
	writeFixture(t, filepath.Join(metadata, "commondir"), []byte("../..\n"))
	writeFixture(t, filepath.Join(metadata, "gitdir"), []byte(filepath.Join(worktree, ".git")+"\n"))
	mainID := projectpath.Sanitize(main)
	if err := os.MkdirAll(filepath.Join(config, "projects", mainID, "memory"), 0700); err != nil {
		t.Fatal(err)
	}
	recoveredID := "-missing-canonical-root"
	writeFixture(t, filepath.Join(config, "projects", recoveredID, "memory", "one.md"), []byte("one"))
	writeFixture(t, filepath.Join(config, "projects", recoveredID, "session.jsonl"), []byte("not json {\"workDir\":\"/Recovered/Decomposed-e\\u0301\" trailing truncated"))
	fsRoot := filepath.Join(home, "actual-project")
	if err := os.MkdirAll(fsRoot, 0700); err != nil {
		t.Fatal(err)
	}
	fsID := projectpath.Sanitize(fsRoot)
	if err := os.MkdirAll(filepath.Join(config, "projects", fsID, "memory"), 0700); err != nil {
		t.Fatal(err)
	}
	projects, err := s.Projects(worktree)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 3 || projects[0].ID != mainID || projects[0].Label != main || !projects[0].IsCurrent {
		t.Fatalf("canonical projects %+v", projects)
	}
	for _, p := range projects {
		if p.ID == recoveredID && p.Label != "/Recovered/Decomposed-é" {
			t.Fatalf("session label %+v", p)
		}
		if p.ID == fsID && p.Label != fsRoot {
			t.Fatalf("filesystem label %+v", p)
		}
	}
	compareSource(t, s, config, cwd, []operation{{Method: "GET", Path: "/api/memory/projects?cwd=" + url.QueryEscape(worktree)}})
	// Ten latest sessions and 64 KiB heads are bounded, including invalid CWD
	// taking precedence over valid workDir in the same transcript.
	limited := "bounded-old-project"
	if err := os.MkdirAll(filepath.Join(config, "projects", limited, "memory"), 0700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(config, "projects", limited, "old.jsonl"), []byte(`{"cwd":"/Must/Not/Scan"}`))
	for i := 0; i < 10; i++ {
		path := filepath.Join(config, "projects", limited, fmt.Sprintf("new%02d.jsonl", i))
		writeFixture(t, path, append(bytes.Repeat([]byte(" "), 64*1024), []byte(`{"cwd":"/Beyond/Head"}`)...))
		stamp := time.Date(2026, 10, 2, 0, 0, i, 0, time.UTC)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	projects, err = s.Projects(worktree)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range projects {
		if p.ID == limited && p.Label != "bounded/old/project" {
			t.Fatalf("bounded session scan %+v", p)
		}
	}
	compareSource(t, s, config, cwd, []operation{{Method: "GET", Path: "/api/memory/projects?cwd=" + url.QueryEscape(worktree)}})
}

func TestMemorySourceCreateCanBeReadAndUpdatedNatively(t *testing.T) {
	s, _, config, cwd := newFixture(t)
	id := projectpath.Sanitize(cwd)
	raw, _ := json.Marshal(map[string]string{"projectId": id, "path": "source/legacy.md", "content": "---\ntype: user\n---\nActual Source write"})
	source := sourceHTTP(t, s.options.Home, config, cwd, []operation{{Method: "PUT", Path: "/api/memory/file", Body: body(string(raw))}})
	if source == nil {
		return
	}
	if source[0].Status != 200 {
		t.Fatal(source)
	}
	var saved struct {
		OK   bool
		File savedFile
	}
	if err := json.Unmarshal([]byte(source[0].Body), &saved); err != nil || !saved.OK {
		t.Fatalf("source saved %s %v", source[0].Body, err)
	}
	compareSource(t, s, config, cwd, []operation{{Method: "GET", Path: fileURL(id, "source/legacy.md")}, {Method: "GET", Path: "/api/memory/files?projectId=" + id}})
	raw, _ = json.Marshal(map[string]any{"projectId": id, "path": "source/legacy.md", "content": "native continuation", "expectedUpdatedAt": saved.File.UpdatedAt, "expectedBytes": saved.File.Bytes})
	updated := request(s, operation{Method: "PUT", Path: "/api/memory/file", Body: body(string(raw))})
	if updated.Status != 200 {
		t.Fatal(updated)
	}
	compareSource(t, s, config, cwd, []operation{{Method: "GET", Path: fileURL(id, "source/legacy.md")}})
}
