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

func TestActualPublicAutoMemoryRequestedDirectoryAndFileContextAgainstWholeSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	for _, profile := range []string{"absent", "absolute", "relative", "empty", "decomposed", "relative-parent"} {
		t.Run(profile, func(t *testing.T) {
			var mu sync.Mutex
			var posts []json.RawMessage
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				body, err := io.ReadAll(r.Body)
				if err != nil || r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
					t.Errorf("private auto-memory model request: %s %s %v", r.Method, r.URL, err)
				}
				mu.Lock()
				posts = append(posts, append(json.RawMessage(nil), body...))
				mu.Unlock()
				response(w, "actual-memory-directory", "", nil, "AUTO_MEMORY_DIRECTORY_RESPONSE")
			}))
			defer peer.Close()
			o := nativeSessionOptions(t, peer.URL)
			home, err := filepath.EvalSymlinks(o.Native.Home)
			if err != nil {
				t.Fatal(err)
			}
			fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-auto-memory-config-directory.fixture.ts")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, bun, "--feature=TRANSCRIPT_CLASSIFIER", "--no-env-file", fixture)
			child.Dir = home
			child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_AUTO_MEMORY_DIRECTORY_SOURCE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
			child.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "profile": profile}))
			output, err := child.CombinedOutput()
			var source struct {
				Requested                                                                                 *string
				Project, InitialDirectory, InitialEntrypoint, InitialPhysical, InitialText, NextDirectory string
				Stages                                                                                    []struct {
					Phase, Directory, Physical, Prompt, Context string
					SameFiles, SameSettings, DirectoryExists    bool
				}
				Caps struct{ Blocked, SecureReads []string }
			}
			if err != nil || json.Unmarshal(output, &source) != nil || len(source.Stages) != 4 || len(source.Caps.Blocked) != 0 || len(source.Caps.SecureReads) != 0 {
				t.Fatalf("whole original auto-memory Source: %v %.8000s", err, output)
			}
			t.Logf("whole Source first-cold memory consumer: %s", raw(map[string]any{"profile": profile, "requested": source.Requested, "project": source.Project, "stage": source.Stages[0], "caps": source.Caps}))
			for i, stage := range source.Stages {
				if !stage.SameSettings || !stage.DirectoryExists || stage.SameFiles != (i < 3) || i < 2 && stage.Directory != source.InitialDirectory || i >= 2 && stage.Directory != source.NextDirectory {
					t.Fatalf("original path/instruction/merged-settings cache lifetime: %+v", source.Stages)
				}
			}
			old := o.Native
			env := map[string]string{}
			for key, value := range o.Environment {
				env[key] = value
			}
			env["HOME"], env["USERPROFILE"], env["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = home, home, "0"
			if source.Requested == nil {
				delete(env, "CLAUDE_CONFIG_DIR")
			} else {
				env["CLAUDE_CONFIG_DIR"] = *source.Requested
			}
			native, err := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: source.Project, Platform: o.Platform, ManagedRoot: filepath.Join(home, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
			if err != nil {
				t.Fatal(err)
			}
			native.RunSecurity, native.PreconnectHTTP = old.RunSecurity, old.PreconnectHTTP
			o.Native, o.Environment, o.WorkDir, o.Stderr = native, env, native.CWD, io.Discard
			command, _, err := ParseCommand([]string{"-p", "--model", "fixture-model", "--tools", "", "--setting-sources", "user", "--thinking", "disabled", "AUTO_MEMORY_DIRECTORY_HUMAN"}, o.Version, o.WorkDir, native.Temp, env)
			if err != nil {
				t.Fatal(err)
			}
			var result bytes.Buffer
			code, err := RunCommand(ctx, command, o, strings.NewReader(""), &result, nil)
			if err != nil || code != 0 || !strings.Contains(result.String(), "AUTO_MEMORY_DIRECTORY_RESPONSE") {
				t.Fatal("actual public memory result", code, err, result.String())
			}
			mu.Lock()
			defer mu.Unlock()
			if len(posts) != 1 {
				t.Fatalf("expected exactly one actual model POST, got %d", len(posts))
			}
			t.Logf("actual public native model POST: %s", posts[0])
			var request any
			if err := json.Unmarshal(posts[0], &request); err != nil {
				t.Fatal(err)
			}
			var text []string
			var collect func(any)
			collect = func(value any) {
				switch value := value.(type) {
				case string:
					text = append(text, value)
				case []any:
					for _, item := range value {
						collect(item)
					}
				case map[string]any:
					for _, item := range value {
						collect(item)
					}
				}
			}
			collect(request)
			actualText := strings.Join(text, "\n")
			for label, want := range map[string]string{"full original auto-memory prompt": source.Stages[0].Prompt, "full original auto-memory file context": source.Stages[0].Context} {
				if !strings.Contains(actualText, want) {
					t.Errorf("%s differs for requested %v: want %.900s; actual %.16000s", label, source.Requested, want, actualText)
				}
			}
			entrypoint := source.InitialEntrypoint
			if !filepath.IsAbs(entrypoint) {
				entrypoint = filepath.Join(source.Project, entrypoint)
			}
			content, err := os.ReadFile(entrypoint)
			if err != nil || string(content) != source.InitialText {
				t.Fatal("actual memory request mutated original physical entrypoint", entrypoint, err, string(content))
			}
			settings, err := os.ReadFile(filepath.Join(source.InitialPhysical, "settings.json"))
			if err != nil || string(settings) != "{}\n" {
				t.Fatal("actual memory request changed original settings", err, string(settings))
			}
		})
	}
}
