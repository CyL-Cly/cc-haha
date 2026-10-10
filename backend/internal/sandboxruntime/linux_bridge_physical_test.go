//go:build !windows

package sandboxruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The helper is a generic native Unix/TCP byte relay at the replaceable socat
// executable boundary. It implements no SRT/CLI/Agent policy and no Harness.
func TestNativeLinuxBridgeProcessHelper(t *testing.T) {
	if os.Getenv("CC_HAHA_LINUX_BRIDGE_HELPER") != "1" {
		return
	}
	args := os.Args
	split := -1
	for i, v := range args {
		if v == "--" {
			split = i
			break
		}
	}
	if split < 0 || len(args) != split+3 {
		os.Exit(2)
	}
	unix := strings.TrimSuffix(strings.TrimPrefix(args[split+1], "UNIX-LISTEN:"), ",fork,reuseaddr")
	tcp := strings.Split(strings.TrimPrefix(args[split+2], "TCP:"), ",")[0]
	listener, e := net.Listen("unix", unix)
	if e != nil {
		os.Exit(3)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	connections := map[net.Conn]struct{}{}
	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGTERM)
	go func() {
		<-done
		listener.Close()
		mu.Lock()
		for c := range connections {
			c.Close()
		}
		mu.Unlock()
	}()
	for {
		c, e := listener.Accept()
		if e != nil {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer c.Close()
			upstream, e := net.Dial("tcp", tcp)
			if e != nil {
				return
			}
			defer upstream.Close()
			mu.Lock()
			connections[c] = struct{}{}
			connections[upstream] = struct{}{}
			mu.Unlock()
			copied := make(chan struct{})
			go func() { io.Copy(upstream, c); upstream.Close(); close(copied) }()
			io.Copy(c, upstream)
			c.Close()
			<-copied
			mu.Lock()
			delete(connections, c)
			delete(connections, upstream)
			mu.Unlock()
		}()
	}
	wg.Wait()
	os.Exit(0)
}
func TestLinuxBridgeNativeProcessesUnixTCPBinaryBytesAndCloseJoin(t *testing.T) {
	root, e := os.MkdirTemp(os.Getenv("TMPDIR"), "br-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(root)
	shim, _ := linuxManagerHelper(t, root)
	socketRoot := linuxFixtureSocketRoot(t)
	peer, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer peer.Close()
	all := make(chan struct{})
	go func() {
		defer close(all)
		for {
			c, e := peer.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	port := float64(peer.Addr().(*net.TCPAddr).Port)
	env := []string{"HOME=" + root, "PATH=/usr/bin:/bin", "TMPDIR=" + socketRoot, "CC_HAHA_LINUX_BRIDGE_HELPER=1"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b, e := NewLinuxBridge(ctx, port, port, LinuxBridgeOptions{TemporaryDirectory: socketRoot, Socat: shim, Environment: env, Random: bytes.NewReader(bytes.Repeat([]byte{2}, 8))})
	if e != nil {
		t.Fatal(e)
	}
	bytesSent := bytes.Repeat([]byte{0, 255, 128, 'z'}, 1024)
	for _, path := range []string{b.HTTPSocketPath, b.SOCKSSocketPath} {
		c, e := net.Dial("unix", path)
		if e != nil {
			t.Fatal(e)
		}
		c.SetDeadline(time.Now().Add(2 * time.Second))
		c.Write(bytesSent)
		received := make([]byte, len(bytesSent))
		_, e = io.ReadFull(c, received)
		c.Close()
		if e != nil || !bytes.Equal(received, bytesSent) {
			t.Fatal("actual native Unix/TCP bridge byte integrity", e)
		}
	}
	pids := []int{}
	for _, process := range b.processes {
		pids = append(pids, process.Process.Pid)
	}
	if e = b.Close(ctx); e != nil {
		t.Fatal(e)
	}
	if e = b.Close(ctx); e != nil {
		t.Fatal("repeated Close did not share join", e)
	}
	for _, pid := range pids {
		if e = syscall.Kill(pid, 0); e == nil {
			t.Fatal("owned bridge native process remained alive", pid)
		}
	}
	for _, path := range []string{b.HTTPSocketPath, b.SOCKSSocketPath} {
		if _, e = os.Stat(path); !os.IsNotExist(e) {
			t.Fatal("owned bridge socket not removed", path, e)
		}
	}
	peer.Close()
	<-all
	// Execute the actual installed producer with the same generic native
	// process seam. This proves SDK argv/ready/Unix/TCP bytes; it does not claim
	// Linux kernel namespaces or a genuine installed socat executable on macOS.
	ready, release := filepath.Join(root, "source-ready.json"), filepath.Join(root, "source-release")
	input, _ := json.Marshal(map[string]any{"port": port, "ready": ready, "release": release})
	fixture, _ := filepath.Abs("testdata/linux-bridge.fixture.ts")
	source := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	source.Env = append(cloneSlice(env), "PATH="+root+":/usr/bin:/bin", "BUN_OPTIONS=--no-env-file")
	source.Dir = root
	source.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	source.Stdout = &stdout
	source.Stderr = &stderr
	// A new loopback peer is needed after the first closed native resource.
	peer2, e := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(int(port)))
	if e != nil {
		t.Fatal(e)
	}
	defer peer2.Close()
	go func() {
		for {
			c, e := peer2.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	if e = source.Start(); e != nil {
		t.Fatal(e)
	}
	var original struct {
		HTTP, SOCKS string
		PIDs        []int
		Args        [][]string
	}
	for {
		raw, e := os.ReadFile(ready)
		if e == nil && json.Unmarshal(raw, &original) == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("actual Source bridge ready", ctx.Err(), stderr.String())
		case <-time.After(time.Millisecond):
		}
	}
	for i, path := range []string{original.HTTP, original.SOCKS} {
		expected := LinuxBridgeArguments(path, port)
		if strings.Join(original.Args[i], "|") != strings.Join(expected, "|") {
			t.Fatal("original socat executable argv", original.Args[i], expected)
		}
		c, e := net.Dial("unix", path)
		if e != nil {
			t.Fatal(e)
		}
		c.SetDeadline(time.Now().Add(2 * time.Second))
		c.Write(bytesSent)
		received := make([]byte, len(bytesSent))
		_, e = io.ReadFull(c, received)
		c.Close()
		if e != nil || !bytes.Equal(received, bytesSent) {
			t.Fatal("actual Source process Unix/TCP bytes", e)
		}
	}
	os.WriteFile(release, nil, 0600)
	if e = source.Wait(); e != nil {
		t.Fatal(e, stdout.String(), stderr.String())
	}
	for _, pid := range original.PIDs {
		if e = syscall.Kill(pid, 0); e == nil {
			t.Fatal("Source administrative process join", pid)
		}
	}
	if stdout.String() != "{\"joined\":true}\n" {
		t.Fatal(fmt.Sprint(stdout.String()))
	}
}
