package application

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
)

const keybindingsPhysicalEdited = "{\"$schema\":\"private-old-schema\",\"future\":{\"untouched\":71},\"bindings\":[{\"context\":\"Chat\",\"bindings\":{\"ctrl+k ctrl+p\":\"command:help\"}}]}\n"

func TestActualCLIKeybindingsExclusiveCreateEditorHotReloadReopenAndNextHumanPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY; actual Windows ConPTY machine required")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "private-keybindings-command-pty"), []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, "testdata/terminal_keybindings_pty.py", binary, home)
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=" + os.Getenv("PATH"), "TERM=xterm-256color", "CC_HAHA_KEYBINDINGS_COMMAND_PTY_HELPER=1"}
	capturePublicGoCoverage(t, command)
	data, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("actual keybindings command/editor PTY %v %.7000s", err, data)
	}
	if !strings.Contains(string(data), `"terminalRestored": true`) {
		t.Fatal("actual PTY receipts missing", string(data))
	}
}

func TestNativeKeybindingsCommandPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_KEYBINDINGS_COMMAND_PTY_HELPER") != "1" {
		t.Skip("owned isolated CLI helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "private-keybindings-command-pty")); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	var requests, gateCalls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		requests.Add(1)
		if !strings.Contains(string(body), "KEYBINDINGS_COMMAND_NEXT_HUMAN") || strings.Count(string(body), "<command-name>/keybindings</command-name>") != 2 || strings.Count(string(body), "<command-name>/help</command-name>") != 1 || strings.Count(string(body), "<local-command-stdout>Help dialog dismissed</local-command-stdout>") != 1 || strings.Contains(string(body), "<command-name>/permissions</command-name>") {
			t.Error("actual local command history/next human differs", string(body))
		}
		response(w, "keybindings-command-next", "", nil, "KEYBINDINGS_COMMAND_NEXT_COMPLETE_19AC")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Stderr = os.Stderr
	seedTerminalReleaseWarmFixture(t, o)
	path := filepath.Join(o.Native.Environment["CLAUDE_CONFIG_DIR"], "keybindings.json")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("new keybindings fixture unexpectedly exists", err)
	}
	if err := os.WriteFile(filepath.Join(home, "keybindings-command-path.txt"), []byte(path), 0600); err != nil {
		t.Fatal(err)
	}
	o.KeybindingCustomizationEnabled = func() bool {
		calls := gateCalls.Add(1)
		_ = os.WriteFile(filepath.Join(home, "keybindings-command-gate-calls.txt"), []byte(strconv.Itoa(int(calls))), 0600)
		return true
	}
	binary, _ := os.Executable()
	editor := filepath.Join(o.Native.Home, "owned-keybindings-editor")
	script := "#!/bin/sh\nexec '" + strings.ReplaceAll(binary, "'", "'\"'\"'") + "' -test.run=^TestNativeKeybindingsEditorPhysicalChild$ -test.count=1 -- \"$@\"\n"
	if err := os.WriteFile(editor, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"EDITOR": editor, "VISUAL": "", "NO_PROXY": "127.0.0.1,localhost", "CC_HAHA_KEYBINDINGS_EDITOR_CHILD": "1", "CC_HAHA_KEYBINDINGS_EDITOR_STATE": fmt.Sprintf("%+v", before), "CC_HAHA_KEYBINDINGS_EDITOR_RECEIPT": filepath.Join(home, "keybindings-editor-receipt.json")} {
		o.Environment[name], o.Native.Environment[name] = value, value
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	code, err := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, os.Environ())
	if err != nil || code != 0 {
		t.Fatal("actual public CLI", code, err)
	}
	if _, err := nativeTUIPresentationTerminalProbe(os.Stdin.Fd()); err != nil {
		t.Fatal(err)
	}
	after, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != keybindingsPhysicalEdited {
		t.Fatal("actual repeated command overwrote edited unknown fields", err, string(content))
	}
	receipt := map[string]any{"terminalRestored": reflect.DeepEqual(before, after), "requests": requests.Load(), "gateCalls": gateCalls.Load(), "exitCode": code, "editedFilePreserved": true}
	if err := os.WriteFile(filepath.Join(home, "keybindings-command-receipt.json"), raw(receipt), 0600); err != nil {
		t.Fatal(err)
	}
	ackDeadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(home, "keybindings-command-termios-release")); err == nil {
			break
		}
		if time.Now().After(ackDeadline) {
			t.Fatal("physical terminal inspection acknowledgement missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if requests.Load() != 1 {
		t.Fatal("local keybindings/editor/chord invoked a model", receipt)
	}
}

func TestNativeKeybindingsEditorPhysicalChild(t *testing.T) {
	if os.Getenv("CC_HAHA_KEYBINDINGS_EDITOR_CHILD") != "1" {
		t.Skip("owned physical editor child")
	}
	path := os.Args[len(os.Args)-1]
	if path != filepath.Join(os.Getenv("HOME"), ".claude", "keybindings.json") {
		t.Fatal("editor received another file", path)
	}
	state, err := term.GetState(os.Stdin.Fd())
	if err != nil || fmt.Sprintf("%+v", state) != os.Getenv("CC_HAHA_KEYBINDINGS_EDITOR_STATE") {
		t.Fatal("actual terminal was not released to editor", err, state)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := os.Getenv("CC_HAHA_KEYBINDINGS_EDITOR_RECEIPT")
	var receipt []map[string]any
	if data, err := os.ReadFile(receiptPath); err == nil {
		if err := json.Unmarshal(data, &receipt); err != nil {
			t.Fatal(err)
		}
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(receipt) == 0 {
		var template struct {
			Schema   string `json:"$schema"`
			Docs     string `json:"$docs"`
			Bindings []struct{ Context string }
		}
		if json.Unmarshal(content, &template) != nil || template.Schema == "" || template.Docs == "" || len(template.Bindings) == 0 || !strings.HasSuffix(string(content), "\n") {
			t.Fatal("exclusive file was not created as actual template", string(content))
		}
	} else if len(receipt) != 1 || string(content) != keybindingsPhysicalEdited {
		t.Fatal("existing edited metadata replaced on reopen", string(content), len(receipt))
	}
	fmt.Printf("KEYBINDINGS_EDITOR_ACTUAL_TTY_19AC_%d\n", len(receipt)+1)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		t.Fatal("editor lost actual terminal input", err)
	}
	line = strings.TrimSpace(line)
	if len(receipt) == 0 && line == "save-keybindings" {
		if err := os.WriteFile(path, []byte(keybindingsPhysicalEdited), 0600); err != nil {
			t.Fatal(err)
		}
	} else if len(receipt) != 1 || line != "cancel-edit" {
		t.Fatal("unexpected actual editor input", line, len(receipt))
	}
	receipt = append(receipt, map[string]any{"pid": os.Getpid(), "path": path, "terminalReleased": true, "stdin": line, "before": string(content)})
	if err := os.WriteFile(receiptPath, raw(receipt), 0600); err != nil {
		t.Fatal(err)
	}
}
