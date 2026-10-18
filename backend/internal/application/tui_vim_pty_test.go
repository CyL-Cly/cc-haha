package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/charmbracelet/x/term"
)

func TestActualCLIVimConfigOperatorsUTF16SameHarnessAndNormalPTY(t *testing.T) {
	actualCLIVimPTY(t, false)
}

func TestActualCLIVimCommandOperatorsUTF16SameHarnessAndNormalPTY(t *testing.T) {
	actualCLIVimPTY(t, true)
}

func TestActualCLIVimQueuedCommandUsesSameNativeOwnerAndPublishedModePTY(t *testing.T) {
	actualCLIVimPTY(t, true, true)
}

func actualCLIVimPTY(t *testing.T, standalone bool, queued ...bool) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY; physical ConPTY remains separate")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("native Python PTY required")
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual frozen Vim Source requires pinned Bun")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(home, "disposable-vim-pty"), []byte("owned"), 0600)
	keys := []map[string]string{{"name": "esc"}}
	for _, char := range "0dwrBxu0ywp" {
		keys = append(keys, map[string]string{"text": string(char)})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-vim-input.fixture.ts")
	source := exec.Command(bun, "--no-env-file", fixture)
	source.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_VIM_INPUT_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	source.Dir = home
	source.Stdin = bytes.NewReader(raw(map[string]any{"cases": []any{map[string]any{"name": "physical-first", "text": "alpha beta gamma delta", "offset": 22, "columns": 120, "mode": "INSERT", "keys": keys}, map[string]any{"name": "physical-surrogate", "text": "😀", "offset": 2, "columns": 120, "mode": "INSERT", "keys": []any{map[string]any{"name": "esc"}, map[string]any{"text": "x"}}}}}))
	capturePublicGoCoverage(t, source)
	result, err := source.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source hook %v %.2500s", err, result)
	}
	var rows []protocol.JSONObject
	if err = json.Unmarshal(result, &rows); err != nil {
		t.Fatal(err, string(result))
	}
	wants := []json.RawMessage{}
	for _, row := range rows {
		var observations []protocol.JSONObject
		_ = json.Unmarshal(row.Get("observations"), &observations)
		wants = append(wants, observations[len(observations)-1].Get("text"))
	}
	wants = append(wants, raw("normal mode restores plain input"))
	if string(wants[1]) != `"\ud83d"` {
		t.Fatal("actual Source lone high-surrogate proof", string(wants[1]))
	}
	if len(queued) > 0 && queued[0] {
		wants = append([]json.RawMessage{raw("INITIAL_HELD_MODEL")}, wants...)
	}
	_ = os.WriteFile(filepath.Join(home, "vim-source-expected.json"), raw(wants), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_vim_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_VIM_PTY_HELPER=1"}
	if standalone {
		cmd.Env = append(cmd.Env, "CC_HAHA_VIM_PTY_STANDALONE=1")
	}
	if len(queued) > 0 && queued[0] {
		cmd.Env = append(cmd.Env, "CC_HAHA_VIM_PTY_QUEUED=1")
	}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Vim public PTY %v %.7000s", err, output)
	}
	if !strings.Contains(string(output), `"terminalRestored": true`) {
		t.Fatal(string(output))
	}
}

func TestNativeCLIVimPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_VIM_PTY_HELPER") != "1" {
		t.Skip("owned public PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-vim-pty")); err != nil {
		t.Fatal(err)
	}
	expected, _ := os.ReadFile(filepath.Join(home, "vim-source-expected.json"))
	var wants []json.RawMessage
	if err := json.Unmarshal(expected, &wants); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		index := int(requests.Add(1))
		_ = os.WriteFile(filepath.Join(home, "vim-request-"+string(rune('0'+index))+".json"), body, 0600)
		root := obj(body)
		var messages []protocol.JSONObject
		_ = json.Unmarshal(root.Get("messages"), &messages)
		var actual json.RawMessage
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].String("role") != "user" {
				continue
			}
			content := messages[i].Get("content")
			if len(content) > 0 && content[0] == '"' {
				actual = content
				break
			}
			var blocks []protocol.JSONObject
			_ = json.Unmarshal(content, &blocks)
			for _, block := range blocks {
				if block.String("type") == "text" {
					actual = block.Get("text")
				}
			}
			break
		}
		got, err := protocol.DecodeJavaScriptJSONString(actual)
		want, expectedErr := protocol.DecodeJavaScriptJSONString(wants[min(index-1, len(wants)-1)])
		if err != nil || expectedErr != nil || !reflect.DeepEqual(got, want) || index > len(wants) {
			t.Errorf("actual Source input not preserved in canonical POST %d got=%s want=%s err=%v", index, actual, wants[min(index-1, len(wants)-1)], err)
		}
		if os.Getenv("CC_HAHA_VIM_PTY_QUEUED") == "1" && index == 1 {
			_ = os.WriteFile(filepath.Join(home, "vim-model-held"), []byte("actual POST body consumed"), 0600)
			deadline := time.Now().Add(12 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(home, "vim-release-model")); err == nil {
					break
				}
				if time.Now().After(deadline) || r.Context().Err() != nil {
					t.Error("public queued Vim model release missing")
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		response(w, "vim-public-model", "", nil, "VIM_PUBLIC_DONE_"+string(rune('0'+index)))
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	o.Stderr = os.Stderr
	o.Arguments.Tools = []string{""}
	o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrNotExist }
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.MkdirAll(config, 0700)
	global := filepath.Join(config, ".claude.json")
	_ = os.WriteFile(global, raw(map[string]any{"editorMode": "normal", "autoUpdates": false, "futureGlobal": map[string]any{"keep": true}, "hasCompletedOnboarding": true, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}}), 0600)
	_ = os.WriteFile(filepath.Join(home, "vim-paths.json"), raw(map[string]any{"global": global}), 0600)
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 38*time.Second)
	defer cancel()
	code, err := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, os.Environ())
	if err != nil || code != 0 {
		t.Fatal(code, err)
	}
	if _, err = nativeTUIPresentationTerminalProbe(os.Stdin.Fd()); err != nil {
		t.Fatal(err)
	}
	after, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(home, "vim-receipt.json"), raw(map[string]any{"requests": requests.Load(), "terminalRestored": reflect.DeepEqual(before, after), "exitCode": code}), 0600)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err = os.Stat(filepath.Join(home, "vim-pty-release")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("physical terminal inspection acknowledgement missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if requests.Load() != int32(len(wants)) || !reflect.DeepEqual(before, after) {
		t.Fatal("physical Vim flow incomplete", requests.Load())
	}
}
