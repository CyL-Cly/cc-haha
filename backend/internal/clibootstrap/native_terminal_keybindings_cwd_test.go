package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// The live environment assignment is the finite external primitive here.
// Settings' separate frozen-host rejection is deliberately not bypassed in its
// own consumer. This is the registered command body on its real Main owner;
// ordinary RunTurn rejects these changed host values before invoking the body.
func TestWholeSourceKeybindingsBodyCurrentProcessPathsAndMkdirFailures(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-keybindings-slash.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"relative", "empty", "mkdir-self-file", "mkdir-ancestor-file"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.WriteFile(filepath.Join(home, "keybindings-private-process-cwd"), []byte("owned"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, "-test.run=^TestNativeKeybindingsCurrentEnvironmentOwnedHelper$", "-test.count=1", "-test.v")
			command.Dir = home
			command.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "CLAUDE_CONFIG_DIR="+filepath.Join(home, ".claude"), "TMPDIR="+home, "CC_HAHA_KEYBINDINGS_CURRENT_CASE="+name, "CC_HAHA_KEYBINDINGS_CURRENT_SOURCE="+fixture)
			capturePublicGoCoverage(t, command)
			data, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("whole Source current-path runtime %v %.6500s", err, data)
			}
			t.Log(strings.TrimSpace(string(data)))
		})
	}
}

func TestNativeKeybindingsCurrentEnvironmentOwnedHelper(t *testing.T) {
	name := os.Getenv("CC_HAHA_KEYBINDINGS_CURRENT_CASE")
	if name == "" {
		t.Skip("owned process-CWD helper")
	}
	if _, err := os.Stat("keybindings-private-process-cwd"); err != nil {
		t.Fatal("isolated working directory marker missing", err)
	}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("local keybindings reached model", r.URL)
		http.Error(w, "unexpected model", http.StatusInternalServerError)
	}))
	defer peer.Close()
	var source struct {
		Path, Physical string
		Content        *string
		Editors        []struct{ Path, Before, Directory string }
		Logs           []string
		Result         struct {
			Messages   []protocol.JSONObject
			ResultText string
		}
	}
	opened := 0
	m := backgroundAgentMainWithOptions(t, peer.URL, func(native *NativeBoundary) {
		work := filepath.Join(native.Home, "owned-work")
		if err := os.MkdirAll(work, 0700); err != nil {
			t.Fatal(err)
		}
		var err error
		native.CWD, err = filepath.EvalSymlinks(work)
		if err != nil {
			t.Fatal(err)
		}
		native.Environment["EDITOR"] = filepath.Join(native.Home, "owned-editor")
		delete(native.Environment, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC")
	}, func(options *MainPrintOptions) {
		options.Interactive = true
		options.KeybindingCustomizationEnabled = func() bool { return true }
		options.EditTerminalFile = func(_ context.Context, path, _, directory string, _ map[string]string) (string, error) {
			opened++
			if len(source.Editors) != 1 || path != source.Editors[0].Path || directory != source.Editors[0].Directory {
				t.Error("actual original editor argument/CWD differs", path, directory, source.Editors)
			}
			return "", nil
		}
	})
	home := m.options.Native.Home
	input := map[string]any{"home": home}
	config := ""
	switch name {
	case "relative":
		config = "owned-relative-config"
	case "empty":
	case "mkdir-self-file", "mkdir-ancestor-file":
		config = filepath.Join(home, ".claude", "regular-parent")
		input["blockedAncestor"] = config
		if name == "mkdir-ancestor-file" {
			config = filepath.Join(config, "nested")
		}
	default:
		t.Fatal("unknown current-path case", name)
	}
	input["config"] = config
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", os.Getenv("CC_HAHA_KEYBINDINGS_CURRENT_SOURCE"))
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_KEYBINDINGS_SLASH_SOURCE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	command.Stdin = bytes.NewReader(rawMain(input))
	capturePublicGoCoverage(t, command)
	data, err := command.CombinedOutput()
	if err != nil || json.Unmarshal(data, &source) != nil || len(source.Result.Messages) != 3 {
		t.Fatalf("whole original path/error command %v %.5500s", err, data)
	}
	if source.Content != nil {
		if err := os.Remove(source.Physical); err != nil {
			t.Fatal(err)
		}
	}
	text, failure := "", error(nil)
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if err := m.environment.SetProperty(quoteMain("CLAUDE_CONFIG_DIR"), quoteMain(config)); err != nil {
			return err
		}
		text, failure = m.nativeLocalCommand(ctx, scope, "keybindings", "unused arguments")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if opened != len(source.Editors) {
		t.Error("real local editor invocation count", opened, source.Editors)
	}
	if source.Content != nil {
		data, err := os.ReadFile(source.Physical)
		if err != nil || string(data) != *source.Content {
			t.Error("actual file was resolved against another process directory", err, string(data))
		}
	}
	canonical := "<local-command-stdout>" + text + "</local-command-stdout>"
	if failure != nil {
		canonical = "<local-command-stderr>" + storage.JavaScriptErrorText(failure) + "</local-command-stderr>"
	}
	if canonical != source.Result.Messages[2].String("content") {
		t.Error("original local filesystem result differs", canonical, source.Result.Messages[2])
	}
}
