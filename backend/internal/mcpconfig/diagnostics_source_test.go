package mcpconfig

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
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func inkDiagnosticLocation(rendered, expected string) bool {
	lines := strings.Split(rendered, "\n")
	for index, line := range lines {
		location, found := strings.CutPrefix(line, "Location:")
		if !found {
			continue
		}
		// The original component gives its "Location: " label nine columns.
		// Only those continuation columns belong to Ink, not the path itself.
		for next := index + 1; next < len(lines); next++ {
			continuation, wrapped := strings.CutPrefix(lines[next], "         ")
			if !wrapped {
				break
			}
			location += continuation
		}
		if location == expected || location == " "+expected {
			return true
		}
	}
	return false
}

func TestInkDiagnosticLocationPreservesPathCharacters(t *testing.T) {
	for _, row := range []struct {
		name, rendered string
		want           bool
	}{
		{"single", "Location:/private/work/.mcp.json\n", true},
		{"label-space", "Location: /private/work/.mcp.json\n", true},
		{"hard-wrap", "Location:/private/work/.mcp.\n         json\n └ [Error] invalid JSON", true},
		{"path-space", "Location:/private/wo rk/.mcp.json\n", false},
		{"wrong-indent", "Location:/private/work/.mcp.\n        json\n", false},
		{"unrelated-line", "[Error] /private/work/.mcp.json\n", false},
	} {
		t.Run(row.name, func(t *testing.T) {
			if got := inkDiagnosticLocation(row.rendered, "/private/work/.mcp.json"); got != row.want {
				t.Fatalf("diagnostic location = %v, want %v", got, row.want)
			}
		})
	}
}

func TestActualSourceMCPDiagnosticsReadAllScopesAndWholeInkConsumer(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual pinned Source Bun required")
	}
	for _, profile := range []string{"empty", "project-warning-and-fatal", "all-four-scopes", "disabled-scopes"} {
		t.Run(profile, func(t *testing.T) {
			s, home, _, _, _, _, sources := fixture(t)
			cwd, managed := filepath.Join(home, "work"), filepath.Join(home, "managed")
			for _, path := range []string{cwd, managed, filepath.Join(home, ".claude"), filepath.Join(home, "tmp")} {
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			globalFile := filepath.Join(home, ".claude", ".claude.json")
			global := protocol.NewJSONObject()
			write := func(path, value string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(value), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(home, ".claude", "settings.json"), `{}`)
			warning := `{"mcpServers":{"warning-peer":{"type":"http","url":"http://127.0.0.1:9/${CC_HAHA_WARNING_MISSING}"}}}`
			if profile != "empty" {
				write(filepath.Join(home, ".mcp.json"), warning)
				write(filepath.Join(cwd, ".mcp.json"), `{broken`)
			}
			if profile == "all-four-scopes" || profile == "disabled-scopes" {
				global.Set("mcpServers", parse(t, warning).Get("mcpServers"))
				local := protocol.NewJSONObject(protocol.JSONField{Name: cwd, Value: json.RawMessage(`{"mcpServers":{"bad":{"command":3}}}`)})
				global.Set("projects", encode(local))
				write(filepath.Join(managed, "managed-mcp.json"), `{"mcpServers":{"bad":{"command":false}}}`)
			}
			if profile == "disabled-scopes" {
				*sources = []string{"policySettings"}
			}
			write(globalFile, string(encode(global)))
			s.options.CWD, s.options.ManagedRoot = cwd, managed
			s.options.Global = func(context.Context) (protocol.JSONObject, error) { return global.Clone(), nil }
			s.options.Project = func(context.Context) (protocol.JSONObject, error) {
				return object(object(global.Get("projects")).Get(cwd)), nil
			}
			absent := map[string]bool{}
			for parent := filepath.Dir(home); ; parent = filepath.Dir(parent) {
				path := filepath.Join(parent, ".mcp.json")
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("diagnostic ancestor not verified absent", path, err)
				}
				absent[path] = true
				if filepath.Dir(parent) == parent {
					break
				}
			}
			s.options.ReadFile = func(path string) ([]byte, error) {
				if absent[path] {
					return nil, &os.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
				}
				if path != home && !strings.HasPrefix(path, home+string(filepath.Separator)) {
					return nil, fmt.Errorf("unexpected diagnostic read: %s", path)
				}
				return os.ReadFile(path)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			fixturePath, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-mcp-diagnostics.fixture.ts")
			command := exec.CommandContext(ctx, bun, "--no-env-file", fixturePath)
			command.Dir = home
			command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_MCP_DIAGNOSTICS_SOURCE=1", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
			command.Stdin = bytes.NewReader(encode(map[string]any{"home": home, "cwd": cwd, "managed": managed, "sources": *sources}))
			var stderr bytes.Buffer
			command.Stderr = &stderr
			data, err := command.Output()
			if err != nil {
				t.Fatal("actual Source diagnostics", err, stderr.String())
			}
			var source struct {
				Scopes       []protocol.JSONObject
				Calls        []string
				Rendered     string
				Capabilities struct{ Blocked []string }
			}
			if err := json.Unmarshal(data, &source); err != nil || len(source.Capabilities.Blocked) != 0 {
				t.Fatal(err, string(data))
			}
			if !reflect.DeepEqual(source.Calls, []string{"user", "project", "local", "enterprise"}) {
				t.Fatal("whole original component did not read original scopes", source.Calls)
			}
			got, err := s.Diagnostics(ctx, globalFile)
			if err != nil {
				t.Fatal(err)
			}
			var left, right any
			_ = json.Unmarshal(encode(got), &left)
			_ = json.Unmarshal(encode(source.Scopes), &right)
			if !reflect.DeepEqual(left, right) {
				t.Errorf("Source actual scope diagnostics differ\nGo %s\nSource %s", encode(got), encode(source.Scopes))
			}
			if profile == "empty" && strings.TrimSpace(source.Rendered) != "" {
				t.Error("empty Source component rendered diagnostics", source.Rendered)
			}
			if profile != "empty" && !strings.Contains(source.Rendered, "MCP Config Diagnostics") {
				t.Error("Source whole Ink diagnostics did not render", source.Rendered)
			}
			if profile == "project-warning-and-fatal" {
				for _, part := range []string{"[Failed to parse]", "[Error]", "[Warning]", "CC_HAHA_WARNING_MISSING", "Location:"} {
					if !strings.Contains(source.Rendered, part) {
						t.Error("Source actual Ink omitted diagnostic", part, source.Rendered)
					}
				}
				// Ink hard-wraps a long machine-specific Location path. The exact
				// structured path is compared above; retain its visible characters
				// here even when the terminal wraps inside the filename.
				if !inkDiagnosticLocation(source.Rendered, filepath.Join(cwd, ".mcp.json")) {
					t.Error("Source actual Ink omitted diagnostic location", source.Rendered)
				}
			}
		})
	}
}
