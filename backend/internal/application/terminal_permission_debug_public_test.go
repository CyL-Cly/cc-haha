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
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
	"github.com/charmbracelet/x/term"
)

const terminalPermissionDebugPTYScript = `import fcntl,json,os,pathlib,select,struct,subprocess,sys,termios,time
binary,home=sys.argv[1:]
master,slave=os.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',40,136,0,0))
before=termios.tcgetattr(slave);captured=bytearray();deadline=time.monotonic()+38
ctrl=[b'\r',b'y',b'n',b'\x1b',b'\x1b[A',b'\x1b[B',b'\x1b[C',b'\x1b[D',b'\t']
def setup():
 os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([binary,'-test.run=^TestNativeTerminalPermissionDebugPublicFixtureProcess$','-test.count=1'],stdin=slave,stdout=slave,stderr=slave,env=os.environ.copy(),preexec_fn=setup,close_fds=True)
def pump():
 if select.select([master],[],[],.04)[0]:
  try:captured.extend(os.read(master,65536))
  except OSError:pass
def wait(needle,label,start=0):
 while needle not in captured[start:]:
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError(label+' '+repr(bytes(captured[-6000:])))
  pump()
def send(value):
 os.write(master,value);time.sleep(.14);pump()
try:
 wait(b'Claude Code','normal public CLI startup')
 send(b'PUBLIC_PERMISSION_DEBUG_HUMAN\r');wait(b'Enter: confirm','actual shell permission')
 tool_path=pathlib.Path(pathlib.Path(home,'permission-debug-tool-path.txt').read_text())
 start=len(captured);send(b'\x04');wait(b'Behavior','unconditional ctrl+d debug',start);wait(b'Reason','actual decision reason',start);wait(b'Suggestions','actual decision suggestions',start)
 for value in ctrl:
  send(value)
  assert not tool_path.exists(),'debug key approved physical tool '+repr(value)
  assert pathlib.Path(home,'permission-debug-model-count.txt').read_text()=='1','debug key resumed actual model '+repr(value)
 start=len(captured);send(b'\x04');wait(b'Enter: confirm','toggle remounts Source Select',start)
 send(b'\r');wait(b'PUBLIC_PERMISSION_DEBUG_TOOL_COMPLETE','actual approved physical tool then Harness')
 assert tool_path.read_text().strip()=='PUBLIC_DEBUG_APPROVED','real tool output absent'
 send(b'PUBLIC_PERMISSION_DEBUG_NEXT_HUMAN\r');wait(b'PUBLIC_PERMISSION_DEBUG_NEXT_COMPLETE','next human remains usable')
 send(b'\x04\x04')
 receipt_file=pathlib.Path(home,'permission-debug-receipt.json')
 while not receipt_file.exists():
  if time.monotonic()>deadline or p.poll() is not None:raise AssertionError('normal exit receipt missing '+repr(bytes(captured[-6000:])))
  pump()
 receipt=json.loads(receipt_file.read_text());assert receipt['terminalRestored'] and receipt['code']==0 and receipt['models']==3,receipt
 assert termios.tcgetattr(slave)==before,'real slave termios not restored'
 pathlib.Path(home,'permission-debug-release').write_text('inspected')
 while p.poll() is None:
  if time.monotonic()>deadline:raise AssertionError('actual CLI remained pending')
  pump()
 assert p.returncode==0,p.returncode
 print(json.dumps({'terminalRestored':True,'actualModelRequests':3,'ignoredDebugKeys':len(ctrl),'tool':os.environ['CC_HAHA_PERMISSION_DEBUG_TOOL'],'debug':os.environ['CC_HAHA_PERMISSION_DEBUG_FLAG']}))
finally:
 pathlib.Path(home,'permission-debug-terminal.txt').write_bytes(captured)
 if p.poll() is None:p.kill();p.wait()
 os.close(master);os.close(slave)
`

func TestActualPublicCLIPermissionDebugUnmountsApprovalThenRunsRealShellAndNextHumanPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("physical Unix PTY; Windows ConPTY requires a host")
	}
	python, e := exec.LookPath("python3")
	if e != nil {
		t.Fatal(e)
	}
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	for _, tool := range []string{"Bash", "PowerShell"} {
		for _, debug := range []string{"false", "true"} {
			t.Run(tool+"/debug-"+debug, func(t *testing.T) {
				executable := ""
				if tool == "PowerShell" {
					path, _ := filepath.Abs("../../../artifacts/go-migration-20261013/powershell-bin-path.txt")
					if data, err := os.ReadFile(path); err == nil {
						executable = filepath.Join(strings.TrimSpace(string(data)), "pwsh")
					}
					if executable == "" {
						executable = physicalPowerShell(t)
					}
					if info, err := os.Stat(executable); err != nil || !info.Mode().IsRegular() {
						t.Fatal("private physical PowerShell unavailable", executable, err)
					}
				}
				home := t.TempDir()
				if e = os.WriteFile(filepath.Join(home, "owned-permission-debug-pty"), []byte("private"), 0600); e != nil {
					t.Fatal(e)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, python, "-c", terminalPermissionDebugPTYScript, binary, home)
				cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "CC_HAHA_PERMISSION_DEBUG_PTY=1", "CC_HAHA_PERMISSION_DEBUG_TOOL=" + tool, "CC_HAHA_PERMISSION_DEBUG_FLAG=" + debug, "CC_HAHA_TEST_PWSH=" + executable}
				capturePublicGoCoverage(t, cmd)
				out, e := cmd.CombinedOutput()
				if e != nil || !strings.Contains(string(out), `"terminalRestored": true`) {
					t.Fatal("public permission debug", e, string(out))
				}
			})
		}
	}
}

func TestNativeTerminalPermissionDebugPublicFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_PERMISSION_DEBUG_PTY") != "1" {
		t.Skip("owned isolated permission debug public CLI helper")
	}
	home := os.Getenv("HOME")
	if _, e := os.Stat(filepath.Join(home, "owned-permission-debug-pty")); e != nil {
		t.Fatal(e)
	}
	tool := os.Getenv("CC_HAHA_PERMISSION_DEBUG_TOOL")
	if tool != "Bash" && tool != "PowerShell" {
		t.Fatal("unowned tool", tool)
	}
	var models atomic.Int32
	var work, file string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		count := models.Add(1)
		if e := os.WriteFile(filepath.Join(home, "permission-debug-model-count.txt"), []byte(fmt.Sprint(count)), 0600); e != nil {
			t.Error(e)
		}
		if r.Method != http.MethodPost || r.Header.Get("x-api-key") != "fake-only-loopback" {
			t.Error("actual ordinary model transport", r.Method, r.Header)
		}
		switch count {
		case 1:
			if !strings.Contains(string(body), "PUBLIC_PERMISSION_DEBUG_HUMAN") {
				t.Error("public human absent", string(body))
			}
			command := "printf 'PUBLIC_DEBUG_APPROVED' > '" + file + "'"
			if tool == "PowerShell" {
				command = "Set-Content -LiteralPath '" + file + "' -Value 'PUBLIC_DEBUG_APPROVED'"
			}
			response(w, "permission-debug-tool", tool, map[string]any{"command": command}, "")
		case 2:
			data, e := os.ReadFile(file)
			if e != nil || strings.TrimSpace(string(data)) != "PUBLIC_DEBUG_APPROVED" {
				t.Error("real shell result before next model", e, string(data))
			}
			if !strings.Contains(string(body), `"type":"tool_result"`) {
				t.Error("actual tool result missing", string(body))
			}
			response(w, "permission-debug-done", "", nil, "PUBLIC_PERMISSION_DEBUG_TOOL_COMPLETE")
		case 3:
			if !strings.Contains(string(body), "PUBLIC_PERMISSION_DEBUG_NEXT_HUMAN") {
				t.Error("next ordinary human absent", string(body))
			}
			response(w, "permission-debug-next", "", nil, "PUBLIC_PERMISSION_DEBUG_NEXT_COMPLETE")
		default:
			t.Error("unexpected real model request", count, string(body))
			response(w, "unexpected", "", nil, "unexpected")
		}
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Stderr = os.Stderr
	o.Interactive = true
	o.ForegroundShellProgress = true
	work = o.WorkDir
	file = filepath.Join(work, "permission-debug-tool-result.txt")
	if e := os.WriteFile(filepath.Join(home, "permission-debug-tool-path.txt"), []byte(file), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.MkdirAll(o.Environment["CLAUDE_CONFIG_DIR"], 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(fmt.Sprintf(`{"permissions":{"ask":[%q]}}`, tool)), 0600); e != nil {
		t.Fatal(e)
	}
	if tool == "PowerShell" {
		executable := physicalPowerShell(t)
		for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
			env["CLAUDE_CODE_POWERSHELL_PATH"] = executable
			env["CLAUDE_CODE_USE_POWERSHELL_TOOL"] = "1"
			env["TMPDIR"] = o.Native.Home
			env["CLAUDE_CODE_TMPDIR"] = o.Native.Home
		}
		o.NativeShells = windowshell.NewNative("windows", o.WorkDir, o.Native.Environment)
	}
	args := []string{"--model", "fixture-model", "--permission-mode", "default", "--tools", tool, "--max-turns", "4", "--system-prompt", "Use the actual tool when requested.", "--no-computer-use"}
	if os.Getenv("CC_HAHA_PERMISSION_DEBUG_FLAG") == "true" {
		args = append(args, "--debug")
	}
	command, _, e := ParseCommand(args, o.Version, o.WorkDir, o.Native.Temp, o.Environment)
	if e != nil {
		t.Fatal(e)
	}
	o.Arguments = command.Arguments
	before, e := term.GetState(os.Stdin.Fd())
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 38*time.Second)
	defer cancel()
	var env []string
	for key, value := range o.Environment {
		env = append(env, key+"="+value)
	}
	env = append(env, "TERM=xterm-256color")
	code, e := RunCommand(ctx, command, o, os.Stdin, os.Stdout, env)
	if e != nil || code != 0 {
		t.Fatal(code, e)
	}
	if _, e = nativeTUIPresentationTerminalProbe(os.Stdin.Fd()); e != nil {
		t.Fatal(e)
	}
	after, e := term.GetState(os.Stdin.Fd())
	if e != nil {
		t.Fatal(e)
	}
	if models.Load() != 3 {
		t.Fatal("public debug lost next request", models.Load())
	}
	receipt, _ := json.Marshal(map[string]any{"code": code, "models": models.Load(), "terminalRestored": reflect.DeepEqual(before, after)})
	if e = os.WriteFile(filepath.Join(home, "permission-debug-receipt.json"), receipt, 0600); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, e = os.Stat(filepath.Join(home, "permission-debug-release")); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("PTY observer failed to release")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
