package terminalsetup

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type osFiles struct{}

func (osFiles) ReadFile(path string) ([]byte, error)     { return os.ReadFile(path) }
func (osFiles) WriteFile(path string, data []byte) error { return os.WriteFile(path, data, 0666) }
func (osFiles) MkdirAll(path string) error               { return os.MkdirAll(path, 0777) }
func (osFiles) CopyFile(source, target string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(target, data, 0666)
}

func TestWholeSourceTerminalSetupPhysicalConfigurationBackupAndDeclaration(t *testing.T) {
	type profile struct {
		name, terminal, platform, content string
		environment                       map[string]string
	}
	rows := []profile{
		{"code-mac-new", "vscode", "darwin", "", nil}, {"cursor-linux-new", "cursor", "linux", "", nil}, {"windsurf-win-new", "windsurf", "win32", "", nil},
		{"code-comments", "vscode", "darwin", "// retained\n[\n    {\"key\":\"existing\", \"args\": {\"text\": \"中文 😀\"}},\n]\n", nil},
		{"code-bom", "vscode", "darwin", "\ufeff[]", nil}, {"code-existing", "vscode", "darwin", "[" + shiftEnterBindingValue + "]", nil},
		{"code-object", "vscode", "darwin", "{}", nil}, {"code-null", "vscode", "darwin", "null", nil}, {"code-bad", "vscode", "darwin", "invalid", nil}, {"code-null-row", "vscode", "darwin", "[null]", nil},
		{"code-recovered-existing", "vscode", "darwin", "[" + shiftEnterBindingValue + " broken]", nil},
		{"code-recovered-new", "vscode", "darwin", `[{"key":"old"`, nil},
		{"code-recovered-missing-value", "vscode", "darwin", `[{"key":}]`, nil},
		{"code-extra-comma", "vscode", "darwin", `[{},,{}]`, nil},
		{"code-numeric-infinity", "vscode", "darwin", `[1e999]`, nil},
		{"code-root-infinity", "vscode", "darwin", `1e999`, nil},
		{"code-root-zero", "vscode", "darwin", `0`, nil},
		{"code-root-false", "vscode", "darwin", `false`, nil},
		{"code-root-empty-string", "vscode", "darwin", `""`, nil},
		{"code-remote-path", "vscode", "darwin", "", map[string]string{"PATH": "/owned/.vscode-server/bin"}}, {"cursor-remote-askpass", "cursor", "linux", "", map[string]string{"VSCODE_GIT_ASKPASS_MAIN": "/owned/.cursor-server/git"}},
		{"alacritty-new", "alacritty", "darwin", "", nil}, {"alacritty-old", "alacritty", "linux", "# retained\n[window]\nopacity = 0.9", nil}, {"alacritty-existing", "alacritty", "darwin", "mods = \"Shift\"\nkey = \"Return\"\n", nil},
		{"alacritty-xdg", "alacritty", "linux", "# private XDG\n", map[string]string{"XDG_CONFIG_HOME": "private-xdg"}},
		{"zed-new", "zed", "darwin", "", nil}, {"zed-old", "zed", "linux", "[{\"context\":\"Editor\",\"bindings\":{\"a\":\"中文 😀\"}}]", nil}, {"zed-nonarray", "zed", "linux", "{}", nil}, {"zed-comments", "zed", "darwin", "// invalid strict JSON\n[]", nil}, {"zed-existing", "zed", "darwin", "[{\"bindings\":{\"shift-enter\":\"already\"}}]", nil},
		{"ghostty", "ghostty", "darwin", "", nil}, {"kitty", "kitty", "linux", "", nil}, {"iterm", "iTerm.app", "darwin", "", nil}, {"wezterm", "WezTerm", "win32", "", nil}, {"warp-direct-visible", "WarpTerminal", "darwin", "", nil},
		{"tmux", "tmux", "darwin", "", nil}, {"windows-terminal", "WindowsTerminal", "win32", "", nil}, {"unknown", "", "linux", "", nil},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			files := map[string]string{}
			parent := ".config/Code/User/keybindings.json"
			if row.platform == "darwin" {
				parent = "Library/Application Support/Code/User/keybindings.json"
			} else if row.platform == "win32" {
				parent = "AppData/Roaming/Code/User/keybindings.json"
			}
			if row.terminal == "cursor" {
				parent = strings.Replace(parent, "Code/", "Cursor/", 1)
			} else if row.terminal == "windsurf" {
				parent = strings.Replace(parent, "Code/", "Windsurf/", 1)
			} else if row.terminal == "alacritty" {
				parent = ".config/alacritty/alacritty.toml"
				if row.environment["XDG_CONFIG_HOME"] != "" {
					parent = row.environment["XDG_CONFIG_HOME"] + "/alacritty/alacritty.toml"
				}
			} else if row.terminal == "zed" {
				parent = ".config/zed/keymap.json"
			}
			if row.content != "" {
				files[parent] = row.content
			}
			data := terminalSetupSource(t, home, map[string]any{"terminal": row.terminal, "platform": row.platform, "files": files, "environment": row.environment, "global": map[string]any{"shiftEnterKeyBindingInstalled": false}})
			var source struct {
				Declaration struct {
					Name, Type, Description string
					IsHidden                bool
				}
				Offered    bool
				Done       [][]string
				Error      *string
				Files      map[string]string
				Onboarding int
				Global     protocol.JSONObject
			}
			if err = json.Unmarshal(data, &source); err != nil {
				t.Fatalf("actual Source %v %.5000s", err, data)
			}
			// Reset only these owned physical fixture directories to the same
			// original bytes; the two implementations receive the identical path.
			for _, directory := range []string{"Library", ".config", "AppData", "private-xdg"} {
				if err = os.RemoveAll(filepath.Join(home, directory)); err != nil {
					t.Fatal(err)
				}
			}
			for name, content := range files {
				path := filepath.Join(home, name)
				if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(path, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			global := protocol.NewJSONObject(protocol.JSONField{Name: "shiftEnterKeyBindingInstalled", Value: json.RawMessage("false")}, protocol.JSONField{Name: "retainedSetupFixture", Value: json.RawMessage(`{"value":"unknown old field"}`)})
			environment := map[string]string{"PATH": "/usr/bin:/bin"}
			for key, value := range row.environment {
				if key == "XDG_CONFIG_HOME" || key == "APPDATA" {
					value = filepath.Join(home, value)
				}
				environment[key] = value
			}
			onboarding := 0
			output, nativeErr := Run(context.Background(), Options{Home: home, Platform: row.platform, Terminal: row.terminal, Environment: environment, Files: osFiles{}, RandomHex: func() (string, error) { return "10203040", nil }, SaveFields: func(fields protocol.JSONObject) error { global.Assign(fields); return nil }, Onboarding: func() error { onboarding++; return nil }})
			if source.Error != nil {
				if nativeErr == nil || nativeErr.Error() != *source.Error {
					t.Fatalf("Go error%v Source%q", nativeErr, *source.Error)
				}
			} else if nativeErr != nil || len(source.Done) != 1 || output != source.Done[0][0] {
				t.Fatalf("Go output%q err%v Source%+v", output, nativeErr, source.Done)
			}
			actualFiles := map[string]string{}
			err = filepath.WalkDir(home, func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				relative, _ := filepath.Rel(home, path)
				if !entry.IsDir() && !strings.HasPrefix(relative, ".claude") {
					data, readErr := os.ReadFile(path)
					if readErr != nil {
						return readErr
					}
					actualFiles[filepath.ToSlash(relative)] = string(data)
				}
				return nil
			})
			if err != nil || !reflect.DeepEqual(actualFiles, source.Files) || onboarding != source.Onboarding || !bytes.Equal(global.Get("retainedSetupFixture"), source.Global.Get("retainedSetupFixture")) || string(global.Get("shiftEnterKeyBindingInstalled")) != string(source.Global.Get("shiftEnterKeyBindingInstalled")) {
				actualJSON, _ := json.Marshal(actualFiles)
				sourceJSON, _ := json.Marshal(source.Files)
				t.Fatalf("physical files Go%.5000s Source%.5000s; onboarding%d/%d; globals%s/%s err%v", actualJSON, sourceJSON, onboarding, source.Onboarding, global.Get("shiftEnterKeyBindingInstalled"), source.Global.Get("shiftEnterKeyBindingInstalled"), err)
			}
			if source.Declaration.Name != "terminal-setup" || source.Declaration.Type != "local-jsx" || source.Declaration.Description != Description(row.terminal) || source.Declaration.IsHidden != Hidden(row.terminal) || source.Offered != Offered(row.terminal, row.platform) {
				t.Fatalf("actual declaration %+v offered%v", source.Declaration, source.Offered)
			}
		})
	}
}
