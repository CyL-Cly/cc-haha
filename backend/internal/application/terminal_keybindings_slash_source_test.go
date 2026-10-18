package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestWholeSourceKeybindingsSlashNFCAndThrownEditorErrors(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-keybindings-slash.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"default", "nfc", "missing-ink", "private-editor-error"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.WriteFile(filepath.Join(home, "keybindings-owned-cwd"), []byte("owned"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, "-test.run=^TestNativeKeybindingsSlashOwnedCWDHelper$", "-test.count=1", "-test.v")
			command.Dir = home
			command.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "CLAUDE_CONFIG_DIR="+filepath.Join(home, ".claude"), "TMPDIR="+home, "CC_HAHA_KEYBINDINGS_SLASH_CASE="+name, "CC_HAHA_KEYBINDINGS_SLASH_FIXTURE="+fixture)
			capturePublicGoCoverage(t, command)
			data, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("actual whole Source/native %s %v %.7000s", name, err, data)
			}
			t.Log(strings.TrimSpace(string(data)))
		})
	}
}

func TestNativeKeybindingsSlashOwnedCWDHelper(t *testing.T) {
	name := os.Getenv("CC_HAHA_KEYBINDINGS_SLASH_CASE")
	if name == "" {
		t.Skip("owned isolated CWD helper")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), "keybindings-owned-cwd")); err != nil {
		t.Fatal("physical child CWD is not disposable", err)
	}
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		calls.Add(1)
		if !strings.Contains(string(body), "/keybindings") || !strings.Contains(string(body), "SLASH_KEYBINDINGS_NEXT") {
			t.Error("real next HTTP lost local command", string(body))
		}
		response(w, "slash-keybindings-next", "", nil, "NEXT_DONE")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	o.KeybindingCustomizationEnabled = func() bool { return true }
	o.Environment["DEBUG"], o.Native.Environment["DEBUG"] = "1", "1"
	home := o.Native.Home
	work := filepath.Join(home, "owned-work")
	if err := os.MkdirAll(work, 0700); err != nil {
		t.Fatal(err)
	}
	work, err := filepath.EvalSymlinks(work)
	if err != nil {
		t.Fatal(err)
	}
	o.Native.CWD, o.WorkDir = work, work
	editor := filepath.Join(home, "owned-editor")
	o.Environment["EDITOR"], o.Native.Environment["EDITOR"] = editor, editor
	input := map[string]any{"home": home}
	config, explicit, editorThrow := "", true, ""
	switch name {
	case "default":
		explicit = false
	case "nfc":
		config = filepath.Join(home, ".claude", "owned-e\u0301")
	case "missing-ink":
		explicit, editorThrow = false, "Ink instance not found - cannot pause rendering"
	case "private-editor-error":
		explicit, editorThrow = false, "Owned private editor <error> 中文 😀"
	default:
		t.Fatal("unknown private profile", name)
	}
	if explicit {
		input["config"] = config
	}
	input["editorThrow"] = editorThrow
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("Pinned Source Bun required")
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-keybindings-slash.fixture.ts")
	// The helper's host CWD is its outer private directory, so repository paths
	// come from the parent test binary's explicit source fixture environment.
	if path := os.Getenv("CC_HAHA_KEYBINDINGS_SLASH_FIXTURE"); path != "" {
		fixture = path
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_KEYBINDINGS_SLASH_SOURCE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USER_TYPE=external", "DEBUG=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	command.Stdin = bytes.NewReader(raw(input))
	capturePublicGoCoverage(t, command)
	data, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("whole Source slash graph %v %.5500s", err, data)
	}
	var source struct {
		Path, Physical, Work string
		Content              *string
		Editors              []struct{ Path, Before, Directory string }
		Logs                 []string
		Result               struct {
			Messages    []protocol.JSONObject
			ShouldQuery bool
			ResultText  string
		}
		ResultTextPresent bool
	}
	if err := json.Unmarshal(data, &source); err != nil || source.Result.ShouldQuery || len(source.Result.Messages) != 3 {
		t.Fatalf("whole Source shape %v %.5500s", err, data)
	}
	if source.Content != nil {
		if err := os.Remove(source.Physical); err != nil {
			t.Fatal(err)
		}
	}
	if explicit {
		o.Environment["CLAUDE_CONFIG_DIR"], o.Native.Environment["CLAUDE_CONFIG_DIR"] = config, config
	}
	// Leave native error reporting's real sink enabled, as in the original call.
	delete(o.Environment, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC")
	delete(o.Native.Environment, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC")
	var mu sync.Mutex
	var outputs []protocol.JSONObject
	o.OnSDK = func(data json.RawMessage) error {
		var output protocol.JSONObject
		if err := output.UnmarshalJSON(data); err != nil {
			return err
		}
		mu.Lock()
		outputs = append(outputs, output)
		mu.Unlock()
		return nil
	}
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	logsBefore := 0
	for _, trace := range o.Native.Trace {
		if trace.Operation == "error-log-append" {
			logsBefore++
		}
	}
	opened := 0
	if err := s.Submit(context.Background(), tui.Input{Text: "/keybindings unused arguments", TurnID: "keybindings-slash"}, func(event tui.Event) error {
		if event.Kind == tui.FileEditorRequested {
			opened++
			if event.FileEditor == nil || len(source.Editors) != 1 || event.FileEditor.Path != source.Editors[0].Path || event.FileEditor.Directory != source.Editors[0].Directory {
				t.Error("actual editor path/CWD differs", event.FileEditor, source.Editors)
			}
			var failure error
			if editorThrow != "" {
				failure = errors.New(editorThrow)
			}
			event.FileEditor.Reply <- tui.FileEditorResult{Failure: failure}
		}
		return nil
	}); err != nil {
		t.Fatal("ordinary native slash input", err)
	}
	if opened != len(source.Editors) || calls.Load() != 0 {
		t.Error("local editor/model count", opened, len(source.Editors), calls.Load())
	}
	actual, readErr := os.ReadFile(source.Physical)
	if source.Content != nil && (readErr != nil || string(actual) != *source.Content) {
		t.Error("physical file differs from Source", readErr, string(actual))
	}
	history := s.process.Host.History()
	if len(history) < 3 || history[len(history)-1].Record.String("content") != source.Result.Messages[2].String("content") {
		t.Error("actual canonical stdout/stderr differs", history, source.Result.Messages[2])
	}
	mu.Lock()
	found := false
	for _, output := range outputs {
		if output.String("type") == "result" {
			found = true
			if output.String("result") != source.Result.ResultText {
				t.Error("actual error resultText differs from original undefined value", output.String("result"), source.ResultTextPresent, source.Result.ResultText)
			}
		}
	}
	mu.Unlock()
	if !found {
		t.Error("actual local SDK result missing")
	}
	if err := s.Submit(context.Background(), tui.Input{Text: "SLASH_KEYBINDINGS_NEXT", TurnID: "keybindings-slash-next"}, nil); err != nil || calls.Load() != 1 {
		t.Fatal("same next HTTP consumer", err, calls.Load())
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	logs := 0
	for _, trace := range o.Native.Trace {
		if trace.Operation == "error-log-append" {
			logs++
		}
	}
	if len(source.Logs) != 0 && logs <= logsBefore {
		t.Error("original local catch reported the error but actual native debug sink was not invoked", source.Logs)
	}
	if len(source.Logs) != 0 {
		debug := filepath.Join(o.Native.Environment["CLAUDE_CONFIG_DIR"], "debug", o.Arguments.SessionID+".txt")
		data, err := os.ReadFile(debug)
		if err != nil || !strings.Contains(string(data), source.Logs[len(source.Logs)-1]) {
			t.Error("actual private debug file lost original local error", err, string(data), source.Logs)
		}
	}
}
