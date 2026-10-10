package sandboxruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestActualSourceLinuxWrapperNetworkPIDSeccompShellAndMissingBridgeMatrix(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "repo")
	temp := filepath.Join(home, "tmp")
	os.MkdirAll(cwd, 0700)
	os.MkdirAll(temp, 0700)
	httpSocket, socksSocket := filepath.Join(home, "http.sock"), filepath.Join(home, "socks.sock")
	os.WriteFile(httpSocket, nil, 0600)
	os.WriteFile(socksSocket, nil, 0600)
	rg, e := exec.LookPath("rg")
	if e != nil {
		t.Fatal(e)
	}
	vendor, _ := filepath.Abs("../../../node_modules/@anthropic-ai/sandbox-runtime/vendor/seccomp/arm64")
	bpf, apply := filepath.Join(vendor, "unix-block.bpf"), filepath.Join(vendor, "apply-seccomp")
	options := []LinuxWrapOptions{}
	rows := []map[string]any{}
	commands := []string{"printf '%s' 'actual Linux wrapper'", "echo $VARIABLE; printf \"quoted\"", "multiline\ntext"}
	for _, command := range commands {
		for _, network := range []bool{false, true} {
			for _, allowUnix := range []bool{false, true} {
				for _, weak := range []bool{false, true} {
					h, s := float64(12345), float64(1080)
					read := &ReadRestriction{DenyOnly: []string{}, AllowWithinDeny: []string{}}
					options = append(options, LinuxWrapOptions{Read: read, NeedsNetworkRestriction: network, EnableWeakerNestedSandbox: weak, AllowAllUnixSockets: allowUnix, HTTPSocketPath: httpSocket, SOCKSSocketPath: socksSocket, HTTPProxyPort: &h, SOCKSProxyPort: &s, Shell: "/bin/bash", SeccompBPFPath: bpf, ApplySeccompPath: apply})
					rows = append(rows, map[string]any{"command": command, "read": read, "needsNetworkRestriction": network, "enableWeakerNestedSandbox": weak, "allowAllUnixSockets": allowUnix, "httpSocketPath": httpSocket, "socksSocketPath": socksSocket, "httpProxyPort": h, "socksProxyPort": s, "binShell": "/bin/bash", "seccompConfig": map[string]any{"bpfPath": bpf, "applyPath": apply}})
				}
			}
		}
	}
	options = append(options, LinuxWrapOptions{NeedsNetworkRestriction: true, HTTPSocketPath: filepath.Join(home, "absent"), SOCKSSocketPath: socksSocket, AllowAllUnixSockets: true, Shell: "/bin/bash"})
	rows = append(rows, map[string]any{"command": "missing bridge", "needsNetworkRestriction": true, "httpSocketPath": filepath.Join(home, "absent"), "socksSocketPath": socksSocket, "allowAllUnixSockets": true, "binShell": "/bin/bash"})
	options = append(options, LinuxWrapOptions{NeedsNetworkRestriction: true, AllowAllUnixSockets: true, Shell: "/no-shell"})
	rows = append(rows, map[string]any{"command": "missing shell", "needsNetworkRestriction": true, "allowAllUnixSockets": true, "binShell": "/no-shell"})
	input, _ := json.Marshal(map[string]any{"cwd": cwd, "rg": rg, "rows": rows})
	fixture, _ := filepath.Abs("testdata/linux-wrap.fixture.ts")
	cmd := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "PATH=/opt/homebrew/bin:/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "TMPDIR=" + temp}
	cmd.Stdin = bytes.NewReader(input)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(output))
	}
	var source []struct{ Result, Error *string }
	if e = json.Unmarshal(output, &source); e != nil {
		t.Fatal(e, string(output))
	}
	for i, o := range options {
		o.Filesystem = &LinuxFilesystem{Paths: PathContext{Home: home, CWD: cwd, Realpath: filepath.EvalSymlinks}, TemporaryDirectory: temp, RipgrepCommand: rg, SearchDepth: 3}
		o.FindExecutable = func(value string) (string, error) {
			if stat, e := os.Stat(value); e == nil && !stat.IsDir() {
				return value, nil
			}
			return "", os.ErrNotExist
		}
		got, e := WrapLinux(context.Background(), rows[i]["command"].(string), o)
		if source[i].Error != nil {
			if e == nil || e.Error() != *source[i].Error {
				t.Fatalf("Source%d error %v; native %v", i, *source[i].Error, e)
			}
			continue
		}
		if e != nil || source[i].Result == nil || got != *source[i].Result {
			t.Fatalf("Source%d %s; native %s %v", i, *source[i].Result, got, e)
		}
		if o.NeedsNetworkRestriction && (!strings.Contains(got, "--unshare-net") || !strings.Contains(got, "--unshare-pid")) {
			t.Fatal("original namespace boundaries missing", got)
		}
	}
}
