//go:build !windows

package sandboxruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

func logLinuxRelayFailure(t *testing.T, root string) {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join(root, "listen-error-*"))
	for _, p := range paths {
		raw, _ := os.ReadFile(p)
		t.Log(string(raw))
	}
	t.Log("actual socket root", root)
}

// A Unix sockaddr has a fixed byte limit independent of HOME and TMPDIR.
// Only sockets use this private root; Source and Go retain the caller's long
// isolated home/CWD and compare the same paths/frames/assertions.
func linuxFixtureSocketRoot(t *testing.T) string {
	t.Helper()
	root, e := os.MkdirTemp("/tmp", "gs-linux-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	physical, e := filepath.EvalSymlinks(root)
	if e != nil {
		t.Fatal(e)
	}
	return physical
}

func linuxManagerHelper(t *testing.T, root string) (string, map[string]string) {
	t.Helper()
	socat := filepath.Join(root, "socat")
	source, _ := filepath.Abs("../../testdata/native-unix-tcp-relay/main.go")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", socat, source)
	cmd.Dir = root
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal("generic native relay compilation", e, string(output))
	}
	// Validate the newly compiled executable before entering Source's fixed
	// socat ready window. It exits 2 on missing argv and creates no resources.
	probe := exec.CommandContext(ctx, socat)
	if e := probe.Run(); e == nil || probe.ProcessState.ExitCode() != 2 {
		t.Fatal("native relay executable probe", e)
	}
	return socat, map[string]string{"HOME": root, "TMPDIR": root, "PATH": root + ":/opt/homebrew/bin:/usr/bin:/bin", "CC_HAHA_LINUX_BRIDGE_HELPER": "1"}
}

func TestActualSourceLinuxManagerInitialUpdateGlobRipgrepAndOwnedBridgeLifetime(t *testing.T) {
	root, e := os.MkdirTemp(os.Getenv("TMPDIR"), "lm-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(root)
	cwd := filepath.Join(root, "repo")
	os.MkdirAll(filepath.Join(cwd, "nested/.git/hooks"), 0700)
	os.MkdirAll(filepath.Join(cwd, "hidden/allowed"), 0700)
	os.WriteFile(filepath.Join(cwd, "nested/.git/hooks/actual-hook"), []byte("hook"), 0600)
	rg, e := exec.LookPath("rg")
	if e != nil {
		t.Fatal(e)
	}
	socat, env := linuxManagerHelper(t, root)
	socketRoot := linuxFixtureSocketRoot(t)
	env["TMPDIR"] = socketRoot
	envLog := filepath.Join(root, "rg-environment.log")
	rgShim := filepath.Join(root, "rg-native")
	shim := "#!/bin/sh\nprintf '%s\\n' \"$CC_HAHA_SANDBOX_ENVIRONMENT_PROBE\" >> '" + envLog + "'\nexec '" + rg + "' \"$@\"\n"
	if e := os.WriteFile(rgShim, []byte(shim), 0700); e != nil {
		t.Fatal(e)
	}
	rg = rgShim
	// The Source dependency primitive is executable presence only; the test
	// never executes bwrap and proves no Linux kernel isolation.
	if e := os.WriteFile(filepath.Join(root, "bwrap"), []byte("#!/bin/sh\nexit 99\n"), 0700); e != nil {
		t.Fatal(e)
	}
	vendor, _ := filepath.Abs("../../../node_modules/@anthropic-ai/sandbox-runtime/vendor/seccomp/arm64")
	vendor, _ = filepath.EvalSymlinks(vendor)
	config := Config{Network: NetworkConfig{AllowedDomains: []string{}, DeniedDomains: []string{}}, Filesystem: FilesystemConfig{DenyRead: []string{}, AllowRead: []string{}, AllowWrite: []string{cwd}, DenyWrite: []string{}}, Ripgrep: testObject(t, fmt.Sprintf(`{"command":%q}`, rg))}
	// Two borrowed TCP ports avoid starting a different proxy implementation in
	// the Source process. The actual Source and Go bridge processes still start.
	peer, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer peer.Close()
	port := float64(peer.Addr().(*net.TCPAddr).Port)
	config.Network.HTTPProxyPort = &port
	config.Network.SOCKSProxyPort = &port
	second := cloneConfig(config)
	second.Filesystem.DenyRead = []string{filepath.Join(cwd, "hid*/**")}
	second.Filesystem.AllowRead = []string{filepath.Join(cwd, "hidden/allowed")}
	second.Filesystem.AllowWrite = append(second.Filesystem.AllowWrite, filepath.Join(cwd, "skip-*"))
	second.Ripgrep = testObject(t, fmt.Sprintf(`{"command":%q,"args":["--glob","!nested/**"]}`, rg))
	weak, allow := true, true
	second.EnableWeakerNestedSandbox = &weak
	second.Network.AllowAllUnixSockets = &allow
	configs := []Config{config, second, config}
	input, _ := json.Marshal(map[string]any{"cwd": cwd, "configs": configs, "command": "printf '%s' 'manager actual'"})
	fixture, _ := filepath.Abs("testdata/linux-manager.fixture.ts")
	cmd := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = root
	cmd.Stdin = bytes.NewReader(input)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(output))
	}
	var source []struct{ Command, HTTP, Socks string }
	if e = json.Unmarshal(output, &source); e != nil {
		t.Fatal(e, string(output))
	}
	manager, e := NewManager(context.Background(), ConvertedConfig{Config: config}, ManagerOptions{Platform: "linux", Home: root, CWD: cwd, SessionSuffix: "source", Environment: env, Realpath: filepath.EvalSymlinks, FindExecutable: exec.LookPath, LinuxTemporaryDirectory: socketRoot, Socat: socat, SeccompBPFPath: filepath.Join(vendor, "unix-block.bpf"), ApplySeccompPath: filepath.Join(vendor, "apply-seccomp")})
	if e != nil {
		logLinuxRelayFailure(t, socketRoot)
		t.Fatal(e)
	}
	defer manager.Close(context.Background())
	for i, c := range configs {
		manager.Update(ConvertedConfig{Config: c})
		currentEnv := cloneEnvironment(env)
		currentEnv["CC_HAHA_SANDBOX_ENVIRONMENT_PROBE"] = fmt.Sprint(i)
		manager.UpdateEnvironment(currentEnv)
		currentEnv["CC_HAHA_SANDBOX_ENVIRONMENT_PROBE"] = "caller-mutation"
		got, e := manager.WrapContext(context.Background(), "printf '%s' 'manager actual'", "/bin/bash")
		if e != nil {
			t.Fatal(e)
		}
		normalize := func(value, http, socks string) string {
			value = strings.ReplaceAll(value, http, "<http>")
			value = strings.ReplaceAll(value, socks, "<socks>")
			seen := map[string]string{}
			return regexp.MustCompile(regexp.QuoteMeta(socketRoot)+`/claude-empty-[a-zA-Z0-9]+`).ReplaceAllStringFunc(value, func(path string) string {
				info, e := os.Stat(path)
				if e != nil || !info.IsDir() {
					t.Fatal("empty mount source not physical", path, e)
				}
				if seen[path] == "" {
					seen[path] = fmt.Sprintf("<empty%d>", len(seen))
				}
				return seen[path]
			})
		}
		if normalize(got, manager.linuxBridge.HTTPSocketPath, manager.linuxBridge.SOCKSSocketPath) != normalize(source[i].Command, source[i].HTTP, source[i].Socks) {
			t.Fatalf("Source actual manager%d:\n%s\nGo:\n%s", i, source[i].Command, got)
		}
	}
	environmentBytes, e := os.ReadFile(envLog)
	if e != nil || string(environmentBytes) != "0\n1\n2\n0\n1\n2\n" {
		t.Fatal("Source then Go actual discovery environment", e, string(environmentBytes))
	}
	pids := []int{}
	for _, p := range manager.linuxBridge.processes {
		pids = append(pids, p.Process.Pid)
	}
	if e = manager.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	for _, pid := range pids {
		if syscall.Kill(pid, 0) == nil {
			t.Fatal("manager left actual bridge alive", pid)
		}
	}
	if _, e = manager.Wrap("after close", "/bin/bash"); e == nil {
		t.Fatal("closed manager admitted command")
	}
}

func TestLinuxManagerCloseCancelsAdmittedPhysicalDiscoveryAndSharesJoin(t *testing.T) {
	root, e := os.MkdirTemp(os.Getenv("TMPDIR"), "mc-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(root)
	cwd := filepath.Join(root, "repo")
	os.Mkdir(cwd, 0700)
	socat, env := linuxManagerHelper(t, root)
	socketRoot := linuxFixtureSocketRoot(t)
	env["TMPDIR"] = socketRoot
	rg := filepath.Join(root, "rg")
	ready := filepath.Join(root, "rg-ready")
	script := "#!/bin/sh\nprintf '%s' \"$$\" > '" + ready + "'\nexec /bin/sleep 30\n"
	os.WriteFile(rg, []byte(script), 0700)
	config := Config{Network: NetworkConfig{AllowedDomains: []string{}, DeniedDomains: []string{}}, Filesystem: FilesystemConfig{AllowWrite: []string{cwd}}, Ripgrep: testObject(t, fmt.Sprintf(`{"command":%q}`, rg))}
	manager, e := NewManager(context.Background(), ConvertedConfig{Config: config}, ManagerOptions{Platform: "linux", Home: root, CWD: cwd, SessionSuffix: "physical", Environment: env, Realpath: filepath.EvalSymlinks, FindExecutable: exec.LookPath, LinuxTemporaryDirectory: socketRoot, Socat: socat})
	if e != nil {
		logLinuxRelayFailure(t, socketRoot)
		t.Fatal(e)
	}
	defer manager.Close(context.Background())
	result := make(chan error, 1)
	go func() { _, e := manager.WrapContext(context.Background(), "printf physical", "/bin/bash"); result <- e }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, e = os.Stat(ready); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actual rg subprocess never started")
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e = manager.Close(ctx); e != nil {
		t.Fatal("Close blocked behind physical Wrap lock", e)
	}
	select {
	case <-result:
	case <-ctx.Done():
		t.Fatal("admitted Wrap escaped Close")
	}
	if e = manager.Close(ctx); e != nil {
		t.Fatal("second Close did not share join", e)
	}
}

func TestLinuxManagerParentCancellationReleasesResourcesWithoutPublishedConsumer(t *testing.T) {
	root, e := os.MkdirTemp(os.Getenv("TMPDIR"), "lc-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(root)
	cwd := filepath.Join(root, "repo")
	os.Mkdir(cwd, 0700)
	socat, env := linuxManagerHelper(t, root)
	socketRoot := linuxFixtureSocketRoot(t)
	env["TMPDIR"] = socketRoot
	config := Config{Network: NetworkConfig{AllowedDomains: []string{}, DeniedDomains: []string{}}, Filesystem: FilesystemConfig{AllowWrite: []string{cwd}}, Ripgrep: testObject(t, `{"command":"rg"}`)}
	lifetime, cancel := context.WithCancel(context.Background())
	manager, e := NewManager(lifetime, ConvertedConfig{Config: config}, ManagerOptions{Platform: "linux", Home: root, CWD: cwd, SessionSuffix: "physical", Environment: env, Realpath: filepath.EvalSymlinks, FindExecutable: exec.LookPath, LinuxTemporaryDirectory: socketRoot, Socat: socat})
	if e != nil {
		cancel()
		logLinuxRelayFailure(t, socketRoot)
		t.Fatal(e)
	}
	defer manager.Close(context.Background())
	pids := []int{}
	for _, p := range manager.linuxBridge.processes {
		pids = append(pids, p.Process.Pid)
	}
	http, socks := manager.ProxyPorts()
	cancel()
	select {
	case <-manager.closeDone:
	case <-time.After(7 * time.Second):
		t.Fatal("unpublished initializer resources escaped canceled lifetime")
	}
	for _, pid := range pids {
		if syscall.Kill(pid, 0) == nil {
			t.Fatal("canceled lifetime left native child", pid)
		}
	}
	for _, port := range []float64{http, socks} {
		c, e := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", int(port)), time.Second)
		if e == nil {
			c.Close()
			t.Fatal("canceled lifetime left proxy", port)
		}
	}
}
