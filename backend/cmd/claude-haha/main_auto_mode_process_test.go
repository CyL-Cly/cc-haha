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
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliargs"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

func autoCommandSignedSystem(t *testing.T, request json.RawMessage, body map[string]any) any {
	t.Helper()
	// Every fresh native process has a distinct device/session identity. Verify
	// the actual whole-body checksum before comparing the rest of the system;
	// retaining a checksum from another request must fail this consumer.
	pattern := regexp.MustCompile(`\bcch=[a-f0-9]{5};`)
	matches := pattern.FindAllIndex(request, -1)
	if len(matches) != 1 {
		t.Fatal("standalone action lacks a unique actual billing checksum")
	}
	unsigned := pattern.ReplaceAll(request, []byte("cch=00000;"))
	if provider.SignClaudeCodeCCHInString(string(unsigned)) != string(request) {
		t.Fatal("standalone action billing checksum does not match its physical body")
	}
	system, ok := body["system"].([]any)
	if !ok || len(system) < 2 {
		t.Fatal("standalone action system shape")
	}
	first, ok := system[0].(map[string]any)
	if !ok {
		t.Fatal("standalone action billing block shape")
	}
	text, ok := first["text"].(string)
	if !ok || !strings.HasPrefix(text, "x-anthropic-billing-header:") || len(pattern.FindAllStringIndex(text, -1)) != 1 {
		t.Fatal("standalone action checksum outside billing block")
	}
	first["text"] = pattern.ReplaceAllString(text, "cch=00000;")
	return system
}

func TestActualSourceAndPublicAutoModeCommands(t *testing.T) {
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
	binary := filepath.Join(root, "actual-auto-mode-cli")
	build := exec.CommandContext(ctx, "go", "build", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	capturePublicGoCoverage(t, build)
	if data, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(data))
	}
	fixture, _ := filepath.Abs("../../testdata/native-auto-mode-cli.fixture.ts")
	for _, profile := range []struct {
		name, operation string
		settings        string
		status          int
		content         []map[string]any
		calls           int
	}{
		{"defaults", "defaults", `{"autoMode":{"allow":[],"environment":["private"]},"future":"unchanged"}`, 200, nil, 0},
		{"trusted-config", "config", `{"autoMode":{"allow":["$defaults","read private"],"soft_deny":[],"hard_deny":["never erase"],"environment":["isolated"]},"future":"unchanged"}`, 200, nil, 0},
		{"no-custom", "critique", `{"autoMode":{"classifyAllShell":true},"future":"unchanged"}`, 200, nil, 0},
		{"custom-empty", "critique", `{"autoMode":{"allow":[]},"autoMemoryEnabled":false,"future":"unchanged"}`, 200, nil, 1},
		{"first-text", "critique", `{"autoMode":{"allow":["$defaults","private write"],"hard_deny":["Never erase anything"]},"future":"unchanged"}`, 200, []map[string]any{{"type": "thinking", "thinking": "hidden", "signature": "fake"}, {"type": "text", "text": "FIRST_TEXT_ONLY"}, {"type": "text", "text": "SECOND_TEXT_NOT_PRINTED"}}, 1},
		{"no-text", "critique", `{"autoMode":{"environment":["private"]},"autoMemoryEnabled":true,"future":"unchanged"}`, 200, []map[string]any{}, 1},
		{"auth-rejection", "critique", `{"autoMode":{"hard_deny":[]},"future":"unchanged"}`, 401, nil, 1},
	} {
		t.Run(profile.name, func(t *testing.T) {
			makeHome := func(name string) string {
				home := filepath.Join(root, name+"-"+profile.name)
				for _, dir := range []string{home, filepath.Join(home, ".claude"), filepath.Join(home, "tmp")} {
					if err := os.MkdirAll(dir, 0700); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(home, ".claude/settings.json"), []byte(profile.settings), 0600); err != nil {
					t.Fatal(err)
				}
				return home
			}
			sourceHome, goHome, nativeHome := makeHome("source"), makeHome("go"), makeHome("native")
			args := []string{"auto-mode", profile.operation}
			if profile.operation == "critique" {
				args = append(args, "--model", "fixture-model")
			}
			input, _ := json.Marshal(map[string]any{"homeDir": sourceHome, "args": args, "status": profile.status, "content": profile.content})
			source := exec.CommandContext(ctx, bun, "--no-env-file", "--feature=TRANSCRIPT_CLASSIFIER", fixture)
			source.Dir = sourceHome
			source.Env = append(resumeErrorEnv(sourceHome, root, "http://127.0.0.1:9"), "PATH=/usr/bin:/bin", "CC_HAHA_AUTO_COMMAND_CLI_FIXTURE=1", "CC_HAHA_AUTO_COMMAND_INPUT="+string(input))
			sc, so, se := resumeErrorRun(t, source, false)
			data, err := os.ReadFile(filepath.Join(sourceHome, "source-auto-command-receipt.json"))
			if err != nil {
				t.Fatal(err, string(so), string(se))
			}
			var original struct {
				Requests     []json.RawMessage
				Capabilities struct{ Blocked []string }
			}
			if err = json.Unmarshal(data, &original); err != nil || len(original.Requests) != profile.calls || len(original.Capabilities.Blocked) != 0 {
				t.Fatal("original command receipt", err, string(data), string(se))
			}
			var mu sync.Mutex
			var requests []json.RawMessage
			peer := httptest.NewServer(nativeCLIModelHTTPHandler(t, func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				requests = append(requests, body)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(profile.status)
				if profile.status != 200 {
					_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"private credential rejected"}}`)
					return
				}
				content := profile.content
				if content == nil {
					content = []map[string]any{{"type": "text", "text": "PUBLIC_AUTO_RULES_CRITIQUE_OK"}}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "actual-auto-critique-response", "type": "message", "role": "assistant", "model": "fixture-model", "content": content, "stop_reason": "end_turn", "stop_sequence": nil, "usage": map[string]any{"input_tokens": 10, "output_tokens": 5}})
			}))
			defer peer.Close()
			actual := exec.CommandContext(ctx, binary, args...)
			actual.Dir = goHome
			actual.Env = resumeErrorEnv(goHome, root, peer.URL)
			gc, goOut, goErr := resumeErrorRun(t, actual, false)
			if gc != sc || !bytes.Equal(goOut, so) || !bytes.Equal(goErr, se) {
				t.Fatalf("actual command differs Go(%d,%q,%q) Source(%d,%q,%q)", gc, goOut, goErr, sc, so, se)
			}
			mu.Lock()
			got := append([]json.RawMessage{}, requests...)
			mu.Unlock()
			if len(got) != profile.calls {
				t.Fatal("physical request count", len(got), profile.calls)
			}
			for i := range got {
				var a, b map[string]any
				_ = json.Unmarshal(got[i], &a)
				_ = json.Unmarshal(original.Requests[i], &b)
				for _, key := range []string{"model", "messages", "max_tokens", "tools", "tool_choice", "stream", "thinking"} {
					if !reflect.DeepEqual(a[key], b[key]) {
						t.Errorf("actual request %s differs Go=%v Source=%v", key, a[key], b[key])
					}
				}
				for _, m := range []map[string]any{a, b} {
					blocks, _ := m["system"].([]any)
					if len(blocks) == 0 || !strings.HasPrefix(blocks[len(blocks)-1].(map[string]any)["text"].(string), "You are an expert reviewer of auto mode classifier rules") {
						t.Error("actual critique system not used", m["system"])
					}
				}
			}
			// Keep the physical compiled entry above. This second fresh action
			// also records the original consumers in the normal Go test profile.
			mu.Lock()
			requests = nil
			mu.Unlock()
			native := nativeCommandConsumerBoundary(t, root, nativeHome, resumeErrorEnv(nativeHome, root, peer.URL))
			parsed := cliargs.Parse(args, cliargs.Config{Version: version})
			if parsed.Parsed == nil {
				t.Fatal("native public command parse", parsed)
			}
			var nativeOut, nativeErr bytes.Buffer
			nativeCode := runNativeAutoModeCommand(ctx, args, parsed.Parsed, native, &nativeOut, &nativeErr)
			if nativeCode != sc || !bytes.Equal(nativeOut.Bytes(), so) || !bytes.Equal(nativeErr.Bytes(), se) {
				t.Fatalf("same public action differs Native(%d,%q,%q) Source(%d,%q,%q)", nativeCode, nativeOut.String(), nativeErr.String(), sc, so, se)
			}
			mu.Lock()
			nativeRequests := append([]json.RawMessage{}, requests...)
			mu.Unlock()
			if len(nativeRequests) != len(got) {
				t.Fatal("native action physical request count", len(nativeRequests), len(got))
			}
			for i := range got {
				var physical, inProcess map[string]any
				if json.Unmarshal(got[i], &physical) != nil || json.Unmarshal(nativeRequests[i], &inProcess) != nil {
					t.Fatal("native action request JSON")
				}
				for _, key := range []string{"model", "messages", "max_tokens", "tools", "tool_choice", "stream", "thinking"} {
					if !reflect.DeepEqual(physical[key], inProcess[key]) {
						t.Errorf("native action request %s differs Physical=%v Native=%v", key, physical[key], inProcess[key])
					}
				}
				if !reflect.DeepEqual(autoCommandSignedSystem(t, got[i], physical), autoCommandSignedSystem(t, nativeRequests[i], inProcess)) {
					t.Error("native action system differs after verifying each physical checksum")
				}
			}
			for _, home := range []string{goHome, sourceHome, nativeHome} {
				after, err := os.ReadFile(filepath.Join(home, ".claude/settings.json"))
				if err != nil || string(after) != profile.settings {
					t.Fatal("inspection action mutated user rules", err)
				}
				if entries, err := os.ReadDir(filepath.Join(home, ".claude/projects")); err != nil && !os.IsNotExist(err) || len(entries) != 0 {
					t.Fatal("standalone auto-mode action created ASK transcript", entries, err)
				}
			}
		})
	}
}
