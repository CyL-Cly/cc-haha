package usercontext

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func nativeMemory(t *testing.T) (*ExplicitUserMemory, string) {
	t.Helper()
	home := t.TempDir()
	cwd := filepath.Join(home, "project", "sub")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	return &ExplicitUserMemory{ConfigHome: filepath.Join(home, ".claude"), Profile: NormalEmptyMemoryProfile, Files: OSMemoryFiles{}, Normal: &NormalMemoryDiscovery{CWD: cwd, Home: home, ManagedRoot: filepath.Join(home, "managed"), NativeProcess: true, EnabledSources: []string{"userSettings", "projectSettings", "localSettings"}, ReadDir: NativeReadDir, Realpath: filepath.EvalSymlinks}}, home
}
func writeMemory(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestNativeAGENTSFallbackAndSeparateFileCache(t *testing.T) {
	m, home := nativeMemory(t)
	writeMemory(t, filepath.Join(home, "project", "AGENTS.md"), "Ancestor agent")
	writeMemory(t, filepath.Join(m.Normal.CWD, "AGENTS.md"), "Child agent")
	files, err := m.NormalMemoryFiles("claude-md-or-agents-md", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Content != "Ancestor agent" || files[1].Content != "Child agent" {
		t.Fatal(files)
	}
	writeMemory(t, filepath.Join(m.Normal.CWD, "CLAUDE.local.md"), "Local disables project-wide fallback")
	same, err := m.NormalMemoryFiles("claude-md-or-agents-md", nil, false)
	if err != nil || !reflect.DeepEqual(same, files) {
		t.Fatal("native file cache was silently recomputed", same, err)
	}
	m.ClearMemoryFileCaches()
	fresh, err := m.NormalMemoryFiles("claude-md-or-agents-md", nil, false)
	if err != nil || len(fresh) != 1 || fresh[0].Type != "Local" {
		t.Fatal("local instructions must disable all project-wide AGENTS fallback", fresh, err)
	}
	both, err := m.NormalMemoryFiles("claude-md-and-agents-md", nil, false)
	if err != nil || len(both) != 3 {
		t.Fatal("mode switch must clear files memo", both, err)
	}
}
func TestNativeInstructionsImportsCommentRulesAndCycle(t *testing.T) {
	_, includes := parseMemory("@./file.md\n", "/project/CLAUDE.md", "Project", "/project/CLAUDE.md", "/home")
	if len(includes) != 1 {
		t.Fatalf("text node import parse: %v", includes)
	}
	m, home := nativeMemory(t)
	root := m.Normal.CWD
	writeMemory(t, filepath.Join(root, "CLAUDE.md"), "<!-- private comment --> Keep note\n\n@./space\\ name.md#heading\n\n`@./ignored.md`\n\n```\n@./ignored.md\n```\n")
	writeMemory(t, filepath.Join(root, "space name.md"), "Included text @./CLAUDE.md")
	writeMemory(t, filepath.Join(root, "ignored.md"), "must not load")
	writeMemory(t, filepath.Join(root, ".claude", "rules", "one.md"), "---\npaths: **\n---\nAlways applies")
	writeMemory(t, filepath.Join(root, ".claude", "rules", "conditional.md"), "---\npaths: src/*.{ts,tsx}\n---\nConditional")
	writeMemory(t, filepath.Join(home, "managed", "CLAUDE.md"), "Managed instruction")
	files, err := m.NormalMemoryFiles("claude-md", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 4 || files[0].Type != "Managed" || files[1].Content != " Keep note\n\n@./space\\ name.md#heading\n\n`@./ignored.md`\n\n```\n@./ignored.md\n```\n" || files[2].Parent != files[1].Path || files[3].Content != "Always applies" {
		t.Fatalf("native parsed files: %+v", files)
	}
	result, err := m.load("claude-md", nil)
	if err != nil {
		t.Fatal(err)
	}
	var text string
	if err := json.Unmarshal(result, &text); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Managed instruction") || strings.Contains(text, "private comment") || strings.Contains(text, "Conditional") || strings.Contains(text, "must not load") {
		t.Fatal(text)
	}
}
func TestNativeUserIncludesExternalButProjectRequiresApproval(t *testing.T) {
	m, home := nativeMemory(t)
	outside := filepath.Join(home, "outside.md")
	writeMemory(t, outside, "External note")
	_, imports := parseMemory("@"+outside, "/project/CLAUDE.md", "User", "/project/CLAUDE.md", home)
	if len(imports) != 1 {
		t.Fatalf("absolute native import parse: %v", imports)
	}
	writeMemory(t, filepath.Join(m.Normal.CWD, "CLAUDE.md"), "@"+outside)
	files, err := m.NormalMemoryFiles("claude-md", nil, false)
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	writeMemory(t, filepath.Join(m.ConfigHome, "CLAUDE.md"), "@"+outside)
	m.ClearMemoryFileCaches()
	files, err = m.NormalMemoryFiles("claude-md", nil, false)
	if err != nil || len(files) != 3 || files[1].Type != "User" || files[1].Content != "External note" {
		t.Fatal(files, err)
	}
}
func TestNativeManagedOnlyStillReadsAutoMemoryEntrypoint(t *testing.T) {
	m, home := nativeMemory(t)
	m.Normal.AutoMemoryEnabled = true
	m.Normal.AutoMemoryDirectory = func() (string, error) { return filepath.Join(home, "memory"), nil }
	writeMemory(t, filepath.Join(home, "memory", "MEMORY.md"), strings.Repeat("entry\n", 205))
	writeMemory(t, filepath.Join(m.Normal.CWD, "CLAUDE.md"), "project")
	files, err := m.NormalMemoryFiles("managed-only", nil, false)
	if err != nil || len(files) != 1 || files[0].Type != "AutoMem" || !strings.Contains(files[0].Content, "205 lines (limit: 200)") {
		t.Fatal(files, err)
	}
}

func TestNativeFallbackMemoIsSharedByApprovalProbeAndContext(t *testing.T) {
	m, _ := nativeMemory(t)
	writeMemory(t, filepath.Join(m.Normal.CWD, "AGENTS.md"), "agent")
	if files, err := m.NormalMemoryFiles("claude-md-or-agents-md", nil, true); err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	writeMemory(t, filepath.Join(m.Normal.CWD, "CLAUDE.md"), "new claude")
	files, err := m.NormalMemoryFiles("claude-md-or-agents-md", nil, false)
	if err != nil || len(files) != 2 || files[0].Content != "new claude" || files[1].Content != "agent" {
		t.Fatal("Source project-wide fallback memo must survive the other force variant", files, err)
	}
	m.ClearMemoryFileCaches()
	files, err = m.NormalMemoryFiles("claude-md-or-agents-md", nil, false)
	if err != nil || len(files) != 1 || files[0].Content != "new claude" {
		t.Fatal("explicit native clear must reset fallback memo", files, err)
	}
}
