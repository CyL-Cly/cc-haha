package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestNativeColdSystemContextConcurrentInspectorDoesNotBlockProducer(t *testing.T) {
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{}`)
	f.n.Environment["ANTHROPIC_API_KEY"] = "offline-system-context"
	f.n.Environment["NODE_ENV"] = "production"
	f.n.Environment["GIT_CONFIG_NOSYSTEM"] = "1"
	f.n.Environment["GIT_CONFIG_GLOBAL"] = filepath.Join(f.home, "no-global")
	contextGit(t, f, "init", "-q", "--initial-branch=main")
	contextGit(t, f, "config", "user.name", "Private Concurrent Context")
	contextGit(t, f, "config", "user.email", "fixture@example.invalid")
	f.write(t, filepath.Join(f.cwd, "tracked.txt"), "private unchanged bytes\n")
	contextGit(t, f, "add", ".")
	contextGit(t, f, "commit", "-qm", "private initial")
	bin := filepath.Join(f.home, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	entered, release := filepath.Join(f.home, "git-entered"), filepath.Join(f.home, "git-release")
	// Only the readonly status subprocess is held. Both consumers use the real
	// Git filesystem, native worker delivery and the same canonical Owner.
	script := "#!/bin/sh\nif [ \"$1\" = --no-optional-locks ] && [ \"$2\" = status ]; then\n  : > '" + entered + "'\n  while [ ! -f '" + release + "' ]; do /bin/sleep 0.01; done\nfi\nexec /usr/bin/git \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	f.n.Environment["PATH"] = bin + ":/usr/bin:/bin"
	m, err := NewMainPrint(MainPrintOptions{Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: runtime.GOOS, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{Model: "deepseek-flash", Tools: []string{"Read"}, MaxTurns: 1, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	var first, second protocol.JSONObject
	read := func(output *protocol.JSONObject) func(*cooperative.Scope) error {
		return func(scope *cooperative.Scope) error {
			var err error
			*output, err = m.nativeInspectionSystemContext(scope)
			return err
		}
	}
	one, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, read(&first))
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(entered); err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("readonly Git producer never entered", ctx.Err())
		}
		time.Sleep(time.Millisecond)
	}
	secondEntered := make(chan struct{})
	two, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		close(secondEntered)
		return read(&second)(scope)
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondEntered:
	case <-ctx.Done():
		t.Fatal("same owner did not admit concurrent inspection")
	}
	if err = os.WriteFile(release, []byte("release owned Git"), 0600); err != nil {
		t.Fatal(err)
	}
	errOne, errTwo := one.Wait(), two.Wait()
	if errOne != nil || errTwo != nil {
		t.Fatal("concurrent consumer blocked the producer continuation", errOne, errTwo)
	}
	if first.String("gitStatus") == "" || first.String("gitStatus") != second.String("gitStatus") || !strings.Contains(first.String("gitStatus"), "Current branch: main") {
		t.Fatal("consumers did not share the actual original Git snapshot")
	}
	if bun := os.Getenv("CC_HAHA_SOURCE_BUN"); bun != "" {
		env := map[string]string{}
		for k, v := range f.n.Environment {
			env[k] = v
		}
		env["PATH"] = "/usr/bin:/bin"
		fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-system-context.fixture.ts")
		command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
		command.Dir = f.home
		command.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "PATH=/usr/bin:/bin", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CC_HAHA_SYSTEM_CONTEXT_FIXTURE=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home)}
		input, _ := json.Marshal(map[string]any{"home": f.home, "cwd": f.cwd, "environment": env, "concurrent": true})
		command.Stdin = bytes.NewReader(input)
		var stderr bytes.Buffer
		command.Stderr = &stderr
		data, err := command.Output()
		if err != nil {
			t.Fatal("actual Source concurrent system context", err, stderr.String())
		}
		var want struct{ First, Repeated protocol.JSONObject }
		if err = json.Unmarshal(data, &want); err != nil {
			t.Fatal(err, string(data))
		}
		if want.First.String("gitStatus") != first.String("gitStatus") || want.Repeated.String("gitStatus") != second.String("gitStatus") {
			t.Fatal("concurrent memo differs from actual Source")
		}
	}
}
