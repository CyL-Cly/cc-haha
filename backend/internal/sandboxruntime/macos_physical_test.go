package sandboxruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeMacSeatbeltPhysicalAllowedWriteDenyProtectedAndBareGitScrub(t *testing.T) {
	if os.Getenv("CC_HAHA_SANDBOX_PHYSICAL_BROKER") != "1" {
		t.Skip("dedicated nonnested Seatbelt broker required; outer sandbox cannot sandbox_apply")
	}
	if _, e := exec.LookPath("sandbox-exec"); e != nil {
		t.Skip("physical macOS Seatbelt required")
	}
	home := t.TempDir()
	cwd := filepath.Join(home, "repo")
	outside := filepath.Join(home, "outside")
	os.MkdirAll(cwd, 0700)
	os.MkdirAll(outside, 0700)
	env := map[string]string{"HOME": home, "PATH": "/usr/bin:/bin", "CLAUDE_TMPDIR": filepath.Join(home, "tmp")}
	config := Config{Network: NetworkConfig{AllowedDomains: []string{}, DeniedDomains: []string{}}, Filesystem: FilesystemConfig{AllowWrite: []string{cwd}, DenyWrite: []string{}, DenyRead: []string{}, AllowRead: []string{}}}
	manager, e := NewManager(context.Background(), ConvertedConfig{Config: config, ScrubPaths: []string{filepath.Join(cwd, "HEAD"), filepath.Join(cwd, "objects")}}, ManagerOptions{Platform: "darwin", Home: home, CWD: cwd, Environment: env, Realpath: filepath.EvalSymlinks, SessionSuffix: "_9_SBX", FindExecutable: func(name string) (string, error) {
		if filepath.IsAbs(name) {
			return name, nil
		}
		return exec.LookPath(name)
	}, Network: NetworkOptions{Environment: env}})
	if e != nil {
		t.Fatal(e)
	}
	defer manager.Close(context.Background())
	run := func(script string) (string, error) {
		t.Helper()
		wrapped, e := manager.Wrap(script, "/bin/bash")
		if e != nil {
			t.Fatal(e)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		http, socks := manager.ProxyPorts()
		input, _ := json.Marshal(map[string]any{"home": home, "cwd": cwd, "rows": []any{map[string]any{"command": script, "needsNetworkRestriction": true, "binShell": "/bin/bash", "httpProxyPort": http, "socksProxyPort": socks, "read": map[string]any{"denyOnly": []string{}, "allowWithinDeny": []string{}}, "write": map[string]any{"allowOnly": append(DefaultWritePaths(home), cwd), "denyWithinAllow": []string{}}}}})
		fixture, _ := filepath.Abs("testdata/macos-wrap.fixture.ts")
		original := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
		original.Dir = home
		original.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CLAUDE_TMPDIR=" + env["CLAUDE_TMPDIR"], "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
		original.Stdin = bytes.NewReader(input)
		raw, e := original.CombinedOutput()
		if e != nil {
			t.Fatal(e, string(raw))
		}
		var observation struct {
			Rows []struct{ Result, Error *string }
		}
		if e = json.Unmarshal(raw, &observation); e != nil || len(observation.Rows) != 1 || observation.Rows[0].Result == nil {
			t.Fatal("actual Source physical wrapper", e, string(raw))
		}
		source := *observation.Rows[0].Result
		if wrapped != source {
			t.Fatal("native child policy differs from actual Source", wrapped, source)
		}
		var previous string
		var previousFailure bool
		for i, wrapper := range []string{source, wrapped} {
			// Only trusted wrapper parsing occurs before sandbox-exec. The
			// application script starts inside actual Source's Seatbelt profile.
			cmd := exec.CommandContext(ctx, "/bin/bash", "-c", wrapper)
			cmd.Dir = cwd
			cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatal("physical sandbox child failed to settle", ctx.Err(), string(out))
			}
			if i == 0 {
				previous = string(out)
				previousFailure = err != nil
			} else if previous != string(out) || previousFailure != (err != nil) {
				t.Fatal("Source/native physical result", previous, previousFailure, string(out), err)
			}
			e = err
		}
		return previous, e
	}
	out, e := run("printf 'exact private bytes' > allowed.txt")
	if e != nil {
		t.Fatal(e, out)
	}
	bytes, e := os.ReadFile(filepath.Join(cwd, "allowed.txt"))
	if e != nil || string(bytes) != "exact private bytes" {
		t.Fatal(e, string(bytes))
	}
	out, e = run("printf forbidden > " + filepath.Join(outside, "denied.txt"))
	if e == nil {
		t.Fatal("Seatbelt allowed outside write", out)
	}
	if _, e = os.Stat(filepath.Join(outside, "denied.txt")); !os.IsNotExist(e) {
		t.Fatal("outside write escaped", e)
	}
	out, e = run("printf forbidden > .gitconfig")
	if e == nil {
		t.Fatal("Seatbelt allowed mandatory dangerous file", out)
	}
	if _, e = os.Stat(filepath.Join(cwd, ".gitconfig")); !os.IsNotExist(e) {
		t.Fatal("dangerous file escaped", e)
	}
	out, e = run("printf planted > HEAD; mkdir -p objects")
	if e != nil {
		t.Fatal("Source permits absent bare candidates during sandboxed command", e, out)
	}
	if e = manager.CleanupAfterCommand(); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"HEAD", "objects"} {
		if _, e = os.Stat(filepath.Join(cwd, name)); !os.IsNotExist(e) {
			t.Fatal("planted bare repo survived actual cleanup", name, e)
		}
	}
	out, e = run("/usr/bin/nc -v -G 1 -w 1 203.0.113.1 80")
	if e == nil || !strings.Contains(out, "Operation not permitted") {
		t.Fatal("actual sandbox must reject outbound at OS boundary", out, e)
	}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("owned loopback response")) }))
	defer peer.Close()
	config.Network.AllowedDomains = []string{"127.0.0.1"}
	manager.Update(ConvertedConfig{Config: config})
	httpPort, _ := manager.ProxyPorts()
	out, e = run("/usr/bin/curl --silent --show-error --noproxy '' --proxy http://127.0.0.1:" + numberString(httpPort) + " " + peer.URL)
	if e != nil || out != "owned loopback response" {
		t.Fatal("sandbox child cannot reach owned approved loopback", out, e)
	}
	if e = manager.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e = manager.Wrap("true", "/bin/bash"); e == nil || !strings.Contains(e.Error(), "Sandbox failed to initialize.") {
		t.Fatal("closed manager admitted shell", e)
	}
}
