package chromenative

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

func TestChromeNativeHostActualSourceFramingValidationAndSocketBroadcast(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Source Unix socket comparison; Windows crosscompile is separate evidence")
	}
	root, err := os.MkdirTemp("/tmp", "cc-ch-source-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0700); err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-chrome-host.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SOURCE_CHROME_HOST=1", "CC_HAHA_SOURCE_CHROME_SOCKET_DIR=" + root, "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	path := filepath.Join(root, strconv.Itoa(cmd.Process.Pid)+".sock")
	deadline := time.Now().Add(4 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatal("actual Source host not listening", stderr.String())
		}
		time.Sleep(time.Millisecond)
	}
	goInput, goOutput, _, _, _ := startHost(t)
	read := func(reader io.Reader) []byte {
		data, err := readFrame(reader)
		if err != nil {
			t.Fatal("actual Source framing", err)
		}
		return data
	}
	for index, request := range []string{`{"type":"get_status"}`, `{bad-json`, `{"type":null}`, `{"type":"unknown-fixture"}`} {
		if err := writeFrame(input, []byte(request)); err != nil {
			t.Fatal(err)
		}
		if err := writeFrame(goInput, []byte(request)); err != nil {
			t.Fatal(err)
		}
		actualSource := read(output)
		actualGo, err := goOutput.waitMessage(t, index).MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(actualGo, actualSource) {
			t.Fatal("original host message differs", request, string(actualGo), string(actualSource))
		}
	}
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if actual := read(output); string(actual) != `{"type":"mcp_connected"}` {
		t.Fatal(string(actual))
	}
	if err := writeFrame(client, []byte(`{"method":"source-owned-method","params":{"id":17},"discard":true}`)); err != nil {
		t.Fatal(err)
	}
	if actual := read(output); string(actual) != `{"type":"tool_request","method":"source-owned-method","params":{"id":17}}` {
		t.Fatal(string(actual))
	}
	if err := writeFrame(input, []byte(`{"type":"notification","method":"source-owned-notification","params":[17]}`)); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	if actual := read(client); string(actual) != `{"method":"source-owned-notification","params":[17]}` {
		t.Fatal("original broadcast contract", string(actual))
	}
	_ = client.Close()
	if actual := read(output); string(actual) != `{"type":"mcp_disconnected"}` {
		t.Fatal(string(actual))
	}
	_ = input.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal("actual Source close", err, stderr.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("actual Source socket cleanup", err)
	}
}
