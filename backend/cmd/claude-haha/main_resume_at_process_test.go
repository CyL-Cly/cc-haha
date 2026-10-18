package main

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
	"sync/atomic"
	"testing"
	"time"
)

// The actual Source and Go public loader must accept a persisted boundary even
// when resume filtering removes that boundary itself. Neither process executes
// the pending call, and both send only the preceding conversation to the model.
func TestActualSourceAndPublicCLIResumeAtFilteredBoundary(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	binary := filepath.Join(root, "actual-resume-at-cli")
	build := exec.CommandContext(ctx, "go", "build", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	capturePublicGoCoverage(t, build)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	fixture, _ := filepath.Abs("../../testdata/native-cli-resume-errors.fixture.ts")
	for _, profile := range []string{"pending-tool", "malformed-attachment"} {
		t.Run(profile, func(t *testing.T) {
			sourceHome := resumeErrorHome(t, root, "source-"+profile)
			goHome := resumeErrorHome(t, root, "go-"+profile)
			originalFiles := map[string][]byte{}
			for _, home := range []string{sourceHome, goHome} {
				path := filepath.Join(home, "valid.jsonl")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				boundary := map[string]any{"type": "assistant", "uuid": "filtered-boundary", "parentUuid": "a1", "sessionId": resumeErrorRequestedSID, "timestamp": "2026-10-01T00:00:02Z", "message": map[string]any{"id": "pending-response", "role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "pending-call", "name": "Bash", "input": map[string]any{"command": "SHOULD_NEVER_EXECUTE"}}}}}
				if profile == "malformed-attachment" {
					boundary = map[string]any{"type": "attachment", "uuid": "filtered-boundary", "parentUuid": "a1", "sessionId": resumeErrorRequestedSID, "timestamp": "2026-10-01T00:00:02Z", "attachment": map[string]any{"type": "file", "filename": false}}
				}
				tail := map[string]any{"type": "user", "uuid": "after-boundary", "parentUuid": "filtered-boundary", "sessionId": resumeErrorRequestedSID, "timestamp": "2026-10-01T00:00:03Z", "message": map[string]any{"role": "user", "content": "TEXT_AFTER_CUT_MUST_NOT_SURVIVE"}}
				for _, row := range []map[string]any{boundary, tail} {
					raw, _ := json.Marshal(row)
					data = append(data, raw...)
					data = append(data, '\n')
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				originalFiles[path] = data
			}
			args := func(home string) []string {
				return []string{"--bare", "--print", "--model", "fixture-model", "--thinking", "disabled", "--no-computer-use", "--session-id", resumeErrorInitialSID, "--output-format", "json", "--tools", "", "--system-prompt", "Private original resume boundary.", "--fork-session", "--resume", filepath.Join(home, "valid.jsonl"), "--resume-session-at", "filtered-boundary", "SIDE_QUESTION_AFTER_ORIGINAL_BOUNDARY"}
			}
			assertRequest := func(raw []byte) {
				var request struct{ Messages json.RawMessage }
				if err := json.Unmarshal(raw, &request); err != nil {
					t.Error(err)
					return
				}
				for _, text := range []string{"old answer", `"old"`, "SIDE_QUESTION_AFTER_ORIGINAL_BOUNDARY"} {
					if !bytes.Contains(request.Messages, []byte(text)) {
						t.Errorf("resume prefix lost %q: %s", text, request.Messages)
					}
				}
				for _, text := range []string{"pending-call", "SHOULD_NEVER_EXECUTE", "TEXT_AFTER_CUT_MUST_NOT_SURVIVE"} {
					if bytes.Contains(request.Messages, []byte(text)) {
						t.Errorf("filtered or later history survived %q: %s", text, request.Messages)
					}
				}
			}
			var calls atomic.Int32
			peer := httptest.NewServer(nativeCLIModelHTTPHandler(t, func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				calls.Add(1)
				assertRequest(body)
				jsonSchemaCLIResponse(w, "normal-resume-response", "", nil, "PUBLIC_RESUME_NORMAL_OK")
			}))
			defer peer.Close()
			input, _ := json.Marshal(map[string]any{"homeDir": sourceHome, "args": args(sourceHome), "success": true})
			source := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			source.Dir = sourceHome
			source.Env = append(resumeErrorEnv(sourceHome, root, peer.URL), "PATH=/usr/bin:/bin", "CC_HAHA_RESUME_ERROR_FIXTURE=1", "CC_HAHA_RESUME_ERROR_INPUT="+string(input))
			sc, so, se := resumeErrorRun(t, source, false)
			receipt, err := os.ReadFile(filepath.Join(sourceHome, "source-resume-error-receipt.json"))
			if err != nil {
				t.Fatal(err, string(so), string(se))
			}
			var original struct {
				Requests     []json.RawMessage
				Capabilities struct{ Blocked []string }
			}
			if err := json.Unmarshal(receipt, &original); err != nil || len(original.Requests) != 1 || len(original.Capabilities.Blocked) != 0 {
				t.Fatal(err, string(receipt), string(se))
			}
			assertRequest(original.Requests[0])
			actual := exec.CommandContext(ctx, binary, args(goHome)...)
			actual.Dir = goHome
			actual.Env = resumeErrorEnv(goHome, root, peer.URL)
			gc, goOut, goErr := resumeErrorRun(t, actual, false)
			for _, observed := range []struct {
				code        int
				out, stderr []byte
			}{{sc, so, se}, {gc, goOut, goErr}} {
				var result map[string]any
				if err := json.Unmarshal(observed.out, &result); err != nil || observed.code != 0 || len(observed.stderr) != 0 || result["subtype"] != "success" || result["result"] != "PUBLIC_RESUME_NORMAL_OK" || result["session_id"] != resumeErrorInitialSID {
					t.Fatalf("actual filtered-boundary resume code%d jsonerr%v stdout%s stderr%s", observed.code, err, observed.out, observed.stderr)
				}
			}
			if calls.Load() != 1 {
				t.Fatal("actual Go public model request count", calls.Load())
			}
			for path, before := range originalFiles {
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("fork mutated parent transcript", path, err)
				}
			}
		})
	}
}
