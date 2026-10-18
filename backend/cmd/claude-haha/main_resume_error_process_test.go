package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const resumeErrorInitialSID = "10101010-1010-4010-8010-101010101010"
const resumeErrorRequestedSID = "20202020-2020-4020-8020-202020202020"

var resumeErrorUUID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func resumeErrorArgs(home, profile, format string) []string {
	a := []string{"--bare", "--print", "--model", "fixture-model", "--thinking", "disabled", "--no-computer-use", "--session-id", resumeErrorInitialSID, "--fork-session", "--output-format", format}
	if format == "stream-json" && profile != "stream-no-verbose" && profile != "loaded-nonverbose" && profile != "loaded-sdk-nonverbose" {
		a = append(a, "--verbose")
	}
	switch profile {
	case "missing-uuid", "stream-no-verbose", "missing-uuid-verbose", "generated-initial-sid", "prefix-cli", "prefix-agent":
		a = append(a, "--resume", resumeErrorRequestedSID)
	case "uuid-final-lf":
		a = append(a, "--resume", resumeErrorRequestedSID+"\n")
	case "missing-uuid-uppercase":
		a = append(a, "--resume", "ABCDEF12-1234-ABCD-EFAB-ABCDEF123456")
	case "invalid-unicode":
		a = append(a, "--resume", "invalid-☃-😀")
	case "jsonl-trailing-space":
		a = append(a, "--resume", filepath.Join(home, "empty-jsonl.JSONL "))
	case "empty-jsonl", "missing-jsonl", "malformed-jsonl", "reducer-error":
		a = append(a, "--resume", filepath.Join(home, profile+".JSONL"))
	case "loaded-nonverbose":
		a = append(a, "--resume", filepath.Join(home, "valid.jsonl"))
	case "loaded-sdk-nonverbose":
		return append(a, "--resume", filepath.Join(home, "valid.jsonl"), "--input-format", "stream-json")
	case "missing-at":
		a = append(a, "--resume", filepath.Join(home, "valid.jsonl"), "--resume-session-at", "missing-message")
	case "invalid-uuid":
		a = append(a, "--resume", "invalid-value")
	case "empty-resume-value":
		a = append(a, "--resume", "")
	case "bare-resume":
		return append(a, "--resume")
	case "at-without-resume":
		a = append(a, "--resume-session-at", "missing-message")
	case "sdk-input":
		return append(a, "--resume", resumeErrorRequestedSID, "--input-format", "stream-json")
	}
	if profile == "generated-initial-sid" {
		for i := range a {
			if a[i] == "--session-id" {
				a = append(a[:i], a[i+2:]...)
				break
			}
		}
	}
	if profile == "missing-uuid-verbose" {
		a = append(a, "--verbose")
	}
	return append(a, "Unconsumed init-error prompt")
}
func resumeErrorHome(t *testing.T, root, name string) string {
	t.Helper()
	h := filepath.Join(root, name)
	for _, p := range []string{h, filepath.Join(h, ".claude"), filepath.Join(h, "tmp")} {
		if e := os.MkdirAll(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	for p, data := range map[string]string{"empty-jsonl.JSONL": "", "malformed-jsonl.JSONL": "bad JSON\n", "valid.jsonl": `{"type":"user","uuid":"u1","parentUuid":null,"sessionId":"` + resumeErrorRequestedSID + `","timestamp":"2026-10-01T00:00:00Z","message":{"role":"user","content":"old"}}` + "\n" + `{"type":"assistant","uuid":"a1","parentUuid":"u1","sessionId":"` + resumeErrorRequestedSID + `","timestamp":"2026-10-01T00:00:01Z","message":{"role":"assistant","id":"a1","content":[{"type":"text","text":"old answer"}]}}` + "\n", "reducer-error.JSONL": `{"type":"user","uuid":"bad","parentUuid":null,"sessionId":"` + resumeErrorRequestedSID + `","timestamp":"2026-10-01T00:00:00Z"}` + "\n", ".claude/settings.json": `{"disableAllHooks":true}`} {
		if e := os.WriteFile(filepath.Join(h, p), []byte(data), 0600); e != nil {
			t.Fatal(e)
		}
	}
	return h
}
func resumeErrorEnv(home, root, origin string) []string {
	return []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_NATIVE_FIXTURE_ROOT=" + root, "CC_HAHA_FIXTURE_TEMP_ROOT=" + root, "PATH=" + os.Getenv("PATH"), "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "CLAUDE_CODE_MAX_RETRIES=0", "CC_HAHA_DISABLE_TERMINAL_SHELL_ENV=1", "ANTHROPIC_API_KEY=private-resume-fake", "ANTHROPIC_BASE_URL=" + origin, "BUN_OPTIONS=--no-env-file"}
}
func resumeErrorRun(t *testing.T, cmd *exec.Cmd, holdInput bool) (int, []byte, []byte) {
	t.Helper()
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	var input io.WriteCloser
	var e error
	if holdInput {
		input, e = cmd.StdinPipe()
		if e != nil {
			t.Fatal(e)
		}
		defer input.Close()
	}
	capturePublicGoCoverage(t, cmd)
	e = cmd.Run()
	code := 0
	if e != nil {
		var exit *exec.ExitError
		if !errors.As(e, &exit) {
			t.Fatal(e, stderr.String())
		}
		code = exit.ExitCode()
	}
	return code, out.Bytes(), stderr.Bytes()
}
func normalizeResumeError(t *testing.T, home, profile, format string, out, stderr []byte) (any, string) {
	t.Helper()
	text := strings.ReplaceAll(string(stderr), home, "<HOME>")
	if format != "stream-json" {
		if len(out) != 0 {
			t.Errorf("load failure wrote nonstream stdout: %s", out)
		}
		return nil, text
	}
	if len(stderr) != 0 {
		t.Errorf("stream load failure wrote stderr: %s", stderr)
	}
	if bytes.Count(out, []byte("\n")) != 1 {
		t.Fatalf("expected exactly one load result: %s", out)
	}
	var result map[string]any
	if e := json.Unmarshal(out, &result); e != nil {
		t.Fatal(e, string(out), text)
	}
	if list, ok := result["errors"].([]any); ok {
		for i, v := range list {
			if message, ok := v.(string); ok {
				list[i] = strings.ReplaceAll(message, home, "<HOME>")
			}
		}
	}
	id, _ := result["uuid"].(string)
	if !resumeErrorUUID.MatchString(id) {
		t.Fatal("invalid result UUID", result)
	}
	result["uuid"] = "<UUID>"
	if profile == "generated-initial-sid" {
		id, _ := result["session_id"].(string)
		if !resumeErrorUUID.MatchString(id) || id == resumeErrorRequestedSID {
			t.Fatal("load failure did not keep fresh SID", result)
		}
		result["session_id"] = "<INITIAL_UUID>"
	} else if result["session_id"] != resumeErrorInitialSID {
		t.Fatal("load failure switched SID", result)
	}
	if profile == "empty-jsonl" || profile == "malformed-jsonl" || profile == "missing-jsonl" {
		list, ok := result["errors"].([]any)
		if ok && len(list) == 1 {
			m, _ := list[0].(string)
			prefix := "No conversation found with session ID: "
			if strings.HasPrefix(m, prefix) && resumeErrorUUID.MatchString(strings.TrimPrefix(m, prefix)) {
				result["errors"] = []any{prefix + "<PARSED_JSONL_UUID>"}
			}
		}
	}
	return result, text
}

// Actual Source main/print loader and actual Go public CLI run with ordinary
// private files. The Go stdin pipe stays open: failure must precede input reads.
func TestActualSourceAndPublicCLIResumeInitializationFailures(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	binary := filepath.Join(root, "actual-resume-cli")
	build := exec.CommandContext(ctx, "go", "build", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	capturePublicGoCoverage(t, build)
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	var calls atomic.Int32
	peer := httptest.NewServer(nativeCLIModelHTTPHandler(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "initialization must not call a model", 500)
	}))
	defer peer.Close()
	fixture, _ := filepath.Abs("../../testdata/native-cli-resume-errors.fixture.ts")
	for _, row := range []struct{ profile, format string }{{"missing-uuid", "text"}, {"missing-uuid", "json"}, {"missing-uuid", "stream-json"}, {"missing-uuid-verbose", "json"}, {"missing-uuid-uppercase", "stream-json"}, {"uuid-final-lf", "stream-json"}, {"generated-initial-sid", "stream-json"}, {"prefix-cli", "stream-json"}, {"prefix-agent", "stream-json"}, {"invalid-unicode", "stream-json"}, {"jsonl-trailing-space", "stream-json"}, {"stream-no-verbose", "stream-json"}, {"loaded-nonverbose", "stream-json"}, {"loaded-sdk-nonverbose", "stream-json"}, {"empty-resume-value", "stream-json"}, {"sdk-input", "stream-json"}, {"empty-jsonl", "stream-json"}, {"missing-jsonl", "stream-json"}, {"malformed-jsonl", "stream-json"}, {"missing-at", "stream-json"}, {"reducer-error", "stream-json"}, {"invalid-uuid", "stream-json"}, {"bare-resume", "stream-json"}, {"at-without-resume", "text"}} {
		t.Run(row.profile+"/"+row.format, func(t *testing.T) {
			sourceHome := resumeErrorHome(t, root, "source-"+row.profile+"-"+row.format)
			goHome := resumeErrorHome(t, root, "go-"+row.profile+"-"+row.format)
			originalFiles := map[string][]byte{}
			for _, home := range []string{sourceHome, goHome} {
				for _, name := range []string{"valid.jsonl", "empty-jsonl.JSONL", "malformed-jsonl.JSONL", "reducer-error.JSONL"} {
					path := filepath.Join(home, name)
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					originalFiles[path] = data
				}
			}
			data, _ := json.Marshal(map[string]any{"homeDir": sourceHome, "args": resumeErrorArgs(sourceHome, row.profile, row.format)})
			source := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			source.Dir = sourceHome
			source.Env = append(resumeErrorEnv(sourceHome, root, peer.URL), "PATH=/usr/bin:/bin", "CC_HAHA_RESUME_ERROR_FIXTURE=1", "CC_HAHA_RESUME_ERROR_INPUT="+string(data))
			sc, so, se := resumeErrorRun(t, source, false)
			receipt, e := os.ReadFile(filepath.Join(sourceHome, "source-resume-error-receipt.json"))
			if e != nil {
				t.Fatal(e, string(se), string(so))
			}
			var original struct {
				Requests     []json.RawMessage
				Capabilities struct{ Blocked []string }
			}
			if e = json.Unmarshal(receipt, &original); e != nil || len(original.Requests) != 0 || len(original.Capabilities.Blocked) != 0 {
				t.Fatal(e, string(receipt), string(se))
			}
			actualArgs := resumeErrorArgs(goHome, row.profile, row.format)
			if row.profile == "prefix-cli" {
				actualArgs = append([]string{"cli", "--app-root", root}, actualArgs...)
			}
			if row.profile == "prefix-agent" {
				actualArgs = append([]string{"agent", "--app-root", root}, actualArgs...)
			}
			actual := exec.CommandContext(ctx, binary, actualArgs...)
			actual.Dir = goHome
			actual.Env = resumeErrorEnv(goHome, root, peer.URL)
			gc, goOut, goErr := resumeErrorRun(t, actual, true)
			if sc != 1 || gc != 1 {
				t.Fatalf("source exit%d Go exit%d source %s/%s Go %s/%s", sc, gc, so, se, goOut, goErr)
			}
			normalizedFormat := row.format
			if row.profile == "at-without-resume" || row.profile == "loaded-nonverbose" || row.profile == "loaded-sdk-nonverbose" {
				normalizedFormat = "text"
			}
			want, ws := normalizeResumeError(t, sourceHome, row.profile, normalizedFormat, so, se)
			got, gs := normalizeResumeError(t, goHome, row.profile, normalizedFormat, goOut, goErr)
			w, _ := json.Marshal(want)
			g, _ := json.Marshal(got)
			if !bytes.Equal(w, g) || ws != gs {
				t.Fatalf("actual loader/public output differs\nSource %s stderr%q\nGo %s stderr%q", w, ws, g, gs)
			}
			if calls.Load() != 0 {
				t.Fatal("public init entered provider", calls.Load())
			}
			for path, before := range originalFiles {
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("resume initialization changed original transcript bytes", path, err)
				}
			}
		})
	}
}

// The new public error binding must retain the actual ordinary query path and
// successful loaded messages. Both original and Go use their normal MainPrint.
func TestActualSourceAndPublicCLIResumeInitializationNormalSuccess(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	binary := filepath.Join(root, "actual-success-cli")
	build := exec.CommandContext(ctx, "go", "build", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	capturePublicGoCoverage(t, build)
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	fixture, _ := filepath.Abs("../../testdata/native-cli-resume-errors.fixture.ts")
	for _, resume := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "resume"}[resume], func(t *testing.T) {
			sourceHome := resumeErrorHome(t, root, map[bool]string{false: "source-fresh", true: "source-resume"}[resume])
			goHome := resumeErrorHome(t, root, map[bool]string{false: "go-fresh", true: "go-resume"}[resume])
			originalFiles := map[string][]byte{}
			for _, home := range []string{sourceHome, goHome} {
				path := filepath.Join(home, "valid.jsonl")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				originalFiles[path] = data
			}
			args := func(home string) []string {
				a := []string{"--bare", "--print", "--model", "fixture-model", "--thinking", "disabled", "--no-computer-use", "--session-id", resumeErrorInitialSID, "--output-format", "json", "--tools", "", "--system-prompt", "Private original resume success boundary."}
				if resume {
					a = append(a, "--fork-session", "--resume", filepath.Join(home, "valid.jsonl"))
				}
				return append(a, "Actual normal user instruction")
			}
			var calls atomic.Int32
			peer := httptest.NewServer(nativeCLIModelHTTPHandler(t, func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				calls.Add(1)
				if !bytes.Contains(body, []byte("Actual normal user instruction")) || resume && (!bytes.Contains(body, []byte("old answer")) || !bytes.Contains(body, []byte(`"old"`))) {
					t.Errorf("normal resume request lost original conversation %s", body)
				}
				jsonSchemaCLIResponse(w, "normal-resume-response", "", nil, "PUBLIC_RESUME_NORMAL_OK")
			}))
			defer peer.Close()
			input, _ := json.Marshal(map[string]any{"homeDir": sourceHome, "args": args(sourceHome), "success": true})
			source := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			source.Dir = sourceHome
			source.Env = append(resumeErrorEnv(sourceHome, root, peer.URL), "PATH=/usr/bin:/bin", "CC_HAHA_RESUME_ERROR_FIXTURE=1", "CC_HAHA_RESUME_ERROR_INPUT="+string(input))
			sc, so, se := resumeErrorRun(t, source, false)
			receipt, e := os.ReadFile(filepath.Join(sourceHome, "source-resume-error-receipt.json"))
			if e != nil {
				t.Fatal(e, string(se))
			}
			var original struct {
				Requests     []json.RawMessage
				Capabilities struct{ Blocked []string }
			}
			if e = json.Unmarshal(receipt, &original); e != nil || len(original.Requests) != 1 || len(original.Capabilities.Blocked) != 0 {
				t.Fatal(e, string(receipt), string(se))
			}
			actual := exec.CommandContext(ctx, binary, args(goHome)...)
			actual.Dir = goHome
			actual.Env = resumeErrorEnv(goHome, root, peer.URL)
			gc, goOut, goErr := resumeErrorRun(t, actual, false)
			for _, observed := range []struct {
				code        int
				out, stderr []byte
			}{{sc, so, se}, {gc, goOut, goErr}} {
				var result map[string]any
				if e = json.Unmarshal(observed.out, &result); e != nil || observed.code != 0 || len(observed.stderr) != 0 || result["subtype"] != "success" || result["result"] != "PUBLIC_RESUME_NORMAL_OK" || result["session_id"] != resumeErrorInitialSID {
					t.Fatalf("ordinary success changed code%d jsonerr%v %s stderr%s", observed.code, e, observed.out, observed.stderr)
				}
			}
			if calls.Load() != 1 {
				t.Fatal("actual normal query count", calls.Load())
			}
			for path, before := range originalFiles {
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("ordinary query/fork changed original transcript bytes", path, err)
				}
			}
		})
	}
}
