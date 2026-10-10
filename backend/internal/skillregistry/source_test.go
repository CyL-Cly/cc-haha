package skillregistry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type sourcePlugin struct {
	Name        string   `json:"name"`
	SkillsPath  string   `json:"skillsPath"`
	SkillsPaths []string `json:"skillsPaths"`
}
type sourceScenario struct {
	CWD         string              `json:"cwd"`
	ProjectRoot string              `json:"projectRoot"`
	Settings    protocol.JSONObject `json:"settings"`
	DisableEnv  string              `json:"disableEnv"`
	Plugins     []sourcePlugin      `json:"plugins"`
	Requests    []string            `json:"requests"`
}

func TestActualOriginalSkillsHandlerRootsAndFiles(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun not configured")
	}
	_, file, _, _ := runtime.Caller(0)
	fixture := filepath.Join(filepath.Dir(file), "../../../scripts/quality-gate/go-parity/native-skill-discovery.fixture.ts")
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	cwd := filepath.Join(repo, "pkg")
	if err := os.MkdirAll(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	files := map[string]string{
		".claude/skills/user/SKILL.md":               "---\nname: User name\ndescription: Run the checks\n  before finishing\nversion: .inf\n---\nInstructions 😀\n",
		".agents/skills/user/SKILL.md":               "Shadowed user skill",
		".agents/skills/shared-only/SKILL.md":        "---\ndescription: false\nuser-invocable: 'false'\n---\nFallback description",
		".agents/skills/fallback/SKILL.md":           "Fallback file",
		".claude/skills/user/script/a.ts":            "export const fixture = true\n",
		".claude/skills/user/script/B.go":            "package fixture\n",
		".claude/skills/user/script/ä.md":            "secondary markdown",
		".claude/skills/user/.hidden":                "Hidden",
		".claude/skills/user/.market-meta.json":      `{"id":"clawhub:user","source":"clawhub","slug":"user","installedAt":"timestamp","fileCount":2,"version":"1","signatureVerified":false,"future":"preserve-other-state"}`,
		"repo/pkg/.agents/skills/near/SKILL.md":      "Near Agent convention",
		"repo/.claude/skills/near/SKILL.md":          "Parent native convention",
		"repo/.claude/skills/parent/SKILL.md":        "Parent skill",
		"repo/nested/.git/HEAD":                      "ref: refs/heads/nested\n",
		"repo/nested/.claude/skills/nested/SKILL.md": "Nested repo skill",
		"other/.git/HEAD":                            "ref: refs/heads/other\n",
		"other/.claude/skills/other/SKILL.md":        "Sibling repo skill",
		"plugin/skills/.hidden/SKILL.md":             "Hidden plugin skill",
		"plugin/skills/visible/SKILL.md":             "---\nname: false\ndescription: [one, two]\nversion: [1, null, 2]\n---\nPlugin body",
		"plugin/direct/SKILL.md":                     "---\nuser-invocable: false\n---\nDirect plugin skill",
		"plugin/direct/nested/SKILL.md":              "Must not inspect children of a direct Skill",
	}
	for name, body := range files {
		put(t, filepath.Join(home, filepath.FromSlash(name)), body)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude", "skills", "fallback"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "nested", "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "other", "pkg"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, ".claude", "skills", "user"), filepath.Join(home, ".claude", "skills", "symlink")); err != nil {
		t.Fatal(err)
	}
	commonRequests := []string{"/api/skills", "/api/skills/detail?source=user&name=user", "/api/skills/detail?source=user&name=symlink", "/api/skills/detail?source=user&name=fallback", "/api/skills/detail?source=project&name=near", "/api/skills/detail?source=plugin&name=sample:.hidden", "/api/skills/detail?source=plugin&name=sample:direct", "/api/skills/detail?source=unsupported&name=item", "/api/skills/detail?source=user&name=x..y", "/api/skills/detail?source=user", "/api/skills/detail?source=user&name=absent"}
	plugins := []sourcePlugin{{Name: "sample", SkillsPath: filepath.Join(home, "plugin", "skills"), SkillsPaths: []string{filepath.Join(home, "plugin", "direct"), filepath.Join(home, "plugin", "skills")}}}
	scenarios := []sourceScenario{
		{cwd, repo, protocol.NewJSONObject(), "", plugins, commonRequests},
		{cwd, repo, protocol.NewJSONObject(), " YeS ", plugins, commonRequests},
		{cwd, repo, protocol.NewJSONObject(protocol.JSONField{Name: "disableAgentSkillsDirectory", Value: json.RawMessage("true")}), "", plugins, commonRequests},
		{filepath.Join(repo, "nested", "sub"), repo, protocol.NewJSONObject(), "", plugins, []string{"/api/skills", "/api/skills/detail?source=project&name=parent"}},
		{filepath.Join(home, "other", "pkg"), repo, protocol.NewJSONObject(), "", plugins, []string{"/api/skills", "/api/skills/detail?source=project&name=parent"}},
		{home, repo, protocol.NewJSONObject(), "", plugins, []string{"/api/skills"}},
	}
	input, _ := json.Marshal(map[string]any{"scenarios": scenarios})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_SKILLS_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	source, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source failed: %v %s", err, stderr.Bytes())
	}
	actual := []any{}
	for _, scenario := range scenarios {
		ps := []Plugin{}
		for _, p := range scenario.Plugins {
			ps = append(ps, Plugin{p.Name, p.SkillsPath, p.SkillsPaths})
		}
		service := testService(t, home, scenario.CWD, ps, scenario.Settings, scenario.DisableEnv)
		service.options.ProjectRoot = scenario.ProjectRoot
		requests := []any{}
		for _, path := range scenario.Requests {
			target, _ := url.Parse(path)
			var body any
			var err error
			status := 200
			if target.Path == "/api/skills" {
				var list []Meta
				list, err = service.List(ctx, scenario.CWD)
				body = map[string]any{"skills": list}
			} else {
				var detail *Detail
				detail, err = service.Detail(ctx, scenario.CWD, target.Query().Get("source"), target.Query().Get("name"))
				body = map[string]any{"detail": detail}
			}
			if err != nil {
				var api *Error
				if !errors.As(err, &api) {
					t.Fatal(err)
				}
				status = api.Status
				code := "BAD_REQUEST"
				if status == 404 {
					code = "NOT_FOUND"
				}
				body = map[string]any{"error": code, "message": api.Message}
			}
			requests = append(requests, map[string]any{"status": status, "body": body})
		}
		roots, err := service.ProjectRoots(ctx, scenario.CWD)
		if err != nil {
			t.Fatal(err)
		}
		actual = append(actual, map[string]any{"userRoots": service.UserRoots(ctx), "projectRoots": roots, "addDirRoots": service.AddDirRoots(ctx, scenario.CWD), "nestedCandidates": service.NestedCandidates(ctx, scenario.CWD), "userNames": service.UserNames(ctx), "requests": requests})
	}
	raw, _ := json.Marshal(actual)
	var a, b []any
	if err = json.Unmarshal(source, &b); err != nil {
		t.Fatalf("Source JSON: %v %s", err, source)
	}
	_ = json.Unmarshal(raw, &a)
	if len(a) != len(b) {
		t.Fatalf("case count: %d %d", len(a), len(b))
	}
	for i := range a {
		t.Run(scenarios[i].CWD+scenarios[i].DisableEnv, func(t *testing.T) {
			if !reflect.DeepEqual(a[i], b[i]) {
				x, _ := json.Marshal(a[i])
				y, _ := json.Marshal(b[i])
				t.Fatalf("Go=%s\nSource=%s", x, y)
			}
		})
	}
}
