package usercontext

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPinnedNativeExcludesBraceExtglobNegationWindowsAndCapabilities(t *testing.T) {
	cases := []struct {
		path     string
		patterns []string
		platform string
		want     bool
	}{
		{"/repo/.claude/CLAUDE.md", []string{"**/{CLAUDE,AGENTS}.md"}, "darwin", true},
		{"/repo/AGENTS.md", []string{"**/@(CLAUDE|AGENTS).md"}, "darwin", true},
		{"/repo/note.md", []string{"!**/AGENTS.md"}, "darwin", true},
		{"/repo/AGENTS.md", []string{"!**/AGENTS.md"}, "darwin", false},
		{`C:\repo\.claude\AGENTS.md`, []string{`C:\repo\**\@(CLAUDE|AGENTS).md`}, "win32", true},
		{"/repo/.claude/other.txt", []string{"**/*.md"}, "linux", false},
	}
	for _, tc := range cases {
		matcher, err := NewExclusionMatcher(filepath.EvalSymlinks, tc.platform)
		if err != nil {
			t.Fatal(err)
		}
		got, err := matcher.IsExcluded(tc.path, tc.patterns)
		if err != nil || got != tc.want {
			t.Fatalf("native picomatch %+v got=%v err=%v", tc, got, err)
		}
		for _, name := range []string{"process", "require", "Bun", "fetch", "WebSocket", "console"} {
			value := matcher.vm.Get(name)
			if value != nil {
				t.Fatalf("pure matcher unexpectedly exposed %s", name)
			}
		}
	}
}
func TestNativeExcludesCompareActualPinnedLibrary(t *testing.T) {
	bun := os.Getenv("CC_HAHA_PINNED_BUN")
	if bun == "" {
		t.Skip("actual Source evidence requires explicitly provided pinned Bun 1.3.14")
	}
	source, err := filepath.Abs("testdata/picomatch-source.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		Path     string   `json:"path"`
		Patterns []string `json:"patterns"`
		Platform string   `json:"platform"`
	}{
		{"/repo/.claude/CLAUDE.md", []string{"**/{CLAUDE,AGENTS}.md"}, "darwin"},
		{"/repo/AGENTS.md", []string{"**/@(CLAUDE|AGENTS).md"}, "darwin"},
		{"/repo/note.md", []string{"!**/AGENTS.md"}, "darwin"},
		{"/repo/AGENTS.md", []string{"!**/AGENTS.md"}, "darwin"},
		{`C:\repo\.claude\AGENTS.md`, []string{`C:\repo\**\@(CLAUDE|AGENTS).md`}, "win32"},
		{"/repo/a.md", []string{"**/*.txt", "**/*.md"}, "linux"},
		{"/repo/.hidden/CLAUDE.md", []string{"**/+(CLAUDE|AGENTS).md"}, "linux"},
	}
	raw, _ := json.Marshal(map[string]any{"home": home, "cases": cases})
	cmd := exec.Command(bun, "--no-env-file", source)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_PICOMATCH_FIXTURE=1", "PATH=/usr/bin:/bin", "TMPDIR=" + home}
	cmd.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source picomatch %v: %s", err, stderr.String())
	}
	var expected []bool
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatalf("Source %s: %v", output, err)
	}
	if len(expected) != len(cases) {
		t.Fatal(expected)
	}
	for i, tc := range cases {
		matcher, err := NewExclusionMatcher(filepath.EvalSymlinks, tc.Platform)
		if err != nil {
			t.Fatal(err)
		}
		got, err := matcher.IsExcluded(tc.Path, tc.Patterns)
		if err != nil || got != expected[i] {
			t.Fatalf("actual Source mismatch case %d: Go=%v Source=%v err=%v", i, got, expected[i], err)
		}
	}
}
func TestNativeExcludesResolveAbsoluteSymlinkPrefixes(t *testing.T) {
	m, home := nativeMemory(t)
	writeMemory(t, filepath.Join(m.Normal.CWD, "CLAUDE.md"), "included? @./other.md")
	writeMemory(t, filepath.Join(m.Normal.CWD, "other.md"), "excluded")
	matcher, err := NewExclusionMatcher(filepath.EvalSymlinks, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	m.Normal.Matcher = matcher
	m.Normal.Excludes = []string{filepath.Join(home, "project", "**", "other.md")}
	files, err := m.NormalMemoryFiles("claude-md", nil, false)
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
}
