package systemprompt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
)

func TestNativeOutputStyleSourcePrecedenceAndSeparateCaches(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "project", "sub")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("---\nname: same\n---\n"+body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	user := filepath.Join(home, ".claude", "output-styles", "same.md")
	parent := filepath.Join(home, "project", ".claude", "output-styles", "same.md")
	child := filepath.Join(cwd, ".claude", "output-styles", "same.md")
	managed := filepath.Join(home, "managed", ".claude", "output-styles", "same.md")
	write(user, "user")
	write(parent, "parent")
	write(child, "child")
	write(managed, "managed")
	loader, err := NewStyleLoader(StyleLoaderOptions{Environment: testOptions(t).Environment, Platform: "darwin", Home: home, ConfigHome: filepath.Join(home, ".claude"), ManagedRoot: filepath.Join(home, "managed"), Files: usercontext.OSMemoryFiles{}, ReadDir: usercontext.NativeReadDir, Realpath: filepath.EvalSymlinks, Lstat: os.Lstat, ProjectRoot: func() string { return filepath.Join(home, "project") }, SourceEnabled: func(string) bool { return true }, Settings: func() (protocol.JSONObject, error) {
		return protocol.NewJSONObject(protocol.JSONField{Name: "outputStyle", Value: quoted("same")}), nil
	}, PluginStyles: func(context.Context) ([]StyleConfig, error) {
		return []StyleConfig{{OutputStyle: OutputStyle{Name: "same", Prompt: "plugin"}, Source: "plugin"}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := loader.Resolve(context.Background(), cwd)
	if err != nil || first.Prompt != "managed" {
		t.Fatal(first, err)
	}
	write(managed, "changed")
	loader.ClearAll()
	same, err := loader.Resolve(context.Background(), cwd)
	if err != nil || same.Prompt != "managed" {
		t.Fatal("all styles clear must retain directory cache", same, err)
	}
	loader.ClearDirectories()
	loader.ClearAll()
	fresh, err := loader.Resolve(context.Background(), cwd)
	if err != nil || fresh.Prompt != "changed" {
		t.Fatal("separate directory clear must read native file", fresh, err)
	}
}

func TestNativeForcedPluginStyleObjectEnumerationAndSymbolPlatform(t *testing.T) {
	home := t.TempDir()
	options := StyleLoaderOptions{Environment: testOptions(t).Environment, Platform: "win32", Home: home, ConfigHome: filepath.Join(home, ".claude"), ManagedRoot: filepath.Join(home, "managed"), Files: usercontext.OSMemoryFiles{}, ReadDir: usercontext.NativeReadDir, Realpath: filepath.EvalSymlinks, Lstat: os.Lstat, ProjectRoot: func() string { return home }, SourceEnabled: func(string) bool { return false }, Settings: func() (protocol.JSONObject, error) {
		t.Error("forced style should not read selected settings")
		return protocol.NewJSONObject(), nil
	}, PluginStyles: func(context.Context) ([]StyleConfig, error) {
		return []StyleConfig{{OutputStyle: OutputStyle{Name: "10", Prompt: "ten"}, Source: "plugin", ForceForPlugin: true}, {OutputStyle: OutputStyle{Name: "2", Prompt: "two"}, Source: "plugin", ForceForPlugin: true}}, nil
	}}
	loader, err := NewStyleLoader(options)
	if err != nil {
		t.Fatal(err)
	}
	style, err := loader.Resolve(context.Background(), home)
	if err != nil || style.Name != "2" {
		t.Fatal(style, err)
	}
	if loader.star() != "✶" {
		t.Fatal("Windows fallback must preserve Source figure")
	}
	if err := options.Environment.SetProperty(quoted("WT_SESSION"), quoted("fixture")); err != nil {
		t.Fatal(err)
	}
	if loader.star() != "★" {
		t.Fatal("Windows Terminal Unicode Source figure")
	}
}

func TestStyleListSharesPrecedenceDescriptionAndCannotMutateResolver(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude", "output-styles")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"scalar.md": "---\nname: same\ndescription: false\n---\n# ignored",
		"number.md": "---\nname: 0\ndescription: 42\n---\n# ignored",
		"array.md":  "---\ndescription: [invalid]\n---\n\n## First 中文 description\nrest",
		"empty.md":  "---\ndescription: '  '\n---\n",
		"long.md":   "# " + strings.Repeat("😀", 51),
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(path, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	loader, err := NewStyleLoader(StyleLoaderOptions{Environment: testOptions(t).Environment, Platform: "darwin", Home: home, ConfigHome: filepath.Join(home, ".claude"), ManagedRoot: filepath.Join(home, "managed"), Files: usercontext.OSMemoryFiles{}, ReadDir: usercontext.NativeReadDir, Realpath: filepath.EvalSymlinks, Lstat: os.Lstat, ProjectRoot: func() string { return home }, SourceEnabled: func(source string) bool { return source == "userSettings" }, Settings: func() (protocol.JSONObject, error) {
		return protocol.NewJSONObject(protocol.JSONField{Name: "outputStyle", Value: quoted("same")}), nil
	}, PluginStyles: func(context.Context) ([]StyleConfig, error) { return nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	list, err := loader.List(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{"same": "false", "number": "42", "array": "First 中文 description", "empty": "Custom empty output style", "Explanatory": "Claude explains its implementation choices and codebase patterns", "Learning": "Claude pauses and asks you to write small pieces of code for hands-on practice", "long": strings.Repeat("😀", 48) + "�..."}
	for _, style := range list {
		if description, ok := expected[style.Name]; ok {
			if style.Description != description {
				t.Fatalf("%s description: %q", style.Name, style.Description)
			}
			delete(expected, style.Name)
		}
	}
	if len(expected) != 0 {
		t.Fatal("missing list items", expected)
	}
	for i := range list {
		list[i].Name = "mutated"
		list[i].Prompt = "mutated"
	}
	selected, err := loader.Resolve(context.Background(), home)
	if err != nil || selected == nil || selected.Name != "same" || selected.Prompt != "# ignored" {
		t.Fatal("UI mutation changed prompt cache", selected, err)
	}
}
