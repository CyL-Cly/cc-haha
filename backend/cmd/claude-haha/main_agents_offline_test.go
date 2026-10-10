//go:build offline_native_fixture

package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicAgentsReadsPrivateDefinitions(t *testing.T) {
	allocation, e := os.MkdirTemp("", "cc-haha-agents-main-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(allocation)
	allocation, e = filepath.EvalSymlinks(allocation)
	if e != nil {
		t.Fatal(e)
	}
	home, cwd, tmp := filepath.Join(allocation, "home"), filepath.Join(allocation, "workspace"), filepath.Join(allocation, "tmp")
	for _, p := range []string{home, cwd, tmp, filepath.Join(home, ".claude/agents"), filepath.Join(cwd, ".claude/agents"), filepath.Join(cwd, ".git")} {
		if e = os.MkdirAll(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	if e = os.WriteFile(filepath.Join(cwd, ".claude/agents/own.md"), []byte("---\nname: Own Fixture\ndescription: Reads only private inputs\nmodel: Custom-Alias\nmemory: project\nfuture: keep\n---\nOwn prompt\n"), 0600); e != nil {
		t.Fatal(e)
	}
	for k, v := range map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude"), "TMPDIR": tmp, "USER_TYPE": "external", "NODE_ENV": "production", "TZ": "UTC", "CLAUDE_CODE_USE_NATIVE_FILE_SEARCH": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH": "1", "CLAUDE_CODE_ENTRYPOINT": "", "CLAUDE_CODE_SIMPLE": "", "CLAUDE_CODE_FORCE_RECOVERY_CLI": "", "ANTHROPIC_API_KEY": "", "ANTHROPIC_AUTH_TOKEN": "", "ANTHROPIC_BASE_URL": ""} {
		t.Setenv(k, v)
	}
	t.Chdir(cwd)
	oldOut, oldErr := os.Stdout, os.Stderr
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()
	outR, outW, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer outR.Close()
	errR, errW, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer errR.Close()
	os.Stdout, os.Stderr = outW, errW
	code := run(context.Background(), []string{"agents", "--setting-sources", "project"})
	outW.Close()
	errW.Close()
	out, _ := io.ReadAll(outR)
	errout, _ := io.ReadAll(errR)
	if code != 0 || len(errout) > 0 || !strings.Contains(string(out), "Project agents:\n  Own Fixture · Custom-Alias · project memory\n") || !strings.Contains(string(out), "Built-in agents:") {
		t.Fatalf("actual agents: code=%d stdout=%q stderr=%q", code, out, errout)
	}
}

func TestPublicAgentsDiagnosticsAndCapabilities(t *testing.T) {
	allocation, e := os.MkdirTemp("", "cc-haha-agents-diagnostics-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(allocation)
	allocation, e = filepath.EvalSymlinks(allocation)
	if e != nil {
		t.Fatal(e)
	}
	home, cwd, tmp := filepath.Join(allocation, "home"), filepath.Join(allocation, "workspace"), filepath.Join(allocation, "tmp")
	for _, p := range []string{home, cwd, tmp, filepath.Join(cwd, ".git")} {
		if e = os.MkdirAll(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	for k, v := range map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude"), "TMPDIR": tmp, "USER_TYPE": "external", "NODE_ENV": "production", "TZ": "UTC", "CLAUDE_CODE_USE_NATIVE_FILE_SEARCH": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH": "1", "CLAUDE_CODE_ENTRYPOINT": "", "CLAUDE_CODE_SIMPLE": "", "CLAUDE_CODE_FORCE_RECOVERY_CLI": "", "ANTHROPIC_API_KEY": "", "ANTHROPIC_AUTH_TOKEN": "", "ANTHROPIC_BASE_URL": ""} {
		t.Setenv(k, v)
	}
	t.Chdir(cwd)
	capture := func(args []string) (int, string, string) {
		oldOut, oldErr := os.Stdout, os.Stderr
		outR, outW, e := os.Pipe()
		if e != nil {
			t.Fatal(e)
		}
		errR, errW, e := os.Pipe()
		if e != nil {
			t.Fatal(e)
		}
		os.Stdout, os.Stderr = outW, errW
		code := run(context.Background(), args)
		outW.Close()
		errW.Close()
		os.Stdout, os.Stderr = oldOut, oldErr
		out, _ := io.ReadAll(outR)
		errout, _ := io.ReadAll(errR)
		outR.Close()
		errR.Close()
		return code, string(out), string(errout)
	}
	for _, c := range []struct {
		args     []string
		code     int
		out, err string
	}{{[]string{"agents", "--help"}, 0, "Usage: claude agents", ""}, {[]string{"agents", "extra"}, 1, "", "too many arguments"}, {[]string{"agents", "--unknown"}, 1, "", "unknown option"}, {[]string{"agents", "--setting-sources", "bad", "--help"}, 1, "", "Invalid setting source"}} {
		code, out, errout := capture(c.args)
		if code != c.code || !strings.Contains(out, c.out) || !strings.Contains(errout, c.err) {
			t.Errorf("%v code=%d stdout=%q stderr=%q", c.args, code, out, errout)
		}
	}
	t.Setenv("CLAUDE_AGENT_SDK_DISABLE_BUILTIN_AGENTS", "1")
	code, out, errout := capture([]string{"agents", "--setting-sources", ""})
	if code != 0 || out != "No agents found.\n" || errout != "" {
		t.Fatalf("empty result=%d %q %q", code, out, errout)
	}
	t.Setenv("CLAUDE_AGENT_SDK_DISABLE_BUILTIN_AGENTS", "")
	t.Setenv("CLAUDE_CODE_USE_NATIVE_FILE_SEARCH", "0")
	code, out, errout = capture([]string{"agents"})
	if code != 1 || out != "" || !strings.Contains(errout, "ripgrep") {
		t.Fatalf("unported search=%d %q %q", code, out, errout)
	}
	t.Setenv("CLAUDE_CODE_USE_NATIVE_FILE_SEARCH", "1")
	t.Setenv("ANTHROPIC_API_KEY", "not-allowed")
	code, out, errout = capture([]string{"agents"})
	if code != 1 || out != "" || !strings.Contains(errout, "ANTHROPIC_API_KEY") {
		t.Fatalf("native guard=%d %q %q", code, out, errout)
	}
}
