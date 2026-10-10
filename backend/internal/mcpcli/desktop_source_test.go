package mcpcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestMCPDesktopReaderAndSelectionMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	winPath := "/mnt/c/Users/Ada/AppData/Roaming/Claude/claude_desktop_config.json"
	rows := []map[string]any{}
	for _, raw := range []string{`{}`, `null`, `[]`, `broken`, `{"mcpServers":{"plain":{"command":"run","extra":"strip"},"typed":{"type":"stdio","command":"other","env":{"A":"1"}},"remote":{"type":"http","url":"https://private.invalid"},"invalid":{"command":""},"badargs":{"command":"ok","args":[1]},"__proto__":{"command":"ignored"}}}`, `{"mcpServers":[{"command":"index-zero"},false,{"command":"index-two","args":[]}]}`} {
		rows = append(rows, map[string]any{"platform": "macos", "content": raw})
	}
	rows = append(rows, map[string]any{"platform": "macos"}, map[string]any{"platform": "macos", "readError": "EACCES"}, map[string]any{"platform": "windows"}, map[string]any{"platform": "linux"}, map[string]any{"platform": "wsl", "userProfile": `C:\Users\Ada`, "present": []string{winPath}, "content": `{"mcpServers":{"ok":{"command":"run"}}}`}, map[string]any{"platform": "wsl", "userProfile": `c:\Users\Ada`, "users": []string{"Public", "Default", "Default User", "All Users", "Ada"}, "present": []string{winPath}, "content": `{}`}, map[string]any{"platform": "wsl", "users": []string{"Unknown"}})
	selections := []map[string]any{
		{"servers": map[string]any{"one": map[string]any{"command": "one"}, "two": map[string]any{"command": "two"}}, "existing": map[string]any{"one": map[string]any{}}, "scope": "user"},
		{"servers": map[string]any{"one": map[string]any{"command": "one"}, "one_2": map[string]any{"command": "next"}}, "existing": map[string]any{"one": map[string]any{}, "one_1": map[string]any{}}, "selected": []string{"one", "one_2"}, "scope": "local"},
		{"servers": map[string]any{"one": map[string]any{"command": "one"}}, "selected": []string{}, "scope": "project"},
		{"servers": map[string]any{"one": map[string]any{"command": "one"}}, "scope": "user", "cancel": true},
	}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows, "selections": selections})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cli-mcp-desktop.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLI_MCP_DESKTOP_FIXTURE=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	result, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Rows       []json.RawMessage
		Selections []json.RawMessage
	}
	if err = json.Unmarshal(result, &source); err != nil {
		t.Fatal(err, string(result))
	}
	for i, row := range rows {
		calls := []map[string]any{}
		o := DesktopFiles{Platform: row["platform"].(string), Home: home, UserProfile: func() string { value, _ := row["userProfile"].(string); return value }()}
		o.Stat = func(path string) (fs.FileInfo, error) {
			calls = append(calls, map[string]any{"op": "stat", "path": path})
			for _, present := range stringsSlice(row["present"]) {
				if path == present {
					return os.Stat(home)
				}
			}
			return nil, fs.ErrNotExist
		}
		o.ReadDir = func(path string) ([]fs.DirEntry, error) {
			calls = append(calls, map[string]any{"op": "readdir", "path": path})
			if row["users"] == nil {
				return nil, fs.ErrNotExist
			}
			entries := []fs.DirEntry{}
			for _, name := range stringsSlice(row["users"]) {
				entries = append(entries, desktopEntry(name))
			}
			return entries, nil
		}
		o.ReadFile = func(path string) ([]byte, error) {
			calls = append(calls, map[string]any{"op": "read", "path": path})
			if content, ok := row["content"].(string); ok {
				return []byte(content), nil
			}
			return nil, fs.ErrNotExist
		}
		p, pathErr := DesktopConfigPath(o)
		calls = []map[string]any{}
		servers, readErr := ReadDesktopServers(o)
		actual := map[string]any{"path": nil, "pathError": nil, "servers": nil, "readError": nil, "calls": calls}
		if pathErr == nil {
			actual["path"] = p
		} else {
			actual["pathError"] = pathErr.Error()
		}
		if readErr == nil {
			actual["servers"] = servers
		} else {
			actual["readError"] = readErr.Error()
		}
		compareMCPJSON(t, i, actual, source.Rows[i])
	}
	for i, row := range selections {
		servers := object(encode(row["servers"]))
		existing := object(encode(row["existing"]))
		selection := NewDesktopSelection(servers, existing)
		defaults := append([]string{}, selection.Selected...)
		if selected, ok := row["selected"]; ok {
			selection.Selected = stringsSlice(selected)
		}
		mutations := []map[string]any{}
		count := 0
		if row["cancel"] != true {
			count, err = ImportDesktopSelection(ctx, servers, selection, row["scope"].(string), func(ctx context.Context, name, scope string, raw []byte) error {
				mutations = append(mutations, map[string]any{"name": name, "scope": scope, "config": object(raw)})
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		output := "\nNo servers were imported."
		if count > 0 {
			noun := "server"
			if count != 1 {
				noun = "servers"
			}
			output = fmtMCPImported(count, noun, row["scope"].(string))
		}
		compareMCPJSON(t, i, map[string]any{"defaults": defaults, "mutations": mutations, "output": output}, source.Selections[i])
	}
}
func fmtMCPImported(count int, noun, scope string) string {
	return fmt.Sprintf("\nSuccessfully imported %d MCP %s to %s config.\n", count, noun, scope)
}
func stringsSlice(raw any) []string { v, _ := raw.([]string); return v }

type desktopEntry string

func (e desktopEntry) Name() string               { return string(e) }
func (e desktopEntry) IsDir() bool                { return true }
func (e desktopEntry) Type() fs.FileMode          { return fs.ModeDir }
func (e desktopEntry) Info() (fs.FileInfo, error) { return nil, fs.ErrNotExist }
func compareMCPJSON(t *testing.T, i int, actual any, want json.RawMessage) {
	t.Helper()
	raw, _ := json.Marshal(actual)
	a, _ := protocol.ParseAndStringifyJavaScriptJSON(raw)
	b, _ := protocol.ParseAndStringifyJavaScriptJSON(want)
	var x, y any
	_ = json.Unmarshal(a, &x)
	_ = json.Unmarshal(b, &y)
	if !reflect.DeepEqual(x, y) {
		t.Errorf("case%d Go%s Source%s", i, a, b)
	}
}
