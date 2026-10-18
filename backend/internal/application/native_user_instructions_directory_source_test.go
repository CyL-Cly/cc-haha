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

func TestActualPublicUserInstructionsRequestedMetadataDefaultGatesAgainstWholeSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned whole Source Bun required")
	}
	for _, profile := range []string{"absent", "absolute", "relative", "empty", "relative-parent", "decomposed", "user-disabled", "managed-only", "bare"} {
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
					t.Errorf("private user-instructions request %s %s: %v", r.Method, r.URL, err)
				}
				mu.Lock()
				posts = append(posts, append(json.RawMessage(nil), body...))
				mu.Unlock()
				response(w, "actual-user-instructions", "", nil, "USER_INSTRUCTIONS_DIRECTORY_RESPONSE")
			}))
			defer peer.Close()
			o := nativeSessionOptions(t, peer.URL)
			home, err := filepath.EvalSymlinks(o.Native.Home)
			if err != nil {
				t.Fatal(err)
			}
			fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-user-instructions-config-directory.fixture.ts")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, bun, "--feature=TRANSCRIPT_CLASSIFIER", "--no-env-file", fixture)
			child.Dir = home
			child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_USER_INSTRUCTIONS_DIRECTORY_SOURCE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin", "TZ=UTC"}
			child.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "profile": profile}))
			output, err := child.CombinedOutput()
			var source struct {
				Requested                                                                        *string
				Project, Managed, InitialPhysical, InitialMode                                   string
				AutoEnabled                                                                      bool
				InitialPrompt                                                                    *string
				FirstContext                                                                     struct{ ClaudeMd string }
				InitialFiles                                                                     []struct{ Path, Type, Content string }
				SameWarmContext, SameContextAfterFilesClear, SameWarmFiles, NextContextDifferent bool
				Caps                                                                             struct{ Blocked, SecureReads []string }
			}
			if err != nil || json.Unmarshal(output, &source) != nil || len(source.Caps.Blocked) != 0 || len(source.Caps.SecureReads) != 0 || !source.SameWarmContext || !source.SameContextAfterFilesClear || !source.SameWarmFiles || !source.NextContextDifferent {
				t.Fatalf("whole original user-instructions consumer: %v %.8000s", err, output)
			}
			t.Logf("whole Source default user-instructions consumer: %s", raw(source))
			wantMode := "claude-md-or-agents-md"
			if profile == "managed-only" {
				wantMode = "managed-only"
			}
			if source.InitialMode != wantMode || source.AutoEnabled != (profile != "bare") {
				t.Fatal("default original mode/auto-memory gates", source.InitialMode, source.AutoEnabled)
			}
			old := o.Native
			env := map[string]string{}
			for key, value := range o.Environment {
				env[key] = value
			}
			env["HOME"], env["USERPROFILE"] = home, home
			delete(env, "CLAUDE_CODE_DISABLE_AUTO_MEMORY")
			delete(env, "CLAUDE_CODE_DISABLE_CLAUDE_MDS")
			if source.Requested == nil {
				delete(env, "CLAUDE_CONFIG_DIR")
			} else {
				env["CLAUDE_CONFIG_DIR"] = *source.Requested
			}
			native, err := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: source.Project, Platform: o.Platform, ManagedRoot: source.Managed, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
			if err != nil {
				t.Fatal(err)
			}
			native.RunSecurity, native.PreconnectHTTP = old.RunSecurity, old.PreconnectHTTP
			o.Native, o.Environment, o.WorkDir, o.Stderr = native, env, native.CWD, io.Discard
			args := []string{"-p", "--model", "fixture-model", "--tools", "", "--setting-sources", "user", "--thinking", "disabled", "USER_INSTRUCTIONS_DIRECTORY_HUMAN"}
			if profile == "user-disabled" {
				args[6] = ""
			}
			if profile == "bare" {
				args = append([]string{"--bare"}, args...)
			}
			command, _, err := ParseCommand(args, o.Version, o.WorkDir, native.Temp, env)
			if err != nil {
				t.Fatal(err)
			}
			var result bytes.Buffer
			code, err := RunCommand(ctx, command, o, strings.NewReader(""), &result, nil)
			if err != nil || code != 0 || !strings.Contains(result.String(), "USER_INSTRUCTIONS_DIRECTORY_RESPONSE") {
				t.Fatal("actual public user-instructions result", code, err, result.String())
			}
			mu.Lock()
			defer mu.Unlock()
			if len(posts) != 1 {
				t.Fatalf("expected one actual model POST, got %d", len(posts))
			}
			t.Logf("actual public native default model POST: %s", posts[0])
			var request any
			if err := json.Unmarshal(posts[0], &request); err != nil {
				t.Fatal(err)
			}
			var parts []string
			var collect func(any)
			collect = func(value any) {
				switch value := value.(type) {
				case string:
					parts = append(parts, value)
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
			actual := strings.Join(parts, "\n")
			if source.FirstContext.ClaudeMd != "" && !strings.Contains(actual, source.FirstContext.ClaudeMd) {
				t.Errorf("complete Source user/managed/rules metadata differs: want %s; actual %.18000s", source.FirstContext.ClaudeMd, actual)
			}
			if source.InitialPrompt != nil && !strings.Contains(actual, *source.InitialPrompt) {
				t.Error("default original auto-memory prompt missing from actual request")
			}
			if profile == "bare" && (strings.Contains(actual, "ACTUAL_") || strings.Contains(actual, "# auto memory")) {
				t.Error("bare caller must skip actual instruction discovery and auto-memory prompt")
			}
			if profile == "managed-only" || profile == "user-disabled" {
				if strings.Contains(actual, "ACTUAL_USER_") {
					t.Error("original instruction source gate admitted User files")
				}
			}
			for _, file := range source.InitialFiles {
				path := file.Path
				if !filepath.IsAbs(path) {
					path = filepath.Join(source.Project, path)
				}
				body, err := os.ReadFile(path)
				if err != nil || string(body) != file.Content {
					t.Fatal("protected original instruction file changed", file.Path, err, string(body))
				}
			}
		})
	}
}
