package application

import (
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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/charmbracelet/x/term"
	"github.com/coder/websocket"
)

const remotePublicPTY = `import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
binary,home=sys.argv[1:];master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',40,138,0,0));before=termios.tcgetattr(slave);output=bytearray();deadline=time.monotonic()+38
def setup():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeRemotePublicFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:output.extend(os.read(master,65536))
  except OSError:pass
def wait(text,start=0):
 while text not in output[start:]:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(str(text)+' '+repr(bytes(output[-6500:])))
  pump()
def send(text):os.write(master,text);time.sleep(.12);pump()
try:
 wait(b'INITIAL_CCR_READY');assert b'Created remote session:' not in output,'true gate exited through false branch'
 send(b'REMOTE_PUBLIC_HUMAN_1\r');wait(b'Enter: confirm');send(b'y');wait(b'REMOTE_PUBLIC_READY_1')
 send(b'REMOTE_PUBLIC_HUMAN_2\r');wait(b'REMOTE_PUBLIC_READY_2')
 start=len(output);send(b'REMOTE_PUBLIC_HUMAN_3\r');wait(b'Enter: confirm',start);send(b'n');wait(b'REMOTE_PUBLIC_READY_3')
 send(b'REMOTE_PUBLIC_HUMAN_4\r');wait(b'REMOTE_PUBLIC_HELD')
 send(b'REMOTE_PUBLIC_HUMAN_5\r');wait(b'REMOTE_PUBLIC_NEXT_HELD');send(b'\x03');wait(b'REMOTE_PUBLIC_INTERRUPTED')
 start=len(output);send(b'REMOTE_PUBLIC_HUMAN_6\r');wait(b'Enter: confirm',start);send(b'\x03');wait(b'REMOTE_PUBLIC_READY_6')
 send(b'!REMOTE_PUBLIC_HUMAN_7\r');wait(b'REMOTE_PUBLIC_READY_7');start=len(output);send(b'\x04');wait(b'Press Ctrl-D again to exit',start);send(b'\x04')
 receipt=pathlib.Path(home,'remote-public-receipt.json')
 while not receipt.exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('exit receipt '+repr(bytes(output[-6500:])))
  pump()
 result=json.loads(receipt.read_text());assert result['terminalRestored'] and result['events']==7 and result['modelRequests']==0 and result['approvals']==1 and result['denials']==1 and result['interrupts']==1 and result['aborts']==1,(result,repr(bytes(output[-9000:])))
 assert termios.tcgetattr(slave)==before,'actual termios changed'
 pathlib.Path(home,'remote-public-release').write_text('observed')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('actual client did not join')
  pump()
 assert p.returncode==0,p.returncode
 print(json.dumps(result))
finally:
 pathlib.Path(home,'remote-public-terminal.txt').write_bytes(output)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
`

func TestActualRemoteTrueGatePublicCLIEventsApprovalCancelInterruptNextHumanAndTermios(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("physical Unix PTY, not Windows ConPTY")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, empty := range []bool{false, true} {
		t.Run(fmt.Sprint("empty-initial=", empty), func(t *testing.T) {
			home := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, python, "-c", remotePublicPTY, binary, home)
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "CC_HAHA_REMOTE_PUBLIC_FIXTURE=1", "CC_HAHA_REMOTE_PUBLIC_EMPTY=" + fmt.Sprint(empty)}
			capturePublicGoCoverage(t, cmd)
			output, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(output), `"terminalRestored": true`) {
				t.Fatal(err, string(output))
			}
		})
	}
}

func TestNativeRemotePublicFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_REMOTE_PUBLIC_FIXTURE") != "1" {
		t.Skip("private actual remote PTY child")
	}
	home := os.Getenv("HOME")
	var mu sync.Mutex
	var conn *websocket.Conn
	var models, events, approvals, denials, interrupts, titlePatches, aborts atomic.Int32
	emptyInitial := os.Getenv("CC_HAHA_REMOTE_PUBLIC_EMPTY") == "true"
	closed := make(chan struct{})
	var closeOnce sync.Once
	tool := filepath.Join(home, "remote-approved-tool.txt")
	toolCommand := "printf 'CCR_APPROVED_PHYSICAL_TOOL\\n' > '" + tool + "'"
	send := func(frame protocol.JSONObject) {
		mu.Lock()
		defer mu.Unlock()
		if conn == nil {
			t.Error("actual CCR socket absent")
			return
		}
		if err := conn.Write(context.Background(), websocket.MessageText, raw(frame)); err != nil {
			t.Error(err)
		}
	}
	answer := func(text string) {
		send(obj(raw(map[string]any{"type": "assistant", "uuid": newID(), "message": map[string]any{"id": newID(), "type": "message", "role": "assistant", "model": "remote-only-model", "content": []any{map[string]string{"type": "text", "text": text}}}})))
		send(obj(raw(map[string]any{"type": "result", "subtype": "success", "uuid": newID(), "result": text})))
	}
	ask := func(id string) {
		send(obj(raw(map[string]any{"type": "control_request", "request_id": id, "request": map[string]any{"subtype": "can_use_tool", "tool_name": "Bash", "tool_use_id": id + "-tool", "input": map[string]string{"command": toolCommand}, "description": "Actual remote Bash requires approval"}})))
	}
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/messages" {
			models.Add(1)
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "initial remote task") {
				t.Error("ordinary human incorrectly entered local model", string(body))
			}
			response(w, "actual-remote-title", "", nil, `{"title":"Actual CCR task","branch":"claude/actual-ccr"}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer owned-ccr-oauth" || r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Error("saved remote credentials/header", r.Header)
		}
		switch r.URL.Path {
		case "/v1/environment_providers":
			_, _ = io.WriteString(w, `{"environments":[{"environment_id":"owned-cloud","kind":"anthropic_cloud"}]}`)
		case "/v1/sessions":
			posted, _ := io.ReadAll(r.Body)
			body := obj(posted)
			want := "initial remote task"
			if emptyInitial {
				want = "Background task"
			}
			if body.String("title") != want {
				t.Error("Source frozen ant title fallback", string(posted), want)
			}
			_, _ = io.WriteString(w, `{"id":"session_owned_public","title":"Actual CCR task"}`)
		case "/v1/sessions/ws/session_owned_public/subscribe":
			if r.URL.Query().Get("organization_uuid") != "owned-ccr-org" {
				t.Error(r.URL)
			}
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer c.CloseNow()
			defer closeOnce.Do(func() { close(closed) })
			mu.Lock()
			conn = c
			mu.Unlock()
			send(obj(raw(map[string]any{"type": "system", "subtype": "init", "model": "remote-only-model", "slash_commands": []string{}})))
			answer("INITIAL_CCR_READY")
			for {
				_, data, err := c.Read(r.Context())
				if err != nil {
					return
				}
				frame := obj(data)
				if frame.String("type") == "control_request" {
					if obj(frame.Get("request")).String("subtype") != "interrupt" {
						t.Error(frame)
					}
					interrupts.Add(1)
					answer("REMOTE_PUBLIC_INTERRUPTED")
					continue
				}
				result := obj(frame.Get("response"))
				permission := obj(result.Get("response"))
				switch result.String("request_id") {
				case "remote-approve":
					if permission.String("behavior") != "allow" || obj(permission.Get("updatedInput")).String("command") != toolCommand {
						t.Error(frame)
					}
					approvals.Add(1)
					if output, err := exec.CommandContext(r.Context(), "/bin/sh", "-c", toolCommand).CombinedOutput(); err != nil {
						t.Error(err, string(output))
					}
					answer("REMOTE_PUBLIC_READY_1")
				case "remote-abort":
					if permission.String("behavior") != "deny" || permission.String("message") != "User aborted" {
						t.Error("actual Source permission onAbort wire", frame)
					}
					aborts.Add(1)
					answer("REMOTE_PUBLIC_READY_6")
				case "remote-deny":
					if permission.String("behavior") != "deny" || permission.String("message") != "User denied permission" {
						t.Error(frame)
					}
					denials.Add(1)
					answer("REMOTE_PUBLIC_READY_3")
				default:
					t.Error("cancelled permission emitted response or unknown control", frame)
				}
			}
		case "/v1/sessions/session_owned_public":
			data, _ := io.ReadAll(r.Body)
			if !emptyInitial || r.Method != "PATCH" || obj(data).String("title") != "REMOTE_PUBLIC_HUMAN_1" || r.Header.Get("anthropic-beta") != "ccr-byoc-2025-07-29" || r.Header.Get("x-organization-uuid") != "owned-ccr-org" {
				t.Error("actual first input title update", r.Method, r.Header, string(data))
			}
			titlePatches.Add(1)
			_, _ = io.WriteString(w, `{}`)
		case "/v1/sessions/session_owned_public/events":
			body, _ := io.ReadAll(r.Body)
			count := events.Add(1)
			if r.Method != "POST" || r.Header.Get("anthropic-beta") != "ccr-byoc-2025-07-29" || r.Header.Get("x-organization-uuid") != "owned-ccr-org" || !strings.Contains(string(body), fmt.Sprintf("REMOTE_PUBLIC_HUMAN_%d", count)) {
				t.Error("real CCR next input", r.Method, r.Header, string(body))
			}
			_, _ = io.WriteString(w, `{}`)
			switch count {
			case 1:
				ask("remote-approve")
			case 2:
				ask("remote-cancelled")
				send(obj(raw(map[string]any{"type": "control_cancel_request", "request_id": "remote-cancelled"})))
				answer("REMOTE_PUBLIC_READY_2")
			case 3:
				ask("remote-deny")
			case 4:
				send(obj(raw(map[string]any{"type": "assistant", "uuid": newID(), "message": map[string]any{"id": newID(), "role": "assistant", "content": []any{map[string]string{"type": "text", "text": "REMOTE_PUBLIC_HELD"}}}})))
			case 5:
				send(obj(raw(map[string]any{"type": "assistant", "uuid": newID(), "message": map[string]any{"id": newID(), "role": "assistant", "content": []any{map[string]string{"type": "text", "text": "REMOTE_PUBLIC_NEXT_HELD"}}}})))
			case 6:
				ask("remote-abort")
			case 7:
				answer("REMOTE_PUBLIC_READY_7")
			default:
				t.Error(count)
			}
		default:
			t.Error("unowned CCR request", r.Method, r.URL)
			w.WriteHeader(500)
		}
	})
	model := httptest.NewServer(api)
	defer model.Close()
	proxy, ca := claudeAIAPIPrivateTLS(t, api)
	o := remoteCLIOptions(t, model.URL, proxy, ca)
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["USER_TYPE"] = "ant"
		env["CLAUDE_INTERNAL_FC_OVERRIDES"] = `{"tengu_remote_backend":true}`
		env["ANTHROPIC_DEFAULT_SONNET_MODEL"] = "fixture-model"
	}
	config := o.Native.Environment["CLAUDE_CONFIG_DIR"]
	global := obj([]byte(`{"oauthAccount":{"organizationUuid":"owned-ccr-org"}}`))
	global.Set("lastReleaseNotesSeen", raw(o.Version))
	if err := os.MkdirAll(filepath.Join(config, "cache"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, ".claude.json"), raw(global), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "cache", "changelog.md"), []byte("## 0.0.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	o.Stderr = os.Stderr
	o.ObserveModelFailure = func(err error) { fmt.Fprintln(os.Stderr, "ACTUAL_REMOTE_MODEL_FAILURE", err) }
	args := []string{"--remote"}
	if !emptyInitial {
		args = append(args, "initial remote task")
	}
	args = append(args, "--model", "sonnet", "--tools", "", "--thinking", "disabled")
	command, _, err := ParseCommand(args, o.Version, o.WorkDir, t.TempDir(), o.Environment)
	if err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	env := []string{"TERM=xterm-256color"}
	for key, value := range o.Environment {
		env = append(env, key+"="+value)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 36*time.Second)
	defer cancel()
	code, err := RunCommand(ctx, command, o, os.Stdin, os.Stdout, env)
	if err != nil || code != 0 {
		t.Fatal(code, err)
	}
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("CCR socket not joined")
	}
	rawAfter, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	pending, err := nativeTUIPresentationTerminalProbe(os.Stdin.Fd())
	if err != nil || pending != 0 {
		t.Fatal("terminal observer found unread input", pending, err)
	}
	after, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	if titlePatches.Load() != 0 && !emptyInitial || emptyInitial && titlePatches.Load() != 1 {
		t.Fatal("first successful message title must update exactly once", titlePatches.Load())
	}
	data, err := os.ReadFile(tool)
	if err != nil || strings.TrimSpace(string(data)) != "CCR_APPROVED_PHYSICAL_TOOL" {
		t.Fatal("real remote tool absent", err, string(data))
	}
	receipt := map[string]any{"code": code, "terminalRestored": reflect.DeepEqual(before, after), "terminalRawAfter": fmt.Sprintf("%#v", rawAfter), "terminalBefore": fmt.Sprintf("%#v", before), "terminalAfter": fmt.Sprintf("%#v", after), "events": events.Load(), "modelRequests": models.Load(), "approvals": approvals.Load(), "denials": denials.Load(), "interrupts": interrupts.Load(), "titlePatches": titlePatches.Load(), "aborts": aborts.Load()}
	if err := os.WriteFile(filepath.Join(home, "remote-public-receipt.json"), raw(receipt), 0600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(home, "remote-public-release")); err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, _ = json.Marshal(receipt)
}
