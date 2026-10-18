package application

import (
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

	"github.com/charmbracelet/x/term"
)

func TestActualPublicCLIConversationCopyExportRenameColorAndNextHumanPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("actual Unix PTY; ConPTY requires Windows host")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("actual native Python PTY unavailable")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"ordinary", "cached-marketing", "sandbox"} {
		t.Run(profile, func(t *testing.T) {
			home := t.TempDir()
			if err := os.WriteFile(filepath.Join(home, "owned-conversation-pty"), []byte("isolated"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, python, "testdata/terminal_conversation_pty.py", binary, home, profile)
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_CONVERSATION_PTY_HELPER=1", "CC_HAHA_CONVERSATION_PTY_PROFILE=" + profile}
			capturePublicGoCoverage(t, cmd)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatal("actual public conversation PTY", err, string(output))
			}
			if !strings.Contains(string(output), `"terminalRestored": true`) {
				t.Fatal("physical restoration receipt missing", string(output))
			}
		})
	}
}

func TestNativeTerminalConversationPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_CONVERSATION_PTY_HELPER") != "1" {
		t.Skip("owned isolated public CLI helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "owned-conversation-pty")); err != nil {
		t.Fatal("private fixture marker missing")
	}
	var ordinary, names atomic.Int32
	var work string
	marketing := os.Getenv("CC_HAHA_CONVERSATION_PTY_PROFILE") == "cached-marketing"
	sandbox := os.Getenv("CC_HAHA_CONVERSATION_PTY_PROFILE") == "sandbox"
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		if marketing && (r.Header.Get("Authorization") != "Bearer fake-private-export-token" || r.Header.Get("X-Api-Key") != "") {
			t.Error("cached marketing next model lost actual credential producer", r.Header)
		}
		if strings.Contains(string(body), "Generate a short kebab-case name") {
			names.Add(1)
			response(w, "pty-name", "", nil, `{"name":"auto-pty-topic"}`)
			return
		}
		n := ordinary.Add(1)
		if strings.Contains(string(body), "/copy") || strings.Contains(string(body), "/export") || strings.Contains(string(body), "/rename") || strings.Contains(string(body), "/color") {
			t.Error("local public command leaked to model")
		}
		switch n {
		case 1:
			response(w, "pty-file", "Write", map[string]string{"file_path": filepath.Join(work, "public-actual-tool.txt"), "content": "PUBLIC_COMMAND_TOOL"}, "")
		case 2:
			response(w, "pty-first", "", nil, "PUBLIC_FIRST_RESPONSE\n```go\nfmt.Println(\"PUBLIC_CODE\")\n```")
		default:
			response(w, "pty-next", "", nil, "PUBLIC_NEXT_HUMAN_COMPLETE")
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Stderr = os.Stderr
	work = o.WorkDir
	bin := filepath.Join(home, "private-clipboard-bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "pbcopy"), []byte("#!/bin/sh\n/bin/cat > \"$CLIPBOARD_RECEIPT\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	clipboard := filepath.Join(home, "clipboard-stdin.txt")
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["PATH"] = bin + ":/usr/bin:/bin"
		env["CLIPBOARD_RECEIPT"] = clipboard
		env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = "name-fixture"
	}
	if sandbox {
		original, err := exec.LookPath("rg")
		if err != nil {
			t.Fatal("actual packaged ripgrep required", err)
		}
		data, err := os.ReadFile(original)
		if err != nil {
			t.Fatal(err)
		}
		private := filepath.Join(bin, "rg")
		if err = os.WriteFile(private, data, 0700); err != nil {
			t.Fatal(err)
		}
		for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
			env["CC_HAHA_RIPGREP_PATH"] = private
			env["CLAUDE_CODE_TMPDIR"] = home
		}
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	global := map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{work: map[string]any{"hasTrustDialogAccepted": true, "hasCompletedProjectOnboarding": marketing}}, "future": map[string]any{"preserve": 17}}
	if marketing {
		for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
			delete(env, "ANTHROPIC_API_KEY")
			env["CLAUDE_CODE_FORCE_FULL_LOGO"] = "1"
		}
		global["oauthAccount"] = map[string]any{"organizationUuid": "private-export-org", "organizationName": "PRIVATE_COMPANY"}
		global["passesEligibilityCache"] = map[string]any{"private-export-org": map[string]any{"eligible": true, "timestamp": 9999999999999, "remaining_passes": 2}}
		global["overageCreditGrantCache"] = map[string]any{"private-export-org": map[string]any{"timestamp": 9999999999999, "info": map[string]any{"available": false}}}
		if err := os.WriteFile(filepath.Join(config, ".credentials.json"), raw(map[string]any{"claudeAiOauth": map[string]any{"accessToken": "fake-private-export-token", "refreshToken": nil, "scopes": []string{"user:inference", "user:profile"}, "expiresAt": 9999999999999, "subscriptionType": "max"}}), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"companyAnnouncements":["PRIVATE_COMPANY_ANNOUNCEMENT"]}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if sandbox {
		if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"sandbox":{"enabled":true}}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(config, ".claude.json"), raw(global), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "conversation-workdir.txt"), []byte(work), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	var environment []string
	for key, value := range o.Environment {
		environment = append(environment, key+"="+value)
	}
	environment = append(environment, "TERM=xterm-256color")
	code, err := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, environment)
	if err != nil || code != 0 {
		t.Fatal("actual CLI conversation exit", code, err)
	}
	if _, err = nativeTUIPresentationTerminalProbe(os.Stdin.Fd()); err != nil {
		t.Fatal(err)
	}
	after, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(config, "projects", "*", o.Arguments.SessionID+".jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatal("real conversation transcript missing", err, files)
	}
	transcript, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(transcript), `"customTitle":"auto-pty-topic"`) || !strings.Contains(string(transcript), `"agentName":"auto-pty-topic"`) || !strings.Contains(string(transcript), `"agentColor":"default"`) {
		t.Fatal("public metadata did not persist Source entry/reset shapes")
	}
	if marketing {
		global, err := os.ReadFile(filepath.Join(config, ".claude.json"))
		var persisted map[string]any
		if err != nil || json.Unmarshal(global, &persisted) != nil || persisted["passesUpsellSeenCount"] != float64(2) || persisted["passesLastSeenRemaining"] != float64(2) {
			t.Fatal("actual mounted export marketing counters", err, string(global))
		}
	}
	receipt := map[string]any{"terminalRestored": reflect.DeepEqual(before, after), "ordinary": ordinary.Load(), "names": names.Load(), "exitCode": code}
	encoded, _ := json.Marshal(receipt)
	if err := os.WriteFile(filepath.Join(home, "conversation-receipt.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(home, "conversation-pty-release")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("physical parent inspection not acknowledged")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ordinary.Load() != 3 || names.Load() != 1 {
		t.Fatal("local public consumers opened extra models or lost next turn", receipt)
	}
}
