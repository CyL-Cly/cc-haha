package marketapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestCatalogHTTPIsOfflineFiltersBeforePaginationAndPreservesProvenance(t *testing.T) {
	calls := new(atomic.Int32)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		t.Error("offline catalog made upstream request")
	}))
	defer peer.Close()
	s, err := New(Options{ConfigDir: t.TempDir(), Environment: func() map[string]string {
		return map[string]string{"HAHA_MARKET_BASE_CLAWHUB": peer.URL, "HAHA_MARKET_BASE_SKILLHUB": peer.URL}
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	request := func(query string) object {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "http://local/api/market/skills"+query, nil)
		s.HandleHTTP(w, r, []string{"api", "market", "skills"})
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		var out object
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	first := request("?limit=1")
	if first["scope"] != "catalog" || num(first["total"]) < 1 || len(arr(first["categories"])) == 0 || first["catalogGeneratedAt"] != catalog["generatedAt"] {
		t.Fatal(first)
	}
	items := arr(first["items"])
	if len(items) != 1 || obj(items[0])["curated"] != true {
		t.Fatal(first)
	}
	entry := obj(items[0])
	found := request("?q=" + str(entry["slug"]) + "&category=" + str(entry["category"]) + "&limit=1")
	if len(arr(found["items"])) != 1 || obj(arr(found["items"])[0])["slug"] != entry["slug"] {
		t.Fatal(found)
	}
	empty := request("?category=missing-category")
	if num(empty["total"]) != 0 || len(arr(empty["items"])) != 0 || empty["nextCursor"] != nil {
		t.Fatal(empty)
	}
	if cursor := str(first["nextCursor"]); cursor != "" {
		next := request("?limit=1&cursor=" + cursor)
		if len(arr(next["items"])) != 1 || obj(arr(next["items"])[0])["slug"] == entry["slug"] {
			t.Fatal(next)
		}
	}
	if calls.Load() != 0 {
		t.Fatal(calls.Load())
	}
}

func TestOwnerHTTPPinsEveryReadAndConcurrentCacheAndInstall(t *testing.T) {
	var mu sync.Mutex
	requests := []string{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		owner := r.URL.Query().Get("owner")
		mu.Lock()
		requests = append(requests, r.URL.Path+"@"+owner)
		mu.Unlock()
		if owner == "ambiguous" {
			w.WriteHeader(409)
			fmt.Fprint(w, `{"code":"AMBIGUOUS_SKILL_SLUG","matches":[{"ownerHandle":"wrong"}]}`)
			return
		}
		if owner != "alice" && owner != "bob" {
			http.Error(w, "missing owner", 400)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/file"):
			fmt.Fprintf(w, "---\nname: shared\ndescription: %s\n---\n%s", owner, owner)
		case strings.Contains(r.URL.Path, "/versions/"):
			json.NewEncoder(w).Encode(object{"version": object{"files": []object{{"path": "SKILL.md", "size": 42}}, "security": object{"status": "clean", "scanners": object{"llm": object{"status": "safe", "summary": "reviewed"}}}}})
		default:
			json.NewEncoder(w).Encode(object{"skill": object{"slug": "shared", "displayName": owner, "description": "body"}, "latestVersion": object{"version": "1", "changelog": "meaningful release", "createdAt": 123}, "owner": object{"handle": owner}})
		}
	}))
	defer peer.Close()
	dir := t.TempDir()
	s, _ := New(Options{ConfigDir: dir, Environment: func() map[string]string { return map[string]string{"HAHA_MARKET_BASE_CLAWHUB": peer.URL} }})
	defer s.Close(context.Background())
	var wg sync.WaitGroup
	for _, owner := range []string{"alice", "bob", "alice", "bob"} {
		wg.Add(1)
		go func(owner string) {
			defer wg.Done()
			detail, _, err := s.Detail(context.Background(), "clawhub", "shared", owner)
			if err != nil || obj(detail["author"])["handle"] != owner || obj(detail["changelog"])["text"] != "meaningful release" || len(detail["securityReports"].([]object)) != 2 {
				t.Errorf("detail %s: %v %v", owner, detail, err)
				return
			}
			file, err := s.File(context.Background(), "clawhub", "shared", "SKILL.md", owner)
			if err != nil || !strings.HasSuffix(str(file["content"]), owner) {
				t.Errorf("file %s: %v %v", owner, file, err)
			}
		}(owner)
	}
	wg.Wait()
	if _, _, err := s.Detail(context.Background(), "clawhub", "shared", "ambiguous"); err == nil {
		t.Fatal("pinned owner widened after 409")
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "http://local/api/market/install", strings.NewReader(`{"id":"clawhub:shared","owner":"bob"}`))
	s.HandleHTTP(w, r, []string{"api", "market", "install"})
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(dir, "skills", "shared", "SKILL.md"))
	if err != nil || !strings.HasSuffix(string(data), "bob") {
		t.Fatalf("installed wrong owner's bytes: %s %v", data, err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, path := range requests {
		if strings.HasSuffix(path, "@wrong") || strings.HasSuffix(path, "@") {
			t.Fatal(path)
		}
	}
}

func TestMarketHTTPRejectsInvalidOwnerAndScope(t *testing.T) {
	s, _ := New(Options{ConfigDir: t.TempDir()})
	defer s.Close(context.Background())
	for _, path := range []string{"/api/market/skills?scope=bogus", "/api/market/skills/clawhub/shared?owner=../escape", "/api/market/skills/skillhub/shared?owner=bad%20owner"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "http://local"+path, nil)
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		s.HandleHTTP(w, r, parts)
		if w.Code != 400 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
}
