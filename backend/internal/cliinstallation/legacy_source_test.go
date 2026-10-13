package cliinstallation

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type legacyProfile struct {
	Config     string   `json:"config,omitempty"`
	Zdot       bool     `json:"zdot"`
	Local      bool     `json:"local"`
	Bin        string   `json:"bin"`
	RC         string   `json:"rc"`
	Code       int      `json:"code"`
	Stderr     string   `json:"stderr"`
	PrefixCode int      `json:"prefixCode"`
	Lines      []string `json:"lines"`
}

const privateNPMPeer = `#!/usr/bin/python3
import json,os,sys
home=os.environ['HOME']
with open(os.path.join(home,'npm-profile.json')) as f:p=json.load(f)
with open(os.path.join(home,'npm-calls.jsonl'),'a') as f:f.write(json.dumps({'args':sys.argv[1:],'cwd':os.getcwd(),'home':home})+'\n')
if sys.argv[1:]==['config','get','prefix']:
 print(p['prefix']);sys.exit(p.get('prefixCode',0))
sys.stderr.write(p.get('stderr','')+'\n')
sys.exit(p.get('code',0))
`

func prepareLegacyProfile(t *testing.T, home string, p legacyProfile) (map[string]string, []string) {
	t.Helper()
	config := filepath.Join(home, ".claude")
	if p.Config != "" {
		config = filepath.Join(home, p.Config)
	}
	prefix := filepath.Join(home, "prefix")
	zdot := filepath.Join(home, "zdot")
	for _, dir := range []string{prefix, filepath.Join(home, ".claude/local"), zdot, filepath.Join(home, ".config/fish")} {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{filepath.Join(prefix, "bin"), filepath.Join(prefix, "lib/node_modules/@anthropic-ai/claude-code"), filepath.Join(prefix, "lib/node_modules/claude-code-local"), zdot, filepath.Join(home, ".config/fish")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, text string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(prefix, "lib/node_modules/claude-code-local/keep.txt"), "package data remains", 0600)
	if p.Local {
		if err := os.MkdirAll(filepath.Join(home, ".claude/local"), 0755); err != nil {
			t.Fatal(err)
		}
		write(filepath.Join(home, ".claude/local/old.js"), "legacy local", 0600)
	}
	switch p.Bin {
	case "file":
		write(filepath.Join(prefix, "bin/claude"), "old npm command", 0755)
	case "directory":
		if err := os.Mkdir(filepath.Join(prefix, "bin/claude"), 0755); err != nil {
			t.Fatal(err)
		}
	case "symlink":
		if err := os.Symlink("../lib/node_modules/claude-code-local/keep.txt", filepath.Join(prefix, "bin/claude")); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.Marshal(p)
	var row map[string]any
	_ = json.Unmarshal(raw, &row)
	row["prefix"] = prefix
	raw, _ = json.Marshal(row)
	write(filepath.Join(home, "npm-profile.json"), string(raw), 0600)
	write(filepath.Join(home, "npm-calls.jsonl"), "", 0600)
	lines := []string{}
	for _, line := range p.Lines {
		lines = append(lines, strings.ReplaceAll(strings.ReplaceAll(line, "<local>", filepath.Join(config, "local/claude")), "<home>", home))
	}
	zsh := home
	if p.Zdot {
		zsh = zdot
	}
	paths := []string{filepath.Join(zsh, ".zshrc"), filepath.Join(home, ".bashrc"), filepath.Join(home, ".config/fish/config.fish")}
	for _, path := range paths {
		if err := os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
		if p.RC == "missing" {
			continue
		}
		if p.RC == "directory" {
			if err := os.Mkdir(path, 0755); err != nil {
				t.Fatal(err)
			}
		} else {
			write(path, strings.Join(lines, "\n"), 0640)
		}
	}
	env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": config, "PATH": filepath.Join(home, "bin") + ":/usr/bin:/bin", "BUN_OPTIONS": "--no-env-file", "NODE_ENV": "production", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH": "1"}
	if p.Zdot {
		env["ZDOTDIR"] = zdot
	}
	return env, paths
}

func TestActualSourceLegacyInstallCleanupPhysicalNPMAndShellFiles(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	allocation, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(allocation, "home")
	if err = os.MkdirAll(filepath.Join(home, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "bin/npm"), []byte(privateNPMPeer), 0755); err != nil {
		t.Fatal(err)
	}
	lines := []string{"# user config", `alias claude='<local>'`, "\ufeffalias\u00a0claude = \"<local>\" # original", `alias claude= <local> # unquoted`, "alias claude='~/custom/claude'", `alias claude='$HOME/.claude/local/claude'`, "alias claude='custom command'", `export CLAUDE_ALIAS='alias claude=<local>'`, "alias other='<local>'", "alias claude=\"<local> --argument\"", "unalias claude", "\r", ""}
	profiles := []legacyProfile{
		{Local: true, Code: 0, Lines: lines},
		{Config: "relocated", Zdot: true, Local: true, Bin: "symlink", Code: 1, Stderr: "npm error code ENOTEMPTY", Lines: lines},
		{Bin: "directory", Code: 1, Stderr: "npm error code ENOTEMPTY", Lines: lines},
		{Bin: "file", Code: 1, Stderr: "npm ERR! code E404", Lines: lines},
		{Code: 1, Stderr: "permission denied\r\nsecond line", RC: "missing", Lines: lines},
		{Code: 1, Stderr: "npm error code ENOTEMPTY", PrefixCode: 1, Lines: lines},
		{Code: 1, Lines: lines},
		{Code: 0, RC: "directory", Lines: lines},
	}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "profiles": profiles})
	fixture, _ := filepath.Abs("../../testdata/native-cli-legacy.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "PATH=" + filepath.Join(home, "bin") + ":/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_INSTALLATION_SOURCE_FIXTURE=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation}
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var source struct {
		Observations []json.RawMessage `json:"observations"`
		Blocked      []string          `json:"blocked"`
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Observations) != len(profiles) || len(source.Blocked) > 0 {
		t.Fatal(err, string(out))
	}
	for i, p := range profiles {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			env, paths := prepareLegacyProfile(t, home, p)
			installer, err := New(Options{Home: home, CWD: home, Environment: env, Platform: "darwin", Architecture: "arm64", PackageName: "claude-code-local"})
			if err != nil {
				t.Fatal(err)
			}
			actualLines := []string{}
			for _, line := range p.Lines {
				actualLines = append(actualLines, strings.ReplaceAll(strings.ReplaceAll(line, "<local>", filepath.Join(env["CLAUDE_CONFIG_DIR"], "local/claude")), "<home>", home))
			}
			filtered, had := FilterLegacyAliases(actualLines, filepath.Join(env["CLAUDE_CONFIG_DIR"], "local/claude"))
			npm := installer.CleanupNPM(ctx)
			aliases := installer.CleanupShellAliases()
			files := []map[string]any{}
			for _, path := range paths {
				row := map[string]any{"path": path, "text": nil, "mode": nil}
				if body, e := os.ReadFile(path); e == nil {
					row["text"] = string(body)
					st, _ := os.Stat(path)
					row["mode"] = st.Mode().Perm()
				}
				files = append(files, row)
			}
			body, err := os.ReadFile(filepath.Join(home, "npm-calls.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			calls := []any{}
			for _, line := range strings.Split(string(body), "\n") {
				if line == "" {
					continue
				}
				var row any
				if e := json.Unmarshal([]byte(line), &row); e != nil {
					t.Fatal(e)
				}
				calls = append(calls, row)
			}
			_, localErr := os.Stat(filepath.Join(home, ".claude/local"))
			_, binErr := os.Stat(filepath.Join(home, "prefix/bin/claude"))
			data, _ := os.ReadFile(filepath.Join(home, "prefix/lib/node_modules/claude-code-local/keep.txt"))
			aliasRows := []any{}
			for _, a := range aliases {
				aliasRows = append(aliasRows, map[string]any{"message": a.Message, "userActionRequired": a.UserActionRequired, "type": a.Type})
			}
			actual := map[string]any{"filtered": map[string]any{"filtered": filtered, "hadAlias": had}, "npmResult": map[string]any{"removed": npm.Removed, "errors": npm.Errors, "warnings": npm.Warnings}, "aliases": aliasRows, "files": files, "calls": calls, "localExists": localErr == nil, "binExists": binErr == nil, "packageData": string(data)}
			raw, _ := json.Marshal(actual)
			var goValue, sourceValue any
			_ = json.Unmarshal(raw, &goValue)
			_ = json.Unmarshal(source.Observations[i], &sourceValue)
			if !reflect.DeepEqual(goValue, sourceValue) {
				t.Fatalf("actual Source/Go cleanup mismatch\nSource %s\nGo %s", source.Observations[i], raw)
			}
		})
	}
}
