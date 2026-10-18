package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
)

func TestActualPublicCLIRawConfigLaunchConsumesOriginalPhysicalSettings(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, tmp := filepath.Join(root, "home"), filepath.Join(root, "home", "tmp")
	if err := os.MkdirAll(tmp, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-config-launch-environment.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	source := exec.CommandContext(ctx, bun, "--feature=TRANSCRIPT_CLASSIFIER", "--no-env-file", fixture)
	source.Dir = home
	source.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + tmp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + root, "CC_HAHA_CONFIG_LAUNCH_SOURCE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	source.Stdin = strings.NewReader(fmt.Sprintf(`{"home":%q}`, home))
	output, err := source.CombinedOutput()
	var original struct {
		Rows []struct {
			Name, CWD, Physical, SettingsPath, Content string
			Directory                                  *string
		}
		Caps struct{ Blocked, SecureReads []json.RawMessage }
	}
	if err != nil || json.Unmarshal(output, &original) != nil || len(original.Rows) != 6 || len(original.Caps.Blocked) != 0 || len(original.Caps.SecureReads) != 0 {
		t.Fatalf("actual original launch settings: %v %.6500s", err, output)
	}
	binary := filepath.Join(root, "public-config-launch-cli")
	build := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	capturePublicGoCoverage(t, build)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	for index, row := range original.Rows {
		t.Run(row.Name, func(t *testing.T) {
			var calls atomic.Int32
			peer := httptest.NewServer(nativeCLIModelHTTPHandler(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/messages" || r.Header.Get("x-config-launch") != row.Name {
					t.Errorf("actual request used another configuration: %s header=%q want=%q", r.URL.Path, r.Header.Get("x-config-launch"), row.Name)
				}
				body, _ := io.ReadAll(r.Body)
				if !bytes.Contains(body, []byte("RAW_LAUNCH_"+row.Name)) {
					t.Error("actual request lost the human prompt", string(body))
				}
				calls.Add(1)
				jsonSchemaCLIResponse(w, "actual-config-launch", "", nil, "ACTUAL_RAW_CONFIG_DONE")
			}))
			defer peer.Close()
			sid := fmt.Sprintf("61616161-6161-4161-8161-%012d", index+1)
			command := exec.CommandContext(ctx, binary, "--bare", "--print", "--no-computer-use", "--tools", "Read", "--model", "fixture-model", "--thinking", "disabled", "--system-prompt", "Actual raw configuration launch.", "--session-id", sid, "RAW_LAUNCH_"+row.Name)
			command.Dir = row.CWD
			command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "TMPDIR=" + tmp, "CC_HAHA_NATIVE_FIXTURE_ROOT=" + root, "PATH=" + os.Getenv("PATH"), "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "ANTHROPIC_API_KEY=private-config-launch-fake", "ANTHROPIC_BASE_URL=" + peer.URL, "CLAUDE_CODE_MAX_RETRIES=0"}
			if row.Directory != nil {
				command.Env = append(command.Env, "CLAUDE_CONFIG_DIR="+*row.Directory)
			}
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			capturePublicGoCoverage(t, command)
			err := command.Run()
			if err != nil || calls.Load() != 1 || stderr.Len() != 0 || strings.TrimSpace(stdout.String()) != "ACTUAL_RAW_CONFIG_DONE" {
				t.Fatalf("public raw launch: %v calls%d stdout%q stderr%q", err, calls.Load(), stdout.Bytes(), stderr.Bytes())
			}
			data, err := os.ReadFile(row.SettingsPath)
			if err != nil || string(data) != row.Content {
				t.Fatalf("public CLI rewrote original settings: %v %s", err, data)
			}
			path := filepath.Join(row.Physical, "projects", projectpath.Sanitize(row.CWD), sid+".jsonl")
			transcript, err := os.ReadFile(path)
			if err != nil || bytes.Count(transcript, []byte(`"type":"user"`)) != 1 || bytes.Count(transcript, []byte(`"type":"assistant"`)) != 1 || !bytes.Contains(transcript, []byte("ACTUAL_RAW_CONFIG_DONE")) {
				t.Fatalf("public CLI wrote another transcript root: %v %s", err, transcript)
			}
		})
	}
}
