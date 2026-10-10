package recovery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureIO(env map[string]string, input string) (IO, *bytes.Buffer, *bytes.Buffer) {
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	return IO{In: strings.NewReader(input), Out: stdout, Err: stderr, Getenv: func(key string) string { return env[key] }, LookupEnv: func(key string) (string, bool) { value, found := env[key]; return value, found }}, stdout, stderr
}

func TestHelpVersionAndRequiredInputs(t *testing.T) {
	for _, fixture := range []struct {
		args            []string
		env             map[string]string
		input, out, err string
		code            int
	}{
		{args: []string{"--help"}, out: Help},
		{args: []string{"-h"}, out: Help},
		{args: []string{"--version"}, out: Version},
		{args: []string{"-v"}, out: Version},
		{args: []string{"-V"}, out: Version},
		{args: []string{"--print"}, err: "Error: prompt is required\n", code: 1},
		{args: []string{"--print", "hello"}, err: "Error: set ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN\n", code: 1},
		{args: nil, err: "Error: set ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN\n", code: 1},
		{args: []string{"--print", "hello"}, env: map[string]string{"ANTHROPIC_API_KEY": "fixture"}, err: "Error: model is required\n", code: 1},
	} {
		streams, stdout, stderr := fixtureIO(fixture.env, fixture.input)
		code, err := Run(context.Background(), fixture.args, streams)
		if err != nil || code != fixture.code || stdout.String() != fixture.out || stderr.String() != fixture.err {
			t.Fatalf("args=%v code=%d error=%v stdout=%q stderr=%q", fixture.args, code, err, stdout, stderr)
		}
	}
	streams, _, stderr := fixtureIO(nil, "stdin ignored")
	streams.InputIsTTY = true
	code, err := Run(context.Background(), []string{"--print"}, streams)
	if err != nil || code != 1 || stderr.String() != "Error: prompt is required\n" {
		t.Fatal("read prompt from a terminal")
	}
}

func TestPrintRequestProfiles(t *testing.T) {
	for _, fixture := range []struct {
		args         []string
		env          map[string]string
		input, model string
		system       *string
		want         string
	}{
		{args: []string{"-p", "hello", "world"}, model: "flash", want: "first\n第二\n"},
		{args: []string{"--print", "--model", "override", "--system-prompt", "base", "--append-system-prompt", "append", "--bare", "--dangerously-skip-permissions", "--unknown", "hello"}, model: "override", system: str("base\n\nappend"), want: "first\n第二\n"},
		{args: []string{"--print", "--model"}, env: map[string]string{"ANTHROPIC_DEFAULT_SONNET_MODEL": "sonnet"}, input: "\ufeff hello\r\n", model: "sonnet", want: "first\n第二\n"},
		{args: []string{"--print", "--system-prompt", "", "--append-system-prompt", "ignored", "hello"}, model: "flash", system: str(""), want: "first\n第二\n"},
		{args: []string{"--print", "--append-system-prompt", "append", "--output-format", "invalid", "hello"}, model: "flash", system: str("append"), want: "first\n第二\n"},
		{args: []string{"--print", "--output-format", "json", "hello"}, model: "flash", want: "{\n  \"content\": [\n    {\n      \"type\": \"text\",\n      \"text\": \"first\"\n    },\n    {\n      \"type\": \"thinking\",\n      \"thinking\": \"private\"\n    },\n    {\n      \"type\": \"text\",\n      \"text\": \"第二\"\n    }\n  ],\n  \"extra\": true\n}\n"},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body request
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.Model != fixture.model || body.MaxTokens != 4096 || len(body.Messages) != 1 || body.Messages[0].Role != "user" || body.System == nil && fixture.system != nil || body.System != nil && fixture.system == nil || body.System != nil && *body.System != *fixture.system {
				t.Errorf("wrong request: %+v", body)
			}
			io.WriteString(w, `{"content":[{"type":"text","text":"first"},{"type":"thinking","thinking":"private"},{"type":"text","text":"第二"}],"extra":true}`)
		}))
		env := map[string]string{"ANTHROPIC_MODEL": "flash", "ANTHROPIC_AUTH_TOKEN": "fixture-token", "ANTHROPIC_BASE_URL": server.URL, "API_TIMEOUT_MS": "2000ms"}
		for k, v := range fixture.env {
			env[k] = v
		}
		streams, stdout, stderr := fixtureIO(env, fixture.input)
		code, err := Run(context.Background(), fixture.args, streams)
		server.Close()
		if code != 0 || err != nil || stdout.String() != fixture.want || stderr.Len() != 0 {
			t.Fatalf("code=%d err=%v out=%q stderr=%s", code, err, stdout, stderr)
		}
	}
}

func str(value string) *string { return &value }

func TestInteractiveHistoryClearAndErrorContinuation(t *testing.T) {
	var requests []request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body request
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests = append(requests, body)
		if len(requests) == 2 {
			w.WriteHeader(500)
			io.WriteString(w, `{"error":{"type":"api_error","message":"fixture failure"}}`)
			return
		}
		io.WriteString(w, `{"content":[{"type":"text","text":"answer"}]}`)
	}))
	defer server.Close()
	streams, stdout, stderr := fixtureIO(map[string]string{"ANTHROPIC_API_KEY": "fixture", "ANTHROPIC_MODEL": "flash", "ANTHROPIC_BASE_URL": server.URL}, " \nfirst\nfailed\nthird\n/clear\nafter\n/quit\nignored\n")
	code, err := Run(context.Background(), []string{"ignored positional"}, streams)
	if code != 0 || err != nil {
		t.Fatal(code, err)
	}
	if len(requests) != 4 || len(requests[1].Messages) != 3 || len(requests[2].Messages) != 4 || len(requests[3].Messages) != 1 || string(requests[3].Messages[0].Content) != `"after"` {
		t.Fatalf("history drift: %+v", requests)
	}
	if stdout.String() != "Claude Haha local interactive mode\nmodel: flash\ncommands: /exit, /clear\n\nyou> you> claude> answer\n\nyou> you> claude> answer\n\nyou> history cleared\nyou> claude> answer\n\nyou> " {
		t.Fatalf("output drift: %q", stdout)
	}
	if stderr.String() != "error: 500 {\"error\":{\"type\":\"api_error\",\"message\":\"fixture failure\"}}\n" {
		t.Fatalf("stderr drift: %q", stderr)
	}
}

type brokenIO struct{}

func (brokenIO) Read([]byte) (int, error)  { return 0, errors.New("read failed") }
func (brokenIO) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestFileInputAndIOFailures(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "system.txt")
	if err := os.WriteFile(file, []byte("from file"), 0600); err != nil {
		t.Fatal(err)
	}
	parsed, err := parse([]string{"", "--system-prompt-file", file}, func(string) string { return "" }, os.ReadFile)
	if err != nil || valueOrEmpty(parsed.system) != "from file" {
		t.Fatal(parsed, err)
	}
	for _, args := range [][]string{{"--system-prompt-file"}, {"--system-prompt-file", filepath.Join(root, "missing")}} {
		if _, err := parse(args, func(string) string { return "" }, os.ReadFile); err == nil {
			t.Fatal("missing file accepted")
		}
	}
	for _, fixture := range []struct {
		args  []string
		env   map[string]string
		stdin bool
	}{
		{args: []string{"--help"}}, {args: []string{"--version"}},
		{args: []string{"--print"}, stdin: true}, {args: []string{"--print"}},
		{args: []string{"--print", "hello"}}, {args: []string{"--print", "hello"}, env: map[string]string{"ANTHROPIC_API_KEY": "fixture"}},
	} {
		streams, _, _ := fixtureIO(fixture.env, "")
		streams.Out, streams.Err = brokenIO{}, brokenIO{}
		if fixture.stdin {
			streams.In = brokenIO{}
		}
		if _, err := Run(context.Background(), fixture.args, streams); err == nil {
			t.Fatalf("ignored broken IO for %v", fixture.args)
		}
	}
}

func TestTimeoutUsesJavaScriptDecimalPrefixAndNumber(t *testing.T) {
	for _, fixture := range []struct {
		input string
		want  float64
	}{
		{"", 600000}, {" 2000ms", 2000}, {"\ufeff1000", 1000},
		{"\u00851000", math.NaN()}, {"nope", math.NaN()}, {"+", math.NaN()},
		{"-1", -1}, {"-0", math.Copysign(0, -1)}, {"0x20", 0},
		{"1e3", 1}, {"1.9", 1}, {"999999999999999999999999999999999999999999999999", 1e48},
		{strings.Repeat("9", 400), math.Inf(1)},
	} {
		got := timeout(func(string) string { return fixture.input })
		if got != fixture.want && !(math.IsNaN(got) && math.IsNaN(fixture.want)) || math.Signbit(got) != math.Signbit(fixture.want) {
			t.Fatalf("timeout(%q) = %v, want %v", fixture.input, got, fixture.want)
		}
	}
}

func TestInvalidTimeoutRemainsARecoverablePerTurnError(t *testing.T) {
	for _, fixture := range []struct{ input, message string }{
		{"NaN", "timeout must be an integer"}, {"-1", "timeout must be a positive integer"},
		{"\u00851000", "timeout must be an integer"}, {strings.Repeat("9", 400), "timeout must be an integer"},
	} {
		streams, stdout, stderr := fixtureIO(map[string]string{"ANTHROPIC_API_KEY": "fixture", "ANTHROPIC_MODEL": "flash", "API_TIMEOUT_MS": fixture.input}, "first\nsecond\n/exit\n")
		streams.HTTPClient = &http.Client{Transport: recoveryTransport(func(*http.Request) (*http.Response, error) {
			t.Error("invalid timeout reached provider transport")
			return nil, errors.New("unexpected request")
		})}
		code, err := Run(context.Background(), nil, streams)
		if code != 0 || err != nil || stderr.String() != strings.Repeat("error: "+fixture.message+"\n", 2) || !strings.HasSuffix(stdout.String(), "you> you> you> ") {
			t.Fatalf("input=%q code=%d err=%v stdout=%q stderr=%q", fixture.input, code, err, stdout, stderr)
		}
	}
}

type recoveryTransport func(*http.Request) (*http.Response, error)

func (transport recoveryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

type failAfterWrites struct {
	writes, allowed int
}

func (writer *failAfterWrites) Write(value []byte) (int, error) {
	writer.writes++
	if writer.writes > writer.allowed {
		return 0, errors.New("output disconnected")
	}
	return len(value), nil
}

func TestInteractiveEOFAndDisconnectedPipes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"content":[{"type":"text","text":"answer"}]}`)
	}))
	defer server.Close()
	env := map[string]string{"ANTHROPIC_API_KEY": "fixture", "ANTHROPIC_MODEL": "flash", "ANTHROPIC_BASE_URL": server.URL}
	for _, input := range []string{"", "last line without newline"} {
		streams, stdout, stderr := fixtureIO(env, input)
		code, err := Run(context.Background(), nil, streams)
		if code != 0 || err != nil || stderr.Len() != 0 || input != "" && !strings.Contains(stdout.String(), "claude> answer") {
			t.Fatalf("EOF input=%q code=%d err=%v out=%s", input, code, err, stdout)
		}
	}
	for _, fixture := range []struct {
		input   string
		allowed int
		badRead bool
	}{
		{input: "", allowed: 0},
		{input: "/clear\n", allowed: 1},
		{input: "hello\n", allowed: 1},
		{input: "\n", allowed: 1},
		{input: "hello\n", allowed: 2},
		{input: "", allowed: 10, badRead: true},
	} {
		streams, _, _ := fixtureIO(env, fixture.input)
		streams.Out = &failAfterWrites{allowed: fixture.allowed}
		if fixture.badRead {
			streams.In = brokenIO{}
		}
		code, err := Run(context.Background(), nil, streams)
		if code != 1 || err == nil {
			t.Fatalf("ignored disconnected stream: %+v code=%d err=%v", fixture, code, err)
		}
	}
}

func TestRequestFailuresRemainInTheProperOutputChannel(t *testing.T) {
	for _, fixture := range []struct {
		body   string
		status int
	}{
		{body: `{"error":{"message":"failed"}}`, status: 500},
		{body: `{}`, status: 200},
	} {
		requests := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			w.WriteHeader(fixture.status)
			io.WriteString(w, fixture.body)
		}))
		env := map[string]string{"ANTHROPIC_API_KEY": "fixture", "ANTHROPIC_MODEL": "flash", "ANTHROPIC_BASE_URL": server.URL}
		streams, stdout, stderr := fixtureIO(env, "")
		streams.LookupEnv = nil // Embedders may supply Getenv without LookupEnv.
		code, err := Run(context.Background(), []string{"--print", "hello"}, streams)
		if code != 1 || err == nil || stdout.Len() != 0 || stderr.Len() != 0 || requests != 1 {
			t.Fatalf("failed print emitted output or retried: code=%d err=%v out=%s requests=%d", code, err, stdout, requests)
		}
		streams, _, _ = fixtureIO(env, "hello\n")
		streams.Err = brokenIO{}
		if code, err := Run(context.Background(), nil, streams); code != 1 || err == nil {
			t.Fatal("interactive stderr failure was ignored")
		}
		server.Close()
	}
	streams, _, _ := fixtureIO(map[string]string{"ANTHROPIC_API_KEY": "fixture", "ANTHROPIC_MODEL": "flash", "API_TIMEOUT_MS": "invalid"}, "")
	if code, err := Run(context.Background(), []string{"--print", "hello"}, streams); code != 1 || err == nil {
		t.Fatal("invalid timeout did not fail")
	}
}
