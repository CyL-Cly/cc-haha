//go:build !windows

package clibootstrap

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func linuxBootstrap(t *testing.T, settings string, bwrap, socat bool) (*MainPrint, string) {
	t.Helper()
	f := newNativePluginFixture(t)
	root, e := os.MkdirTemp(os.Getenv("TMPDIR"), "nl-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	socketRoot, e := os.MkdirTemp("/tmp", "gs-linux-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(socketRoot) })
	socketRoot, e = filepath.EvalSymlinks(socketRoot)
	if e != nil {
		t.Fatal(e)
	}
	if bwrap {
		f.write(t, filepath.Join(root, "bwrap"), "#!/bin/sh\nexit 99\n")
		os.Chmod(filepath.Join(root, "bwrap"), 0700)
	}
	if socat {
		// Compile a small generic transport primitive before the Source deadline.
		// Instrumented whole-CLI test startup is unrelated to socat readiness.
		source, _ := filepath.Abs("../../testdata/native-unix-tcp-relay/main.go")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "go", "build", "-o", filepath.Join(root, "socat"), source)
		cmd.Dir = root
		capturePublicGoCoverage(t, cmd)
		output, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatal("generic relay compilation", e, string(output))
		}
		probe := exec.CommandContext(ctx, filepath.Join(root, "socat"))
		capturePublicGoCoverage(t, probe)
		if e := probe.Run(); e == nil || probe.ProcessState.ExitCode() != 2 {
			t.Fatal("native relay executable probe", e)
		}
	}
	f.n.Environment["PATH"] = root + ":/opt/homebrew/bin:/usr/bin:/bin"
	f.n.Environment["TMPDIR"] = socketRoot
	f.n.Environment["CLAUDE_CODE_TMPDIR"] = f.n.Temp
	f.n.Environment["SHELL"] = "/bin/bash"
	f.n.Environment["DEBUG"] = "1"
	f.write(t, filepath.Join(f.config, "settings.json"), settings)
	disabled := false
	seedNativeReleaseWarmFixture(t, f, "fixture")
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", MaxTurns: 100, Tools: []string{"Read", "Bash"}, Thinking: requestcontext.Thinking{Type: "disabled"}, AdditionalDirectories: []string{f.config}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "linux", Interactive: true, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { m.Close() })
	return m, socketRoot
}

func TestLinuxMainPrintActualDependencyDisabledEnabledAssetsSettingsAndShutdown(t *testing.T) {
	for _, row := range []struct {
		enabled, required, bwrap, socat bool
		reason                          string
	}{
		{false, false, false, false, ""}, {true, true, false, false, "bubblewrap (bwrap) not installed"}, {true, true, true, false, "socat not installed"}, {true, false, false, false, ""}, {true, true, true, true, ""},
	} {
		t.Run(fmt.Sprintf("enabled%v-required%v-bwrap%v-socat%v", row.enabled, row.required, row.bwrap, row.socat), func(t *testing.T) {
			settings := fmt.Sprintf(`{"sandbox":{"enabled":%v,"failIfUnavailable":%v,"network":{"allowedDomains":[]}}}`, row.enabled, row.required)
			m, root := linuxBootstrap(t, settings, row.bwrap, row.socat)
			e := m.StartSession(context.Background())
			if row.reason != "" {
				if e == nil || !strings.Contains(e.Error(), row.reason) {
					t.Fatal("required original dependencies", e)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if !row.enabled || !row.bwrap || !row.socat {
				if m.sandbox == nil || !m.sandbox.dependenciesChecked || m.sandbox.manager != nil {
					t.Fatal("disabled/unavailable must probe without initialize")
				}
				if _, e = os.Stat(filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "Cache/native-sandbox-runtime-0.0.44")); !os.IsNotExist(e) {
					t.Fatal("disabled initialized assets", e)
				}
				return
			}
			if m.sandbox.manager == nil {
				if m.errorLogSink != nil {
					m.errorLogSink.Flush()
				}
				debugFiles, _ := filepath.Glob(filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "debug", "*.txt"))
				for _, p := range debugFiles {
					raw, _ := os.ReadFile(p)
					t.Log(string(raw))
				}
				errors, _ := filepath.Glob(filepath.Join(root, "listen-error-*"))
				for _, p := range errors {
					raw, _ := os.ReadFile(p)
					t.Log(string(raw))
				}
				t.Fatal("Linux production MainPrint manager not initialized", "bridge temp", m.sandbox.linux.TemporaryDirectory, "test root", root)
			}
			for _, path := range []string{m.sandbox.linux.BPF, m.sandbox.linux.Apply} {
				if info, e := os.Stat(path); e != nil || !info.Mode().IsRegular() {
					t.Fatal("actual pinned native asset missing", path, e)
				}
			}
			command, e := m.sandbox.manager.WrapContext(context.Background(), "printf linux-only", "/bin/bash")
			physicalAssets, pathError := filepath.EvalSymlinks(m.sandbox.linux.AssetRoot)
			if pathError != nil {
				t.Fatal(pathError)
			}
			if e != nil || !strings.Contains(command, "--unshare-net") || !strings.Contains(command, m.sandbox.linux.Apply) || !strings.Contains(command, "--ro-bind "+physicalAssets+" "+physicalAssets) {
				t.Fatal("actual Linux wrapper/helper protection", e, command)
			}
			same := m.sandbox.manager
			if e = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error { return m.initializeNativeSandbox(scope) }); e != nil || m.sandbox.manager != same {
				t.Fatal("duplicate initialize changed resources", e)
			}
			if e = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				m.settingState.ResetSettingsCache()
				converted, e := m.nativeSandboxConfig(scope, m.sandbox)
				if e != nil {
					return e
				}
				found := false
				for _, path := range converted.Config.Filesystem.DenyWrite {
					found = found || path == m.sandbox.linux.AssetRoot
				}
				if !found {
					return fmt.Errorf("settings reload discarded immutable helper protection")
				}
				same.Update(converted)
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			http, socks := same.ProxyPorts()
			if e = m.ShutdownSession(context.Background()); e != nil {
				t.Fatal(e)
			}
			for _, port := range []float64{http, socks} {
				c, e := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", int(port)), time.Second)
				if e == nil {
					c.Close()
					t.Fatal("actual MainPrint leaked proxy", port)
				}
			}
			matches, _ := filepath.Glob(filepath.Join(root, "claude-*.sock"))
			if len(matches) != 0 {
				t.Fatal("actual MainPrint leaked bridge sockets", matches)
			}
		})
	}
}

func TestLinuxMainPrintForeignPreparationRemainsRestricted(t *testing.T) {
	m, _ := linuxBootstrap(t, `{"sandbox":{"enabled":false}}`, false, false)
	if _, e := m.prepareNativeLinuxSandbox(nil, false); e != cooperative.ErrRestricted {
		t.Fatal("unowned producer admitted", e)
	}
}
