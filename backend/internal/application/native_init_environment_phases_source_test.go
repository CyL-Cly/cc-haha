package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualPublicInitSafePreconnectAndFullModelEnvironmentAgainstWholeSource(t *testing.T) {
	for _, profile := range []struct {
		name                string
		userEnv, projectEnv map[string]string
		settingSources      string
		heads               int
	}{
		{name: "safe-before-full", heads: 1},
		{name: "project-proxy-applied-after-preconnect", projectEnv: map[string]string{"HTTPS_PROXY": "http://127.0.0.1:1", "NO_PROXY": "127.0.0.1"}, heads: 1},
		{name: "trusted-proxy-skips-init-cleared-for-model", userEnv: map[string]string{"HTTPS_PROXY": "http://127.0.0.1:1"}, projectEnv: map[string]string{"HTTPS_PROXY": "", "NO_PROXY": "127.0.0.1"}, heads: 0},
		{name: "project-source-disabled", settingSources: "user", heads: 1},
	} {
		t.Run(profile.name, func(t *testing.T) {
			bun := os.Getenv("CC_HAHA_SOURCE_BUN")
			if bun == "" {
				t.Skip("pinned actual Source Bun required")
			}
			type request struct{ Method, Path string }
			var mu sync.Mutex
			requests := []request{}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requests = append(requests, request{r.Method, r.URL.Path})
				mu.Unlock()
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				body, _ := io.ReadAll(r.Body)
				if r.Method != http.MethodPost || !strings.Contains(string(body), "INIT_PHASES_HUMAN") {
					t.Error("unexpected init/model request", r.Method, r.URL, string(body))
				}
				response(w, "init-phases", "", nil, "INIT_PHASES_RESPONSE")
			}))
			defer peer.Close()
			o := nativeSessionOptions(t, "http://127.0.0.1:1/unselected-launch-base")
			home, err := filepath.EvalSymlinks(o.Native.Home)
			if err != nil {
				t.Fatal(err)
			}
			fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cli-init-environment-phases.fixture.ts")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, bun, "--feature=TRANSCRIPT_CLASSIFIER", "--no-env-file", fixture)
			child.Dir = home
			child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_INIT_PHASES_SOURCE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "ANTHROPIC_API_KEY=private-init-phases-fake-key", "PATH=/usr/bin:/bin"}
			child.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "origin": peer.URL, "userEnv": profile.userEnv, "projectEnv": profile.projectEnv, "settingSources": profile.settingSources}))
			output, err := child.CombinedOutput()
			var source struct {
				Project    string
				Safe, Full struct{ Base, Trusted, Project string }
				Observed   []struct{ URL, Method string }
				Files      []struct{ Path, Content string }
				Caps       struct{ Blocked, SecureReads []string }
			}
			if err != nil || json.Unmarshal(output, &source) != nil || len(source.Observed) != profile.heads || source.Safe.Base != peer.URL+"/safe" || source.Full.Base != peer.URL+map[bool]string{false: "/full", true: "/safe"}[profile.settingSources == "user"] || len(source.Caps.Blocked) != 0 || len(source.Caps.SecureReads) != 0 {
				t.Fatalf("actual safe/init/full Source: %v %.6000s", err, output)
			}
			mu.Lock()
			if len(requests) != profile.heads || profile.heads == 1 && requests[0] != (request{http.MethodHead, "/safe"}) {
				t.Error("whole Source physical init request", requests)
			}
			requests = nil
			mu.Unlock()
			old := o.Native
			env := map[string]string{}
			for key, value := range o.Environment {
				env[key] = value
			}
			env["HOME"], env["USERPROFILE"], env["CLAUDE_CONFIG_DIR"] = home, home, filepath.Join(home, ".claude")
			native, err := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: source.Project, Platform: o.Platform, ManagedRoot: filepath.Join(home, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
			if err != nil {
				t.Fatal(err)
			}
			native.RunSecurity, native.PreconnectHTTP = old.RunSecurity, old.PreconnectHTTP
			o.Native, o.Environment, o.WorkDir = native, env, native.CWD
			o.Stderr = io.Discard
			args := []string{"-p", "--model", "fixture-model", "--tools", "", "--system-prompt", "private actual init phases", "--thinking", "disabled", "INIT_PHASES_HUMAN"}
			if profile.settingSources != "" {
				args = append([]string{"--setting-sources", profile.settingSources}, args...)
			}
			command, _, err := ParseCommand(args, o.Version, o.WorkDir, native.Temp, env)
			if err != nil {
				t.Fatal(err)
			}
			var result bytes.Buffer
			code, err := RunCommand(ctx, command, o, strings.NewReader(""), &result, nil)
			if err != nil || code != 0 || !strings.Contains(result.String(), "INIT_PHASES_RESPONSE") {
				t.Fatal("actual next Harness result", code, err, result.String())
			}
			mu.Lock()
			defer mu.Unlock()
			var heads, posts []request
			for _, request := range requests {
				if request.Method == http.MethodHead {
					heads = append(heads, request)
				} else {
					posts = append(posts, request)
				}
			}
			if len(heads) != profile.heads || profile.heads == 1 && heads[0].Path != "/safe" || len(posts) != 1 || peer.URL+posts[0].Path != source.Full.Base+"/v1/messages" {
				t.Errorf("actual normal init/next model phases differ from whole Source: HEAD %v POST %v; Source safe %s full %s", heads, posts, source.Safe.Base, source.Full.Base)
			}
			for _, file := range source.Files {
				bytes, err := os.ReadFile(file.Path)
				if err != nil || string(bytes) != file.Content {
					t.Errorf("init changed protected settings %s: %v", file.Path, err)
				}
			}
		})
	}
}
