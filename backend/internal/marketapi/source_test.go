package marketapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type step struct {
	Now      int64   `json:"now,omitempty"`
	Disabled *string `json:"disabled,omitempty"`
	Control  any     `json:"control,omitempty"`
	Path     string  `json:"path"`
	Method   string  `json:"method,omitempty"`
	Body     any     `json:"body,omitempty"`
}
type response struct {
	Status int `json:"status"`
	Body   any `json:"body"`
}

func marketPeer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	calls := new(atomic.Int32)
	contents := map[string]string{"SKILL.md": "---\nname: go-market-test\ndescription: Actual Go Market installed skill\n---\nRead the file requested by the user and return its exact marker.\n", "scripts/readme.txt": "market-file-marker\n"}
	files := []object{}
	for _, path := range []string{"SKILL.md", "scripts/readme.txt"} {
		sum := sha256.Sum256([]byte(contents[path]))
		files = append(files, object{"path": path, "size": len(contents[path]), "sha256": hex.EncodeToString(sum[:])})
	}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		path := r.URL.Path
		fixture := ""
		switch {
		case path == "/api/v1/skills":
			fixture = "clawhub-list.json"
		case path == "/api/skills":
			fixture = "skillhub-list.json"
		case path == "/api/v1/search":
			fixture = "clawhub-search.json"
		case strings.Contains(path, "/go-market-test/versions/"):
			json.NewEncoder(w).Encode(object{"version": object{"version": "1.0", "license": "MIT", "files": files, "security": object{"status": "clean", "hasWarnings": true}}})
			return
		case path == "/api/v1/skills/go-market-test":
			json.NewEncoder(w).Encode(object{"skill": object{"slug": "go-market-test", "displayName": "Go Market Test", "description": "---\nname: go-market-test\n---\nA real installed skill", "stats": object{"downloads": 17}}, "latestVersion": object{"version": "1.0"}, "owner": object{"handle": "local-fixture"}})
			return
		case path == "/api/v1/skills/report-order":
			fmt.Fprint(w, `{"skill":{"slug":"report-order","ownerName":"fallback-owner","downloads":17,"verified":true},"owner":{"displayName":"Display","image":""},"securityReports":{"zeta":{"status":"safe"},"alpha":{"status":"benign"},"2":{"status":"clean"},"1":{"status":"benign"}}}`)
			return
		case strings.HasSuffix(path, "/file"):
			if v, ok := contents[r.URL.Query().Get("path")]; ok {
				fmt.Fprint(w, v)
			} else {
				fmt.Fprint(w, "# Physical HTTP market file")
			}
			return
		case strings.HasSuffix(path, "/files"):
			fixture = "skillhub-files.json"
		case strings.Contains(path, "/versions/"):
			fixture = "clawhub-version-detail.json"
		case strings.HasSuffix(path, "/pe-compliance-expert-pro"):
			fixture = "skillhub-detail.json"
		case strings.HasSuffix(path, "/git"):
			fixture = "clawhub-detail.json"
		default:
			http.Error(w, "Not Found", 404)
			return
		}
		data, e := os.ReadFile(filepath.Join(filepath.Join(originalSourceRoot(t), "src/server/__tests__/fixtures/market"), fixture))
		if e != nil {
			t.Error(e)
			http.Error(w, "fixture missing", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))
	t.Cleanup(peer.Close)
	return peer, calls
}
func disposableEnv(home, base string) []string {
	out := []string{}
	for _, key := range []string{"PATH", "GOROOT", "GOCACHE", "GOMODCACHE", "GOPATH", "DYLD_LIBRARY_PATH"} {
		if value := os.Getenv(key); value != "" {
			out = append(out, key+"="+value)
		}
	}
	return append(out, "HOME="+home, "USERPROFILE="+home, "CLAUDE_CONFIG_DIR="+filepath.Join(home, ".claude"), "XDG_CONFIG_HOME="+filepath.Join(home, "config"), "XDG_DATA_HOME="+filepath.Join(home, "data"), "XDG_STATE_HOME="+filepath.Join(home, "state"), "XDG_CACHE_HOME="+filepath.Join(home, "cache"), "TMPDIR="+home, "NODE_ENV=production", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file", "HAHA_MARKET_BASE_CLAWHUB="+base, "HAHA_MARKET_BASE_SKILLHUB="+base)
}
func sourceContract(t *testing.T, home, base string, steps []step) []response {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual original Source requires explicit pinned Bun")
	}
	payload, _ := json.Marshal(object{"now": int64(1800000000000), "base": base, "steps": steps})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", "testdata/source.fixture.ts")
	cmd.Env = append(disposableEnv(home, base), "CC_HAHA_MARKET_SOURCE_ROOT="+originalSourceRoot(t))
	cmd.Stdin = bytes.NewReader(payload)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source consumer: %v\n%s", err, output)
	}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "MARKET_SOURCE_RESULT:") {
			var result []response
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "MARKET_SOURCE_RESULT:")), &result); err != nil {
				t.Fatal(err)
			}
			return result
		}
	}
	t.Fatalf("missing actual Source receipt: %s", output)
	return nil
}
func normalizeResult(value any, home string) any {
	switch v := value.(type) {
	case map[string]any:
		out := object{}
		for key, x := range v {
			if key == "installedAt" {
				if _, ok := x.(string); ok {
					out[key] = "<ISO_TIME>"
				} else {
					out[key] = x
				}
			} else {
				out[key] = normalizeResult(x, home)
			}
		}
		return out
	case []any:
		out := []any{}
		for _, x := range v {
			out = append(out, normalizeResult(x, home))
		}
		return out
	case string:
		return strings.ReplaceAll(v, home, "<HOME>")
	}
	return value
}
func TestActualOriginalMarketAPIAndPhysicalGoInstallConsumer(t *testing.T) {
	peer, calls := marketPeer(t)
	steps := []step{
		{Path: "/api/market/status"}, {Path: "/api/market/skills?limit=3"}, {Path: "/api/market/skills?limit=3"}, {Path: "/api/market/skills?source=skillhub&q=git&limit=2"}, {Path: "/api/market/skills?cursor=invalid"}, {Path: "/api/market/skills/clawhub/git"}, {Path: "/api/market/skills/skillhub/pe-compliance-expert-pro"},
		{Path: "/api/market/skills/skillhub/report-order"}, {Path: "/api/market/skills/clawhub/git/file?path=SKILL.md"},
		{Path: "/api/market/install", Method: "POST", Body: object{"id": "clawhub:go-market-test"}}, {Path: "/api/market/skills/clawhub/go-market-test"}, {Path: "/api/market/install", Method: "POST", Body: object{"id": "clawhub:go-market-test"}}, {Path: "/api/market/uninstall", Method: "POST", Body: object{"id": "clawhub:go-market-test"}}, {Path: "/api/market/uninstall", Method: "POST", Body: object{"id": "clawhub:go-market-test"}},
		{Path: "/api/market/skills?source=evil"}, {Path: "/api/market/skills?security=hacked"}, {Path: "/api/market/skills?installed=nope"}, {Path: "/api/market/skills/npm/git"}, {Path: "/api/market/skills/clawhub/..%2Fetc"}, {Path: "/api/market/skills/clawhub/git/file?path=../../etc"}, {Path: "/api/market/skills/clawhub/git/file"}, {Path: "/api/market/nonsense"}, {Path: "/api/market/skills", Method: "DELETE"}, {Path: "/api/market/install", Method: "POST", Body: "not-json"}, {Path: "/api/market/install", Method: "POST", Body: []any{}}, {Path: "/api/market/install", Method: "POST", Body: object{"id": "bad"}},
		{Path: "/api/market/install", Method: "POST", Body: "{} {}"},
		{Path: "/api/market/skills/clawhub/git/file?path=" + url.QueryEscape(strings.Repeat("😀", 300))},
	}
	sourceHome := t.TempDir()
	expected := sourceContract(t, sourceHome, peer.URL, steps)
	if calls.Load() < 10 {
		t.Fatal("Source did not use physical upstream")
	}
	calls.Store(0)
	home := t.TempDir()
	changes := 0
	s, err := New(Options{ConfigDir: filepath.Join(home, ".claude"), Environment: func() map[string]string {
		return map[string]string{"HAHA_MARKET_BASE_CLAWHUB": peer.URL, "HAHA_MARKET_BASE_SKILLHUB": peer.URL}
	}, Now: func() time.Time { return time.UnixMilli(1800000000000) }, Changed: func() { changes++ }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	if len(expected) != len(steps) {
		t.Fatal("Source result count")
	}
	for i, step := range steps {
		var body []byte
		if v, ok := step.Body.(string); ok {
			body = []byte(v)
		} else if step.Body != nil {
			body, _ = json.Marshal(step.Body)
		}
		method := step.Method
		if method == "" {
			method = "GET"
		}
		req := httptest.NewRequest(method, "http://fixture.invalid"+step.Path, bytes.NewReader(body))
		rec := httptest.NewRecorder()
		s.HandleHTTP(rec, req, strings.Split(strings.Trim(req.URL.EscapedPath(), "/"), "/"))
		var actual any
		if err = json.Unmarshal(rec.Body.Bytes(), &actual); err != nil {
			t.Fatal(err)
		}
		a, b := normalizeResult(actual, home), normalizeResult(expected[i].Body, sourceHome)
		if rec.Code != expected[i].Status || !reflect.DeepEqual(a, b) {
			av, _ := json.MarshalIndent(a, "", "  ")
			bv, _ := json.MarshalIndent(b, "", "  ")
			t.Errorf("case %d %s status Go=%d Source=%d\nGo=%s\nSource=%s", i, step.Path, rec.Code, expected[i].Status, av, bv)
		}
		if i == 9 {
			path := filepath.Join(home, ".claude/skills/go-market-test/SKILL.md")
			data, e := os.ReadFile(path)
			if e != nil || !strings.Contains(string(data), "Actual Go Market installed skill") {
				t.Fatalf("physical install: %v", e)
			}
			meta := s.meta("go-market-test")
			if meta == nil || meta["fileCount"] != float64(2) {
				t.Fatalf("original metadata shape: %v", meta)
			}
		}
	}
	if changes != 2 {
		t.Fatalf("installed/removed registry invalidation=%d", changes)
	}
	if _, e := os.Stat(filepath.Join(home, ".claude/skills/go-market-test")); !os.IsNotExist(e) {
		t.Fatal("managed skill not removed")
	}
	if calls.Load() < 10 {
		t.Fatal("Go did not use physical upstream")
	}
}

func originalSourceRoot(t *testing.T) string {
	t.Helper()
	if root := os.Getenv("CC_HAHA_MARKET_SOURCE_ROOT"); root != "" {
		return root
	}
	root, e := filepath.Abs("../../..")
	if e != nil {
		t.Fatal(e)
	}
	return root
}
