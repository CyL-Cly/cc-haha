package application

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestPublicVerboseJSONUsesActualSharedHarnessAndPreservesSingleResultMode(t *testing.T) {
	for _, verbose := range []bool{false, true} {
		t.Run(map[bool]string{false: "single-result", true: "full-array"}[verbose], func(t *testing.T) {
			originalKinds := originalPublicJSONKinds(t, verbose)
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// The original CLI init HEAD is distinct from model POSTs.
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusNoContent)
					return
				}

				response(w, "verbose-json-physical", "", nil, "VERBOSE_JSON_PHYSICAL_DONE")
			}))
			defer peer.Close()
			o := nativeSessionOptions(t, peer.URL)
			args := []string{"--bare", "--print", "--model", "fixture-model", "--thinking", "disabled", "--system-prompt", "Actual verbose JSON CLI.", "--tools", "Read", "--output-format", "json"}
			if verbose {
				args = append(args, "--verbose")
			}
			args = append(args, "actual private response")
			command, _, err := ParseCommand(args, o.Version, o.WorkDir, filepath.Join(o.WorkDir, "tmp"))
			if err != nil {
				t.Fatal("original public syntax rejected before the shared Harness:", err)
			}
			var stdout bytes.Buffer
			code, err := RunCommand(context.Background(), command, o, bytes.NewReader(nil), &stdout, nil)
			if err != nil || code != 0 {
				t.Fatalf("code=%d err=%v stdout=%s", code, err, stdout.Bytes())
			}
			var result protocol.JSONObject
			var observedKinds []string
			if !verbose {
				err = result.UnmarshalJSON(stdout.Bytes())
				observedKinds = []string{result.String("type") + "/" + result.String("subtype")}
			} else {
				var messages []protocol.JSONObject
				if err = json.Unmarshal(stdout.Bytes(), &messages); err != nil || len(messages) < 3 {
					t.Fatalf("missing original full message array: %v %s", err, stdout.Bytes())
				}
				if messages[0].String("type") != "system" || messages[0].String("subtype") != "init" {
					t.Fatal("startup did not participate in the same collection", string(stdout.Bytes()))
				}
				assistant := false
				for _, message := range messages {
					observedKinds = append(observedKinds, message.String("type")+"/"+message.String("subtype"))
					if message.String("type") == "assistant" {
						assistant = true
					}
					if message.String("type") == "stream_event" || message.String("type") == "control_response" || message.String("subtype") == "session_state_changed" {
						t.Fatal("SDK-only event entered public JSON collection", message)
					}
				}
				if !assistant {
					t.Fatal("actual provider assistant was lost")
				}
				result = messages[len(messages)-1]
			}
			if !reflect.DeepEqual(observedKinds, originalKinds) {
				t.Fatalf("actual CLI output envelopes changed: Go%v original%v", observedKinds, originalKinds)
			}
			if err != nil || result.String("type") != "result" || result.String("subtype") != "success" || result.String("result") != "VERBOSE_JSON_PHYSICAL_DONE" {
				t.Fatalf("last result changed: %v %s", err, stdout.Bytes())
			}
			if stdout.Bytes()[stdout.Len()-1] != '\n' || bytes.Count(stdout.Bytes(), []byte{'\n'}) != 1 {
				t.Fatal("public JSON must be one newline-terminated document")
			}
		})
	}
}

// Reuse the frozen actual original Commander/MainPrint/QueryEngine fixture;
// its external managed/keychain/discovery primitives alone are isolated. The
// provider is a physical loopback peer and the receipt proves one real POST.
func originalPublicJSONKinds(t *testing.T, verbose bool) []string {
	t.Helper()
	args := []string{}
	if verbose {
		args = []string{"--verbose"}
	}
	stdout, stderr, code, calls := originalPublicJSONCLI(t, args)
	if code != 0 || calls != 1 || stderr != "" {
		t.Fatalf("original CLI did not query: code%d calls%d stderr%s", code, calls, stderr)
	}
	var messages []protocol.JSONObject
	var err error
	if verbose {
		err = json.Unmarshal([]byte(stdout), &messages)
	} else {
		var result protocol.JSONObject
		err = result.UnmarshalJSON([]byte(stdout))
		messages = []protocol.JSONObject{result}
	}
	if err != nil || len(messages) == 0 || messages[len(messages)-1].String("result") != "ACTUAL_DEBUG_DONE" {
		t.Fatalf("original CLI formatting failed: %v %s", err, stdout)
	}
	var kinds []string
	for _, message := range messages {
		kinds = append(kinds, message.String("type")+"/"+message.String("subtype"))
	}
	return kinds
}

func originalPublicJSONCLI(t *testing.T, args []string) (string, string, int, int) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(home, "tmp")
	if err := os.MkdirAll(tmp, 0700); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "arguments": args})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-debug-cli.fixture.ts")
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		bun = "bun"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + tmp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_DEBUG_CLI_FIXTURE=1", "CC_HAHA_DEBUG_CLI_INPUT=" + string(input), "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "CLAUDE_CODE_MAX_RETRIES=0", "CC_HAHA_DISABLE_TERMINAL_SHELL_ENV=1"}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	raw, err := os.ReadFile(filepath.Join(home, "original-debug-cli-receipt.json"))
	var receipt struct {
		Code         int
		Requests     []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err != nil || json.Unmarshal(raw, &receipt) != nil || len(receipt.Capabilities.Blocked) != 0 {
		t.Fatalf("original CLI escaped or did not query: %v receipt%s stderr%s", err, raw, stderr.Bytes())
	}
	return stdout.String(), stderr.String(), receipt.Code, len(receipt.Requests)
}

func TestPublicPrintFormatCombinationsRejectExactlyLikeActualOriginalCLI(t *testing.T) {
	for _, args := range [][]string{
		{"--include-partial-messages"},
		{"--replay-user-messages"},
		{"--input-format", "stream-json"},
		{"--output-format", "stream-json", "--verbose", "--replay-user-messages"},
	} {
		t.Run(args[0], func(t *testing.T) {
			stdout, stderr, code, calls := originalPublicJSONCLI(t, args)
			if code != 1 || calls != 0 || stdout != "" || stderr == "" {
				t.Fatalf("original rejection must precede provider: code%d calls%d stdout%q stderr%q", code, calls, stdout, stderr)
			}
			goArgs := append([]string{"--bare", "--print", "--output-format", "json"}, args...)
			goArgs = append(goArgs, "prompt")
			_, _, err := ParseCommand(goArgs, "fixture", t.TempDir(), t.TempDir())
			var actual bytes.Buffer
			if err == nil {
				t.Fatal("Go accepted the original invalid format combination")
			}
			if e := WriteCommandError(&actual, err); e != nil || actual.String() != stderr {
				t.Fatalf("Go accepted or changed original format rejection: error%v original%q", err, stderr)
			}
		})
	}
}
