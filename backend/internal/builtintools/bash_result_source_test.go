package builtintools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolerrors"
)

func TestNativeBashExitSemanticsErrorFormattingAndMappedOutputMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	config, temp := filepath.Join(home, ".claude"), filepath.Join(home, "tmp")
	for _, path := range []string{config, temp} {
		if e := os.MkdirAll(path, 0700); e != nil {
			t.Fatal(e)
		}
	}
	type commandRow struct {
		Command string `json:"command"`
		Code    int    `json:"code"`
	}
	commands := []commandRow{}
	for _, command := range []string{"", "true", "grep absent file", "rg absent", "find .", "diff a b", "test x = y", "[ x = y ]", "'grep' absent", "/usr/bin/grep absent", "echo ignored | grep absent", "echo ignored; test x", "echo ignored\ngrep absent", "grep absent > output.txt", "grep absent 2>/dev/null", "grep absent > /dev/null 2>&1", "grep absent > $OUT", "echo '${}' ; grep absent", "cat <<EOF\nhello\nEOF\ngrep absent", "echo \\\ngrep absent", "echo \\\\\ngrep absent", "echo '#comment'; grep absent", "grep absent # tail comment", "echo \";\" | grep absent", "echo && [ x ]", "grep absent &", "grep absent < input"} {
		for _, code := range []int{-1, 0, 1, 2, 7} {
			commands = append(commands, commandRow{command, code})
		}
	}
	type errorRow struct {
		Stdout      string `json:"stdout"`
		Stderr      string `json:"stderr"`
		Code        int    `json:"code"`
		Interrupted bool   `json:"interrupted"`
	}
	errorsIn := []errorRow{{"", "", 7, false}, {" out ", " err ", 2, false}, {"", "failed\n", 9, true}, {"\ufeff", "\u00a0", 1, false}, {strings.Repeat("x", 4988) + "😀" + strings.Repeat("y", 6000), "", 7, false}, {"", strings.Repeat("😀", 6000), 2, true}}
	results := []map[string]any{}
	for _, stdout := range []string{"", " x \n", "  \n\t\n  retained\ufeff", "\u00a0\n\u2007\n😀\u0085", "\r\n\n  x\n", "\ufeffx\ufeff"} {
		for _, interrupted := range []bool{false, true} {
			results = append(results, map[string]any{"stdout": stdout, "stderr": "\ufeff \n", "interrupted": interrupted, "isImage": false})
		}
	}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "commands": commands, "errors": errorsIn, "results": results})
	path, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-bash-result.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", path)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_BASH_RESULT_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("actual Source %v %.2400s", e, stderr.String())
	}
	var source struct {
		Semantics          []bool
		Errors             []json.RawMessage
		Mapped             []protocol.JSONObject
		MaxResultSizeChars int
		Capabilities       struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Semantics) != len(commands) || len(source.Errors) != len(errorsIn) || len(source.Mapped) != len(results) || len(source.Capabilities.Blocked) != 0 {
		t.Fatalf("Source escaped or wrong shape %v %.2400s", e, out)
	}
	for i, row := range commands {
		actual, e := bashResultIsError("darwin", row.Command, row.Code)
		if e != nil || actual != source.Semantics[i] {
			t.Errorf("semantics %q/%d Go%v Source%v %v", row.Command, row.Code, actual, source.Semantics[i], e)
		}
	}
	for i, row := range errorsIn {
		actual := toolerrors.FormatShellError(&toolerrors.ShellError{Stdout: row.Stdout, Stderr: row.Stderr, Code: row.Code, Interrupted: row.Interrupted})
		a, _ := protocol.DecodeJavaScriptJSONString(actual)
		b, _ := protocol.DecodeJavaScriptJSONString(source.Errors[i])
		if !reflect.DeepEqual(a, b) {
			t.Errorf("formatError case%d differs Go%d Source%d UTF16", i, len(a), len(b))
		}
	}
	for i, row := range results {
		actual, e := MapResult("Bash", raw(row), quote("bash-result-id"))
		if e != nil {
			t.Fatal(e)
		}
		var a, b any
		_ = json.Unmarshal(raw(actual), &a)
		_ = json.Unmarshal(raw(source.Mapped[i]), &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("mapped case%d Go%s Source%s", i, raw(actual), raw(source.Mapped[i]))
		}
	}
	s, o, _, _ := fixture(t)
	if e = o.Run(ctx, func(scope *cooperative.Scope) error {
		_, catalog, e := s.Catalog(scope)
		if e != nil {
			return e
		}
		for _, tool := range catalog {
			if tool.Name == "Bash" && tool.PersistenceThreshold != float64(source.MaxResultSizeChars) {
				t.Errorf("Bash threshold%v Source%d", tool.PersistenceThreshold, source.MaxResultSizeChars)
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}

func TestNativeBashExitOneIsInformationalAndFailuresRetainActualMergedOutput(t *testing.T) {
	s, o, home, _ := fixture(t)
	if e := os.WriteFile(filepath.Join(home, "input"), []byte("line\n"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, command := range []string{"grep absent input", "diff input /dev/null", "test x = y", "[ x = y ]"} {
		call(t, s, o, "Bash", protocol.NewJSONObject(field("command", command)))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	e := o.Run(ctx, func(scope *cooperative.Scope) error {
		_, e := s.Call(ctx, "Bash", input(`{"command":"printf shell-ok; printf error >&2; exit 7"}`), scope, false)
		return e
	})
	var shell *toolerrors.ShellError
	if !errors.As(e, &shell) || shell.Code != 7 || shell.Stdout != "" || shell.Stderr != "shell-okerror" || shell.Interrupted {
		t.Fatalf("actual merged shell failure lost %+v %v", shell, e)
	}
}
