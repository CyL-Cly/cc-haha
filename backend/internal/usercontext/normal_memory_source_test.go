package usercontext

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceNativeInstructionDiscovery(t *testing.T) {
	bun := os.Getenv("CC_HAHA_PINNED_BUN")
	if bun == "" {
		t.Skip("actual Source evidence requires explicitly provided pinned Bun 1.3.14")
	}
	source, err := filepath.Abs("testdata/normal-memory-source.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	type originalFile struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	cases := []struct {
		name, mode string
		files      []originalFile
		additional []string
		memory     bool
	}{
		{name: "active-agents-fallback", mode: "claude-md-or-agents-md", files: []originalFile{{"project/AGENTS.md", "parent"}, {"project/sub/AGENTS.md", "child"}}},
		{name: "local-disables-all-fallback", mode: "claude-md-or-agents-md", files: []originalFile{{"project/AGENTS.md", "parent"}, {"project/sub/AGENTS.md", "child"}, {"project/sub/CLAUDE.local.md", "local"}}},
		{name: "markdown-imports-rules", mode: "claude-md", files: []originalFile{{"project/sub/CLAUDE.md", "<!-- comment --> Keep note\n\n@./space\\ name.md#section\n\n`@./ignored.md`\n\n```\n@./ignored.md\n```\n"}, {"project/sub/space name.md", "Included @./CLAUDE.md"}, {"project/sub/ignored.md", "ignored"}, {"project/sub/.claude/rules/always.md", "---\npaths: **\n---\nAlways"}, {"project/sub/.claude/rules/conditional.md", "---\npaths: src/*.{ts,tsx}\n---\nConditional"}}},
		{name: "comments-crlf-code-links", mode: "claude-md", files: []originalFile{{"project/sub/CLAUDE.md", "First <!-- inline --> text\r\n\r\n<!-- only comment -->\r\n\r\n[link @./link.md](https://example.invalid)\r\n\r\n> @./quote.md\r\n\r\n- @./list.md\r\n\r\n```md\r\n<!-- code -->\r\n@./ignored.md\r\n```\r\n"}, {"project/sub/link.md", "linked"}, {"project/sub/quote.md", "quoted"}, {"project/sub/list.md", "listed"}, {"project/sub/ignored.md", "ignored"}}},
		{name: "both-deduplicates-agents-content", mode: "claude-md-and-agents-md", files: []originalFile{{"project/CLAUDE.md", "same"}, {"project/sub/AGENTS.md", "same"}, {"project/sub/.claude/AGENTS.md", "unique"}}},
		{name: "additional-own-fallback", mode: "claude-md-or-agents-md", additional: []string{"extra"}, files: []originalFile{{"project/sub/CLAUDE.md", "primary"}, {"extra/AGENTS.md", "extra agent"}}},
		{name: "memory-entrypoint", mode: "claude-md", memory: true, files: []originalFile{{"memory/MEMORY.md", "---\ntype: user\n---\nMemory index\n@./not-an-import.md\n"}, {"memory/not-an-import.md", "not imported"}}},
		{name: "memory-entrypoint-UTF16-cut", mode: "claude-md", memory: true, files: []originalFile{{"memory/MEMORY.md", strings.Repeat("a", 24999) + "😀tail"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			allocation := t.TempDir()
			home := filepath.Join(allocation, "home")
			if err := os.MkdirAll(home, 0700); err != nil {
				t.Fatal(err)
			}
			for _, leaf := range []string{"tmp", "config", "cache"} {
				if err := os.MkdirAll(filepath.Join(home, leaf), 0700); err != nil {
					t.Fatal(err)
				}
			}
			input := map[string]any{"home": home, "cwd": "project/sub", "mode": tc.mode, "files": tc.files, "sources": []string{"userSettings", "projectSettings", "localSettings"}, "additional": tc.additional, "memory": tc.memory}
			raw, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bun, "--no-env-file", "--feature=TRANSCRIPT_CLASSIFIER", source)
			cmd.Dir = home
			cmd.Stdin = bytes.NewReader(raw)
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "XDG_CACHE_HOME=" + filepath.Join(home, "cache"), "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "SHELL=/bin/zsh", "USER_TYPE=external", "ANTHROPIC_API_KEY=fixture", "CC_HAHA_MEMORY_FIXTURE=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "TZ=UTC", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			output, err := cmd.Output()
			if err != nil {
				t.Fatalf("Source failed %v\n%s", err, stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("Source diagnostic: %s", stderr.String())
			}
			var expected struct {
				Files        []MemoryFile    `json:"files"`
				ClaudeMd     json.RawMessage `json:"claudeMd"`
				Capabilities struct {
					Blocked     []any `json:"blocked"`
					SecureReads []any `json:"secureReads"`
				} `json:"capabilities"`
			}
			if err := json.Unmarshal(output, &expected); err != nil {
				t.Fatalf("Source output %s: %v", output, err)
			}
			if len(expected.Capabilities.Blocked) > 0 || len(expected.Capabilities.SecureReads) > 0 {
				t.Fatalf("Source isolation guard: %s", output)
			}
			m := &ExplicitUserMemory{ConfigHome: filepath.Join(home, ".claude"), Profile: NormalEmptyMemoryProfile, Files: OSMemoryFiles{}, Normal: &NormalMemoryDiscovery{CWD: filepath.Join(home, "project", "sub"), Home: home, ManagedRoot: filepath.Join(home, "managed"), NativeProcess: true, EnabledSources: []string{"userSettings", "projectSettings", "localSettings"}, ReadDir: NativeReadDir, Realpath: filepath.EvalSymlinks, AdditionalDirectoriesEnabled: true, AutoMemoryEnabled: tc.memory, AutoMemoryDirectory: func() (string, error) { return filepath.Join(home, "memory"), nil }}}
			additional := []string{}
			for _, path := range tc.additional {
				additional = append(additional, filepath.Join(home, path))
			}
			files, err := m.NormalMemoryFiles(tc.mode, additional, false)
			if err != nil {
				t.Fatal(err)
			}
			ordinaryFiles := append([]MemoryFile(nil), files...)
			for i := range ordinaryFiles {
				ordinaryFiles[i].contentCodeUnits = nil
			}
			if !reflect.DeepEqual(ordinaryFiles, expected.Files) {
				a, _ := json.MarshalIndent(files, "", " ")
				b, _ := json.MarshalIndent(expected.Files, "", " ")
				t.Fatalf("native discovery differs\nGo %s\nSource %s", a, b)
			}
			var sourceRecord protocol.JSONObject
			if err := sourceRecord.UnmarshalJSON(output); err != nil {
				t.Fatal(err)
			}
			var sourceFiles []protocol.JSONObject
			if err := json.Unmarshal(sourceRecord.Get("files"), &sourceFiles); err != nil {
				t.Fatal(err)
			}
			for i, file := range files {
				a, err := protocol.DecodeJavaScriptJSONString(file.PromptContent())
				if err != nil {
					t.Fatal(err)
				}
				b, err := protocol.DecodeJavaScriptJSONString(sourceFiles[i].Get("content"))
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(a, b) {
					t.Fatalf("native file %d lost original UTF16 content", i)
				}
			}
			claude, err := m.load(tc.mode, additional)
			if err != nil {
				t.Fatal(err)
			}
			if claude == nil {
				claude = json.RawMessage(`""`)
			}
			a, err := protocol.DecodeJavaScriptJSONString(claude)
			if err != nil {
				t.Fatal(err)
			}
			b, err := protocol.DecodeJavaScriptJSONString(expected.ClaudeMd)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("instruction context differs\nGo %s\nSource %s", claude, expected.ClaudeMd)
			}
		})
	}
}
