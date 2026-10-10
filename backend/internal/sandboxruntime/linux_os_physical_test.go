//go:build linux

package sandboxruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
)

// This target-OS check executes bwrap and the original embedded ELF helper.
// Source/transport-seam tests on macOS never count as this evidence.
func TestActualLinuxKernelBwrapSourceAndGoFilesystemNetworkSeccomp(t *testing.T) {
	if os.Getenv("CC_HAHA_SANDBOX_LINUX_OS") != "1" {
		t.Skip("explicit isolated Linux kernel validation lane required")
	}
	for _, name := range []string{"bwrap", "socat", "rg", "curl"} {
		if _, e := exec.LookPath(name); e != nil {
			t.Fatal("actual Linux dependency unavailable", name, e)
		}
	}
	if os.Getenv("CC_HAHA_SOURCE_BUN") == "" {
		t.Fatal("pinned actual Source Bun required")
	}
	root, e := os.MkdirTemp(os.Getenv("TMPDIR"), "ko-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(root)
	cwd := filepath.Join(root, "repo")
	temp := linuxFixtureSocketRoot(t)
	os.MkdirAll(cwd, 0700)
	os.MkdirAll(temp, 0700)
	bpf, apply, e := InstallSeccompAssets(filepath.Join(root, "assets"), runtime.GOARCH)
	if e != nil || bpf == "" {
		t.Fatal("actual native helper unavailable", e)
	}
	rg, _ := exec.LookPath("rg")
	socat, _ := exec.LookPath("socat")
	environment := map[string]string{"HOME": root, "TMPDIR": temp, "PATH": os.Getenv("PATH")}
	config := Config{Network: NetworkConfig{AllowedDomains: []string{}, DeniedDomains: []string{}}, Filesystem: FilesystemConfig{AllowWrite: []string{cwd}, DenyWrite: []string{filepath.Join(root, "assets")}, DenyRead: []string{}, AllowRead: []string{}}, Ripgrep: testObject(t, fmt.Sprintf(`{"command":%q}`, rg))}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	manager, e := NewManager(ctx, ConvertedConfig{Config: config}, ManagerOptions{Platform: "linux", Home: root, CWD: cwd, SessionSuffix: "actual-os", Environment: environment, Realpath: filepath.EvalSymlinks, FindExecutable: exec.LookPath, LinuxTemporaryDirectory: temp, Socat: socat, SeccompBPFPath: bpf, ApplySeccompPath: apply})
	if e != nil {
		t.Fatal(e)
	}
	defer manager.Close(context.Background())
	var reached atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		fmt.Fprint(w, "must not reach host loopback")
	}))
	defer target.Close()
	script := "printf 'actual Linux owned bytes' > allowed.txt; cat allowed.txt; if (printf forbidden > " + teamspawn.ShellQuote(filepath.Join(root, "outside")) + ") 2>/dev/null; then exit 9; else printf ':blocked'; fi; if curl --max-time 2 --noproxy '*' --silent " + teamspawn.ShellQuote(target.URL) + " >/dev/null 2>&1; then exit 9; else printf ':network-blocked'; fi"
	goCommand, e := manager.WrapContext(ctx, script, "/bin/bash")
	if e != nil {
		t.Fatal(e)
	}
	http, socks := manager.ProxyPorts()
	row := map[string]any{"command": script, "needsNetworkRestriction": true, "read": ReadRestriction{DenyOnly: []string{}, AllowWithinDeny: []string{}}, "write": WriteRestriction{AllowOnly: append(DefaultWritePaths(root), cwd), DenyWithinAllow: config.Filesystem.DenyWrite}, "httpSocketPath": manager.linuxBridge.HTTPSocketPath, "socksSocketPath": manager.linuxBridge.SOCKSSocketPath, "httpProxyPort": http, "socksProxyPort": socks, "binShell": "/bin/bash", "seccompConfig": map[string]any{"bpfPath": bpf, "applyPath": apply}}
	input, _ := json.Marshal(map[string]any{"cwd": cwd, "rg": rg, "rows": []any{row}})
	fixture, _ := filepath.Abs("testdata/linux-wrap.fixture.ts")
	source := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	source.Dir = root
	source.Stdin = bytes.NewReader(input)
	source.Env = []string{"HOME=" + root, "TMPDIR=" + temp, "PATH=" + environment["PATH"], "BUN_OPTIONS=--no-env-file"}
	raw, e := source.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(raw))
	}
	var rows []struct{ Result, Error string }
	if e = json.Unmarshal(raw, &rows); e != nil || len(rows) != 1 || rows[0].Error != "" {
		t.Fatal("actual Source wrapper", e, string(raw))
	}
	for _, command := range []string{rows[0].Result, goCommand} {
		cmd := exec.CommandContext(ctx, "/bin/bash", "-c", command)
		cmd.Dir = cwd
		cmd.Env = source.Env
		output, e := cmd.CombinedOutput()
		if e != nil || string(output) != "actual Linux owned bytes:blocked:network-blocked" {
			t.Fatal("actual kernel command", e, string(output))
		}
		if reached.Load() != 0 {
			t.Fatal("actual namespace escaped to host loopback")
		}
		if _, e = os.Stat(filepath.Join(root, "outside")); !os.IsNotExist(e) {
			t.Fatal("actual Linux write escaped", e)
		}
		bytes, e := os.ReadFile(filepath.Join(cwd, "allowed.txt"))
		if e != nil || string(bytes) != "actual Linux owned bytes" {
			t.Fatal("actual Linux file bytes", e)
		}
		if !strings.Contains(command, "--unshare-net") || !strings.Contains(command, apply) {
			t.Fatal("actual namespace/seccomp consumer absent")
		}
	}
}
