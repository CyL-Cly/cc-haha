package systemprompt

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
)

// This test runs the actual original prompt producer with original native
// inputs. Source sections are only the comparator output, never Go inputs.
func TestActualSourceDefaultSections(t *testing.T) {
	bun := os.Getenv("CC_HAHA_PINNED_BUN")
	if bun == "" {
		t.Skip("actual Source evidence requires explicitly provided pinned Bun 1.3.14")
	}
	source, err := filepath.Abs("testdata/source.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	uname := func(flag string) string {
		t.Helper()
		b, err := exec.Command("/usr/bin/uname", flag).Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(b))
	}
	info := OSInfo{Type: uname("-s"), Release: uname("-r")}
	type originalStyleFile struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	for _, tc := range []struct {
		name, model, language              string
		simple, interactive, memory        bool
		tools                              []string
		style                              string
		files                              []originalStyleFile
		agentEnvironment, agentCwdOverride bool
		additional                         []string
		baseURL                            string
		sequenceModel                      string
	}{
		{name: "deepseek-normal-memory", model: "deepseek-v4-flash", interactive: true, memory: true, tools: []string{"Read", "Edit", "Write", "Bash", "Agent"}},
		{name: "model-cache-clear", model: "actual-before-model", sequenceModel: "actual-after-model", interactive: false, memory: false, tools: []string{"Read"}},
		{name: "language-tools", model: "claude-sonnet-4-5[1m]", language: "中文", interactive: false, tools: []string{"AskUserQuestion", "TodoWrite", "TaskCreate", "Read"}},
		{name: "simple", model: "deepseek-v4-flash", simple: true, memory: true, interactive: true, tools: []string{"Read"}},
		{name: "deepseek-official-base", model: "deepseek-v4-flash", interactive: true, memory: true, tools: []string{"Read", "Bash"}, baseURL: "https://api.deepseek.com/anthropic"},
		{name: "child-agent-environment", model: "claude-sonnet-4-5[1m]", interactive: true, tools: []string{}, agentEnvironment: true, additional: []string{"/original/additional-one", "/original/additional-two"}},
		{name: "isolated-child-agent-environment", model: "fixture-model", interactive: true, tools: []string{}, agentEnvironment: true, agentCwdOverride: true},
		{name: "builtin-explanatory", model: "deepseek-v4-flash", interactive: true, style: "Explanatory", tools: []string{"Read"}},
		{name: "builtin-learning", model: "deepseek-v4-flash", interactive: true, style: "Learning", tools: []string{"Read"}},
		{name: "native-custom-style", model: "deepseek-v4-flash", interactive: true, style: "concise", tools: []string{"Read"}, files: []originalStyleFile{{Path: ".claude/output-styles/concise.md", Content: "---\nname: concise\nkeep-coding-instructions: false\n---\nAlways concise 中文\n"}}},
		{name: "project-style-priority", model: "deepseek-v4-flash", interactive: true, style: "same", tools: []string{"Read"}, files: []originalStyleFile{{Path: ".claude/output-styles/user.md", Content: "---\nname: same\n---\nUser"}, {Path: "project/.claude/output-styles/project.md", Content: "---\nname: same\nkeep-coding-instructions: true\n---\nProject"}}},
	} {
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
			input := map[string]any{"home": home, "model": tc.model, "tools": tc.tools, "language": tc.language, "simple": tc.simple, "interactive": tc.interactive, "autoMemory": tc.memory, "style": tc.style, "files": tc.files, "sources": []string{"userSettings", "projectSettings"}, "agentEnvironment": tc.agentEnvironment, "agentCwdOverride": tc.agentCwdOverride, "additional": tc.additional, "baseURL": tc.baseURL}
			raw, err := json.Marshal(input)
			if tc.sequenceModel != "" {
				input["sequenceModel"] = tc.sequenceModel
				raw, err = json.Marshal(input)
			}
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bun, "--no-env-file", "--feature=TRANSCRIPT_CLASSIFIER", source)
			cmd.Dir = home
			cmd.Stdin = bytes.NewReader(raw)
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "XDG_CACHE_HOME=" + filepath.Join(home, "cache"), "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "SHELL=/bin/zsh", "USER_TYPE=external", "ANTHROPIC_API_KEY=fixture", "CC_HAHA_SYSTEM_PROMPT_FIXTURE=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "TZ=UTC", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			output, err := cmd.Output()
			if err != nil {
				t.Fatalf("Source failed: %v\n%s", err, stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("Source unexpected diagnostic: %s", stderr.String())
			}
			var expected struct {
				Sections []json.RawMessage   `json:"sections"`
				Sequence [][]json.RawMessage `json:"sequence"`
				Styles   []struct {
					Name, Description, Source, Prompt string
					Keep, Nil                         bool
				} `json:"styles"`
				Capabilities struct {
					Blocked     []any `json:"blocked"`
					SecureReads []any `json:"secureReads"`
				} `json:"capabilities"`
			}
			if err := json.Unmarshal(output, &expected); err != nil {
				t.Fatalf("Source output: %s\n%v", output, err)
			}
			if len(expected.Capabilities.Blocked) != 0 || len(expected.Capabilities.SecureReads) != 0 {
				t.Fatalf("Source crossed fixture boundary: %s", output)
			}
			options := testOptions(t)
			options.GlobalCacheScope = func() bool { return tc.baseURL == "" }
			options.CWD = func() string { return filepath.Join(home, "project") }
			options.Model = tc.model
			options.Platform = runtime.GOOS
			if runtime.GOOS == "windows" {
				options.Platform = "win32"
			}
			options.OSInfo = func() (OSInfo, error) { return info, nil }
			options.NonInteractive = func() bool { return !tc.interactive }
			styles, err := NewStyleLoader(StyleLoaderOptions{Environment: options.Environment, Platform: options.Platform, Home: home, ConfigHome: filepath.Join(home, ".claude"), ManagedRoot: filepath.Join(home, "managed"), Files: usercontext.OSMemoryFiles{}, ReadDir: usercontext.NativeReadDir, Realpath: filepath.EvalSymlinks, Lstat: os.Lstat, ProjectRoot: options.CWD, SourceEnabled: func(source string) bool { return source == "userSettings" || source == "projectSettings" }, Settings: func() (protocol.JSONObject, error) {
				return protocol.NewJSONObject(protocol.JSONField{Name: "outputStyle", Value: quoted(tc.style)}), nil
			}, PluginStyles: func(context.Context) ([]StyleConfig, error) { return nil, nil }})
			if err != nil {
				t.Fatal(err)
			}
			options.OutputStyle = styles.Resolve
			actualStyles, err := styles.List(context.Background(), options.CWD())
			if err != nil || len(actualStyles) != len(expected.Styles) {
				t.Fatal("style list count differs", actualStyles, expected.Styles, err)
			}
			for i, style := range actualStyles {
				sourceStyle := expected.Styles[i]
				if style.Name != sourceStyle.Name || style.Description != sourceStyle.Description || style.Source != sourceStyle.Source || style.Prompt != sourceStyle.Prompt || style.KeepCodingInstructions != sourceStyle.Keep || style.Nil != sourceStyle.Nil {
					t.Fatalf("style list %d differs\nGo: %+v\nSource: %+v", i, style, sourceStyle)
				}
			}
			options.InitialSettings = func() (protocol.JSONObject, error) {
				memory := json.RawMessage("false")
				if tc.memory {
					memory = json.RawMessage("true")
				}
				return protocol.NewJSONObject(protocol.JSONField{Name: "language", Value: quoted(tc.language)}, protocol.JSONField{Name: "autoMemoryEnabled", Value: memory}), nil
			}
			if tc.simple {
				_ = options.Environment.SetProperty(quoted("CLAUDE_CODE_SIMPLE"), quoted("1"))
			}
			key := projectpath.Sanitize(filepath.Join(home, "project"))
			options.AutoMemory = &AutoMemory{Directory: func() (string, error) {
				return filepath.Join(home, ".claude", "projects", key, "memory") + string(filepath.Separator), nil
			}, Mkdir: func(path string) error { return os.MkdirAll(path, 0700) }}
			service, err := New(options)
			if err != nil {
				t.Fatal(err)
			}
			tools := []protocol.JSONObject{}
			for _, name := range tc.tools {
				tools = append(tools, protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quoted(name)}))
			}
			var actual []json.RawMessage
			if tc.agentEnvironment {
				section, envError := service.AgentEnvironment(context.Background(), tc.model, tc.additional)
				if tc.agentCwdOverride {
					section, envError = service.AgentEnvironmentAt(context.Background(), tc.model, tc.additional, filepath.Join(home, "project", "child"), options.IsGit)
				}
				actual = []json.RawMessage{section}
				err = envError
			} else {
				actual, err = service.Resolve(context.Background(), tools)
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(actual) != len(expected.Sections) {
				t.Fatalf("section count Go=%d Source=%d", len(actual), len(expected.Sections))
			}
			for i := range actual {
				a, err := protocol.DecodeJavaScriptJSONString(actual[i])
				if err != nil {
					t.Fatal(err)
				}
				b, err := protocol.DecodeJavaScriptJSONString(expected.Sections[i])
				if err != nil {
					t.Fatal(err)
				}
				if !equalUnits(a, b) {
					t.Fatalf("section %d differs\nGo: %s\nSource: %s", i, actual[i], expected.Sections[i])
				}
			}
			if tc.sequenceModel != "" {
				for stage, sourceSections := range expected.Sequence {
					if stage == 1 {
						service.Clear()
					}
					parts, err := service.ResolveForModel(context.Background(), tools, tc.sequenceModel)
					if err != nil || len(parts) != len(sourceSections) {
						t.Fatal("current model section count", err)
					}
					for i := range parts {
						a, _ := protocol.DecodeJavaScriptJSONString(parts[i])
						b, _ := protocol.DecodeJavaScriptJSONString(sourceSections[i])
						if !equalUnits(a, b) {
							t.Fatalf("Source model cache stage %d section %d: Go %s Source %s", stage, i, parts[i], sourceSections[i])
						}
					}
				}
			}
		})
	}
}

func equalUnits(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
