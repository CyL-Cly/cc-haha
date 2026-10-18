package terminalsetup

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Files interface {
	ReadFile(string) ([]byte, error)
	WriteFile(string, []byte) error
	MkdirAll(string) error
	CopyFile(string, string) error
}

type Options struct {
	Home, Platform, Terminal string
	Environment              map[string]string
	Files                    Files
	Hyperlinks               bool
	Style                    func(string, string) string
	RandomHex                func() (string, error)
	SaveFields               func(protocol.JSONObject) error
	Onboarding               func() error
	Stat                     func(string) (fs.FileInfo, error)
	Execute                  func(context.Context, string, []string) Execution
	GetFields                func() (protocol.JSONObject, error)
	LogError                 func(error)
}

var nativeNames = map[string]string{"ghostty": "Ghostty", "kitty": "Kitty", "iTerm.app": "iTerm2", "WezTerm": "WezTerm", "WarpTerminal": "Warp"}

func Hidden(terminal string) bool { return nativeNames[terminal] != "" && terminal != "WarpTerminal" }
func Description(terminal string) string {
	if terminal == "Apple_Terminal" {
		return "Enable Option+Enter key binding for newlines and visual bell"
	}
	return "Install Shift+Enter key binding for newlines"
}
func Offered(terminal, platform string) bool {
	return platform == "darwin" && terminal == "Apple_Terminal" || terminal == "vscode" || terminal == "cursor" || terminal == "windsurf" || terminal == "alacritty" || terminal == "zed"
}

func (options Options) style(role, text string) string {
	if options.Style != nil {
		return options.Style(role, text)
	}
	return text
}
func (options Options) pathLink(path string) string {
	if !options.Hyperlinks {
		return path
	}
	return "\x1b]8;;" + (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String() + "\x07" + path + "\x1b]8;;\x07"
}
func (options Options) see(path string) string {
	return options.style("dim", "See "+options.pathLink(path)) + "\n"
}
func (options Options) backup(path, label string) (string, error) {
	random := options.RandomHex
	if random == nil {
		random = func() (string, error) {
			var data [4]byte
			_, err := rand.Read(data[:])
			return hex.EncodeToString(data[:]), err
		}
	}
	suffix, err := random()
	if err != nil {
		return "", err
	}
	backup := path + "." + suffix + ".bak"
	if err = options.Files.CopyFile(path, backup); err != nil {
		return options.style("warning", "Error backing up existing "+label+". Bailing out.") + "\n" + options.see(path) + options.style("dim", "Backup path: "+options.pathLink(backup)) + "\n", nil
	}
	return "", nil
}
func inaccessible(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission)
}

func Run(ctx context.Context, options Options) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if name := nativeNames[options.Terminal]; name != "" {
		return "Shift+Enter is natively supported in " + name + ".\n\nNo configuration needed. Just use Shift+Enter to add newlines.", nil
	}
	if !Offered(options.Terminal, options.Platform) {
		terminal := options.Terminal
		if terminal == "" {
			terminal = "your current terminal"
		}
		platform := ""
		if options.Platform == "darwin" {
			platform = "   • macOS: Apple Terminal\n"
		} else if options.Platform == "win32" || options.Platform == "windows" {
			platform = "   • Windows: Windows Terminal\n"
		}
		return "Terminal setup cannot be run from " + terminal + ".\n\nThis command configures a convenient Shift+Enter shortcut for multi-line prompts.\n" + options.style("dim", "Note: You can already use backslash (\\\\) + return to add newlines.") + "\n\nTo set up the shortcut (optional):\n1. Exit tmux/screen temporarily\n2. Run /terminal-setup directly in one of these terminals:\n" + platform + "   • IDE: VSCode, Cursor, Windsurf, Zed\n   • Other: Alacritty\n3. Return to tmux/screen - settings will persist\n\n" + options.style("dim", "Note: iTerm2, WezTerm, Ghostty, Kitty, and Warp support Shift+Enter natively."), nil
	}
	if options.Files == nil || options.SaveFields == nil || options.Onboarding == nil {
		return "", errors.New("terminal setup requires its actual filesystem and configuration consumers")
	}
	var output string
	var err error
	switch options.Terminal {
	case "vscode", "cursor", "windsurf":
		output, err = options.installVSCode()
	case "alacritty":
		output, err = options.installAlacritty()
	case "zed":
		output, err = options.installZed()
	case "Apple_Terminal":
		output, err = options.installApple(ctx)
	default:
		return "", errors.New("Apple Terminal preference setup is not migrated")
	}
	if err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	field := "shiftEnterKeyBindingInstalled"
	if options.Terminal == "Apple_Terminal" {
		field = "optionAsMetaKeyInstalled"
	}
	if err = options.SaveFields(protocol.NewJSONObject(protocol.JSONField{Name: field, Value: json.RawMessage("true")})); err != nil {
		return "", err
	}
	if err = options.Onboarding(); err != nil {
		return "", err
	}
	return output, nil
}

func (options Options) installVSCode() (output string, err error) {
	editor, directory := "VSCode", "Code"
	if options.Terminal == "cursor" {
		editor, directory = "Cursor", "Cursor"
	} else if options.Terminal == "windsurf" {
		editor, directory = "Windsurf", "Windsurf"
	}
	for _, marker := range []string{".vscode-server", ".cursor-server", ".windsurf-server"} {
		if strings.Contains(options.Environment["VSCODE_GIT_ASKPASS_MAIN"], marker) || strings.Contains(options.Environment["PATH"], marker) {
			return options.style("warning", "Cannot install keybindings from a remote "+editor+" session.") + "\n\n" + editor + " keybindings must be installed on your local machine, not the remote server.\n\nTo install the Shift+Enter keybinding:\n1. Open " + editor + " on your local machine (not connected to remote)\n2. Open the Command Palette (Cmd/Ctrl+Shift+P) → \"Preferences: Open Keyboard Shortcuts (JSON)\"\n3. Add this keybinding (the file must be a JSON array):\n\n" + options.style("dim", "[\n  {\n    \"key\": \"shift+enter\",\n    \"command\": \"workbench.action.terminal.sendSequence\",\n    \"args\": { \"text\": \"\\u001b\\r\" },\n    \"when\": \"terminalFocus\"\n  }\n]") + "\n", nil
		}
	}
	defer func() {
		if err != nil {
			err = errors.New("Failed to install " + editor + " terminal Shift+Enter key binding")
		}
	}()
	parent := filepath.Join(options.Home, ".config", directory, "User")
	if options.Platform == "darwin" {
		parent = filepath.Join(options.Home, "Library", "Application Support", directory, "User")
	} else if options.Platform == "win32" || options.Platform == "windows" {
		parent = filepath.Join(options.Home, "AppData", "Roaming", directory, "User")
	}
	path := filepath.Join(parent, "keybindings.json")
	if err = options.Files.MkdirAll(parent); err != nil {
		return "", err
	}
	content := "[]"
	data, readErr := options.Files.ReadFile(path)
	if readErr == nil {
		content = string(protocol.DecodeJavaScriptUTF8(data))
		if output, err = options.backup(path, editor+" terminal keybindings"); err != nil || output != "" {
			return output, err
		}
	} else if !inaccessible(readErr) {
		return "", readErr
	}
	parsed, parseErr := parseJSONC(strings.TrimPrefix(content, "\ufeff"))
	if parseErr == nil {
		if parsed.kind != '[' && !(parsed.kind == 'v' && string(parsed.raw) == "null") {
			return "", errors.New("keybindings.find is not a function")
		}
		for _, child := range parsed.children {
			if child.kind == 'v' && string(child.raw) == "null" {
				return "", errors.New("cannot read properties of null")
			}
			var binding protocol.JSONObject
			if child.kind == '{' && binding.UnmarshalJSON(child.raw) == nil && binding.String("key") == "shift+enter" && binding.String("command") == "workbench.action.terminal.sendSequence" && binding.String("when") == "terminalFocus" {
				return options.style("warning", "Found existing "+editor+" terminal Shift+Enter key binding. Remove it to continue.") + "\n" + options.see(path), nil
			}
		}
	}
	if err = options.Files.WriteFile(path, []byte(appendJSONC(content, json.RawMessage(shiftEnterBindingValue)))); err != nil {
		return "", err
	}
	return options.style("success", "Installed "+editor+" terminal Shift+Enter key binding") + "\n" + options.see(path), nil
}

const shiftEnterBindingValue = `{"key":"shift+enter","command":"workbench.action.terminal.sendSequence","args":{"text":"\u001b\r"},"when":"terminalFocus"}`

func (options Options) installAlacritty() (output string, err error) {
	paths := []string{filepath.Join(options.Home, ".config", "alacritty", "alacritty.toml")}
	if xdg := options.Environment["XDG_CONFIG_HOME"]; xdg != "" {
		paths[0] = filepath.Join(xdg, "alacritty", "alacritty.toml")
	}
	if options.Platform == "win32" || options.Platform == "windows" {
		if appData := options.Environment["APPDATA"]; appData != "" {
			paths = append(paths, filepath.Join(appData, "alacritty", "alacritty.toml"))
		}
	}
	path, content, exists := paths[0], "", false
	for _, candidate := range paths {
		data, readErr := options.Files.ReadFile(candidate)
		if readErr == nil {
			path, content, exists = candidate, string(protocol.DecodeJavaScriptUTF8(data)), true
			break
		}
		if !inaccessible(readErr) {
			return "", readErr
		}
	}
	defer func() {
		if err != nil {
			err = errors.New("Failed to install Alacritty Shift+Enter key binding")
		}
	}()
	if exists {
		if strings.Contains(content, `mods = "Shift"`) && strings.Contains(content, `key = "Return"`) {
			return options.style("warning", "Found existing Alacritty Shift+Enter key binding. Remove it to continue.") + "\n" + options.see(path), nil
		}
		if output, err = options.backup(path, "Alacritty config"); err != nil || output != "" {
			return output, err
		}
	} else if err = options.Files.MkdirAll(filepath.Dir(path)); err != nil {
		return "", err
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += "\n[[keyboard.bindings]]\nkey = \"Return\"\nmods = \"Shift\"\nchars = \"\\u001B\\r\"\n"
	if err = options.Files.WriteFile(path, []byte(content)); err != nil {
		return "", err
	}
	return options.style("success", "Installed Alacritty Shift+Enter key binding") + "\n" + options.style("success", "You may need to restart Alacritty for changes to take effect") + "\n" + options.see(path), nil
}

func (options Options) installZed() (output string, err error) {
	defer func() {
		if err != nil {
			err = errors.New("Failed to install Zed Shift+Enter key binding")
		}
	}()
	path := filepath.Join(options.Home, ".config", "zed", "keymap.json")
	if err = options.Files.MkdirAll(filepath.Dir(path)); err != nil {
		return "", err
	}
	content := "[]"
	data, readErr := options.Files.ReadFile(path)
	if readErr == nil {
		content = string(protocol.DecodeJavaScriptUTF8(data))
		if strings.Contains(content, "shift-enter") {
			return options.style("warning", "Found existing Zed Shift+Enter key binding. Remove it to continue.") + "\n" + options.see(path), nil
		}
		if output, err = options.backup(path, "Zed keymap"); err != nil || output != "" {
			return output, err
		}
	} else if !inaccessible(readErr) {
		return "", readErr
	}
	var values []json.RawMessage
	_ = json.Unmarshal([]byte(content), &values)
	values = append(values, json.RawMessage(`{"context":"Terminal","bindings":{"shift-enter":["terminal::SendText","\u001b\r"]}}`))
	data, err = json.Marshal(values)
	if err != nil {
		return "", err
	}
	data = protocol.StringifyValidatedJavaScriptJSON(data)
	var encoded bytes.Buffer
	if err = json.Indent(&encoded, data, "", "  "); err != nil {
		return "", err
	}
	if err = options.Files.WriteFile(path, append(encoded.Bytes(), '\n')); err != nil {
		return "", err
	}
	return options.style("success", "Installed Zed Shift+Enter key binding") + "\n" + options.see(path), nil
}
