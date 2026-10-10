package errorlog

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type physicalSinkFiles struct{}

func (physicalSinkFiles) MkdirAll(p string) error { return os.MkdirAll(p, 0700) }
func (physicalSinkFiles) AppendFile(p string, b []byte) error {
	f, e := os.OpenFile(p, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	c := f.Close()
	if e != nil {
		return e
	}
	return c
}
func (physicalSinkFiles) Remove(p string) error     { return os.Remove(p) }
func (physicalSinkFiles) Symlink(p, l string) error { return os.Symlink(p, l) }
func sinkSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if e := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			link, e := os.Readlink(p)
			out[p] = "link:" + link
			return e
		}
		b, e := os.ReadFile(p)
		out[p] = string(b)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	return out
}
func TestNativeErrorSinkFilesAndDeferredOverflowAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	profiles := []struct {
		env            map[string]string
		args           []string
		count          int
		formatted      bool
		failAfterInit  bool
		message, stack string
	}{
		{env: map[string]string{}, count: 1, message: "private lookup failure"},
		{env: map[string]string{"DISABLE_TELEMETRY": "1"}, count: 1, message: "private lookup failure"},
		{env: map[string]string{"DEBUG": "true"}, count: 2, message: "private lookup failure"},
		{env: map[string]string{"DEBUG": "true"}, count: 2, message: "private lookup failure", failAfterInit: true},
		{env: map[string]string{"DEBUG_SDK": "1"}, count: 2, message: "private lookup failure", stack: "private\noriginal stack", formatted: true},
		{env: map[string]string{"DEBUG": "0"}, count: 1, message: "private lookup failure"},
		{env: map[string]string{"DEBUG": "true", "CLAUDE_CODE_DEBUG_LOG_LEVEL": "error"}, count: 1, message: "private lookup failure"},
		{env: map[string]string{"DEBUG": "true", "CLAUDE_CODE_DEBUG_LOGS_DIR": "<PRIVATE_DEBUG_FILE>"}, count: 2, message: "private lookup failure"},
		{env: map[string]string{}, args: []string{"--debug-file=<PRIVATE_DEBUG_FILE>"}, count: 2, message: "private lookup failure"},
		{env: map[string]string{"USER_TYPE": "ant"}, count: 1, message: "private lookup failure"},
		{env: map[string]string{"USER_TYPE": "ant"}, count: 101, message: "private lookup failure"},
		{env: map[string]string{"USER_TYPE": "ant", "NODE_ENV": "test"}, count: 51, message: "private lookup failure"},
		{env: map[string]string{"DEBUG": "true", "DISABLE_ERROR_REPORTING": "0"}, count: 1, message: "private lookup failure"},
		{env: map[string]string{"DEBUG": "true", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "false"}, count: 1, message: "private lookup failure"},
		{env: map[string]string{"DEBUG": "true", "CLAUDE_CODE_USE_BEDROCK": "true"}, count: 1, message: "private lookup failure"},
		{env: map[string]string{}, args: []string{"--debug-to-stderr"}, count: 1, message: "private lookup failure"},
		{env: map[string]string{"NODE_ENV": "test"}, args: []string{"-d2e"}, count: 1, message: "private lookup failure"},
		{env: map[string]string{}, args: []string{"--debug=error"}, count: 1, message: "private lookup failure"},
		{env: map[string]string{}, args: []string{"--debug=!error"}, count: 1, message: "private lookup failure"},
		{env: map[string]string{}, args: []string{"--debug=other"}, count: 1, message: "private lookup failure"},
		{env: map[string]string{}, args: []string{"--debug=other,!error"}, count: 1, message: "private lookup failure"},
	}
	for i, p := range profiles {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			root := t.TempDir()
			config := filepath.Join(root, ".claude")
			if e := os.MkdirAll(config, 0700); e != nil {
				t.Fatal(e)
			}
			cwd := filepath.Join(root, strings.Repeat("long-汉😀", 50))
			env := map[string]string{"CLAUDE_CONFIG_DIR": config}
			for k, v := range p.env {
				env[k] = v
			}
			for k, v := range env {
				env[k] = strings.ReplaceAll(v, "<PRIVATE_DEBUG_FILE>", filepath.Join(config, "custom", "explicit-log"))
			}
			args := append([]string{}, p.args...)
			for i, arg := range args {
				args[i] = strings.ReplaceAll(arg, "<PRIVATE_DEBUG_FILE>", filepath.Join(config, "custom", "explicit-log"))
			}
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-error-sink.fixture.ts")
			data, _ := json.Marshal(map[string]any{"environment": env, "arguments": args, "cwd": cwd, "count": p.count, "formatted": p.formatted, "failAfterInit": p.failAfterInit, "message": p.message, "stack": p.stack})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = root
			cmd.Env = []string{"HOME=" + root, "USERPROFILE=" + root, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + root, "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(root), "CC_HAHA_ERROR_LOG_FIXTURE=1", "PATH=/usr/bin:/bin"}
			cmd.Stdin = bytes.NewReader(data)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, e := cmd.Output()
			if e != nil {
				t.Fatal(e, stderr.String())
			}
			var source struct {
				Before, After map[string]string
				Stderr        string
				Memory        []Row
				Version       string
			}
			if e = json.Unmarshal(out, &source); e != nil {
				t.Fatal(e, string(out))
			}
			if e = os.RemoveAll(config); e != nil {
				t.Fatal(e)
			}
			if e = os.MkdirAll(config, 0700); e != nil {
				t.Fatal(e)
			}
			deferred := []func(){}
			var emitted strings.Builder
			now := func() time.Time { return time.UnixMilli(1700000000000) }
			sink, e := NewNativeSink(NativeSinkOptions{Files: physicalSinkFiles{}, Now: now, Metadata: func() Metadata {
				return Metadata{Environment: env, CWD: cwd, Home: root, ConfigDir: config, Platform: "darwin", SessionID: "private-error-session", Version: source.Version}
			}, Arguments: args, Formatted: p.formatted, Stderr: func(s string) { emitted.WriteString(s) }, Defer: func(f func()) { deferred = append(deferred, f) }})
			if e != nil {
				t.Fatal(e)
			}
			defer sink.Close()
			if p.failAfterInit {
				path := filepath.Join(config, "debug", "private-error-session.txt")
				if e = os.Remove(path); e != nil {
					t.Fatal(e)
				}
				if e = os.Mkdir(path, 0700); e != nil {
					t.Fatal(e)
				}
			}
			service, _ := New(Options{Environment: func() map[string]string { return env }, Now: now})
			service.Attach(sink.Write)
			for n := 0; n < p.count; n++ {
				suffix := "#" + stringInt(n)
				stack := ""
				if p.stack != "" {
					stack = p.stack + suffix
				}
				service.Log(Error{Name: "Error", Message: p.message + suffix, Stack: stack})
			}
			before := sinkSnapshot(t, config)
			sink.Close()
			for _, f := range deferred {
				f()
			}
			after := sinkSnapshot(t, config)
			if !reflect.DeepEqual(before, source.Before) || !reflect.DeepEqual(after, source.After) || emitted.String() != source.Stderr || !reflect.DeepEqual(service.Memory(), source.Memory) {
				t.Fatalf("profile%d\nbeforeGo%v Source%v\nafterGo%v Source%v\nstderrGo%q Source%q\nmemoryGo%v Source%v", i, before, source.Before, after, source.After, emitted.String(), source.Stderr, service.Memory(), source.Memory)
			}
		})
	}
}
func stringInt(n int) string { b, _ := json.Marshal(n); return string(b) }
