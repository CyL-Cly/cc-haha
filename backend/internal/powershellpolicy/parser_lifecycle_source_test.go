package powershellpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
)

func TestActualSourcePowerShellParserFailuresRetryAndTransientCacheEviction(t *testing.T) {
	type profile struct {
		Command      string           `json:"command"`
		Results      []map[string]any `json:"results"`
		Calls        int              `json:"calls"`
		NoPowerShell bool             `json:"noPowerShell,omitempty"`
	}
	profiles := []profile{
		{Command: "Get-Process", NoPowerShell: true, Calls: 2},
		{Command: strings.Repeat("中", 1501), Calls: 2},
		{Command: "Get-Process", Calls: 1, Results: []map[string]any{{"timedOut": true}, {"timedOut": true}}},
		{Command: "Get-Process", Calls: 1, Results: []map[string]any{{"timedOut": true}, {"stdout": `{"valid":true,"errors":[],"statements":[],"variables":[],"hasStopParsing":false,"originalCommand":"Get-Process"}`, "failed": false, "timedOut": false, "exitCode": 0}}},
		{Command: "Get-Process", Calls: 2, Results: []map[string]any{{"stdout": "", "stderr": "private error", "failed": true, "exitCode": 17}, {"stdout": "", "stderr": "private error", "failed": true, "exitCode": 17}}},
		{Command: "Get-Process", Calls: 2, Results: []map[string]any{{"stdout": ""}, {"stdout": ""}}},
		{Command: "Get-Process", Calls: 2, Results: []map[string]any{{"stdout": "non-JSON"}, {"stdout": "null"}}},
		{Command: "Get-Process", Calls: 2, Results: []map[string]any{{"error": "private spawn failure"}, {"error": "private spawn failure"}}},
	}
	var source struct {
		FailureProfiles []struct {
			Results            []json.RawMessage
			RemainingProcesses int
		}
	}
	sourcePolicy(t, map[string]any{"executable": "/fixture/pwsh", "failureProfiles": profiles}, &source)
	if len(source.FailureProfiles) != len(profiles) {
		t.Fatal("incomplete Source process failure corpus")
	}
	for i, profile := range profiles {
		index := 0
		executable := "/fixture/pwsh"
		if profile.NoPowerShell {
			executable = ""
		}
		parser := NewParser(ParserOptions{Platform: "darwin", Executable: executable, Run: func(context.Context, string, []string, map[string]string, string) (ProcessResult, error) {
			if index >= len(profile.Results) {
				t.Fatal("unowned extra Go AST process")
			}
			row := profile.Results[index]
			index++
			result := ProcessResult{}
			result.Stdout, _ = row["stdout"].(string)
			result.Stderr, _ = row["stderr"].(string)
			result.TimedOut, _ = row["timedOut"].(bool)
			result.ExitCode, _ = row["exitCode"].(int)
			if message, _ := row["error"].(string); message != "" {
				return result, errors.New(message)
			}
			return result, nil
		}})
		for call := 0; call < profile.Calls; call++ {
			sameJSON(t, "parser failure/cache lifecycle", parser.Parse(context.Background(), profile.Command), source.FailureProfiles[i].Results[call])
		}
		if len(profile.Results)-index != source.FailureProfiles[i].RemainingProcesses {
			t.Fatal("retry/cache process count differs", i, index)
		}
	}
}
func TestActualSourcePowerShellParserBoundedLRUAndTimeoutEnvironment(t *testing.T) {
	commands := []string{}
	for i := 0; i < 257; i++ {
		commands = append(commands, fmt.Sprintf("Get-Process -Id %d", i))
	}
	commands = append(commands, commands[1], "Get-Process -Id 257", commands[0])
	probes := []string{"Get-Process -Id 0", "Get-Process -Id 1", "Get-Process -Id 2", "Get-Process -Id 3", "Get-Process -Id 256", "Get-Process -Id 257"}
	timeouts := []string{"", "0", "-1", "false", "  +17.9ms", "250ms", "\uFEFF23ignored", "1e3", "0x10", "2147483648", "9007199254740993", strings.Repeat("9", 400)}
	var source struct {
		CacheRows []struct {
			Result json.RawMessage
			Size   int
			Has    []bool
		}
		TimeoutValues []string
	}
	sourcePolicy(t, map[string]any{"cacheCommands": commands, "cacheProbes": probes, "timeoutValues": timeouts}, &source)
	if len(source.CacheRows) != len(commands) || len(source.TimeoutValues) != len(timeouts) {
		t.Fatal("Source cache/timeout corpus incomplete")
	}
	parser := NewParser(ParserOptions{})
	for i, command := range commands {
		sameJSON(t, "cache permanent unavailable parser", parser.Parse(context.Background(), command), source.CacheRows[i].Result)
		parser.mu.Lock()
		size := len(parser.cache)
		has := []bool{}
		for _, probe := range probes {
			_, found := parser.cache[probe]
			has = append(has, found)
		}
		parser.mu.Unlock()
		if size != source.CacheRows[i].Size || fmt.Sprint(has) != fmt.Sprint(source.CacheRows[i].Has) {
			t.Fatalf("Source LRU row%d size=%d/%d has=%v/%v", i, size, source.CacheRows[i].Size, has, source.CacheRows[i].Has)
		}
	}
	for i, value := range timeouts {
		if got := parseTimeoutNumber(ParseTimeoutMilliseconds(value)); got != source.TimeoutValues[i] {
			t.Fatalf("Source parse timeout %q -> %q/%q", value, got, source.TimeoutValues[i])
		}
	}
}

func TestActualSourcePowerShellParserSettingsEnvironmentRefreshRetainsCachedAST(t *testing.T) {
	valid := `{"valid":true,"errors":[],"statements":[],"variables":[],"hasStopParsing":false,"originalCommand":"Get-Process"}`
	rows := []map[string]any{
		{"command": "Get-Process", "timeout": "31ms", "results": []map[string]any{{"timedOut": true}, {"timedOut": true}}},
		{"command": "Get-Process", "timeout": "2000", "results": []map[string]any{{"stdout": valid, "failed": false, "timedOut": false, "exitCode": 0}}},
		{"command": "Get-Process", "timeout": "33", "results": []map[string]any{}},
		{"command": "Get-Service", "timeout": "2500", "results": []map[string]any{{"stdout": strings.ReplaceAll(valid, "Get-Process", "Get-Service"), "failed": false, "timedOut": false, "exitCode": 0}}},
	}
	var source struct {
		ParserEnvironmentRows []struct {
			Result             json.RawMessage
			Timeouts           []float64
			RemainingProcesses int
		}
	}
	sourcePolicy(t, map[string]any{"executable": "/fixture/pwsh", "parserEnvironmentRows": rows}, &source)
	if len(source.ParserEnvironmentRows) != len(rows) {
		t.Fatal("Source mutable environment corpus incomplete")
	}
	processes := []map[string]any{}
	observed := []float64{}
	parser := NewParser(ParserOptions{Executable: "/fixture/pwsh", Run: func(ctx context.Context, _ string, _ []string, environment map[string]string, _ string) (ProcessResult, error) {
		if _, owned := ctx.Deadline(); !owned {
			return ProcessResult{}, errors.New("parser process lacks configured deadline")
		}
		observed = append(observed, ParseTimeoutMilliseconds(environment["CLAUDE_CODE_PWSH_PARSE_TIMEOUT_MS"]))
		if len(processes) == 0 {
			return ProcessResult{}, errors.New("extra parser process after Source cache hit")
		}
		row := processes[0]
		processes = processes[1:]
		stdout, _ := row["stdout"].(string)
		timedOut, _ := row["timedOut"].(bool)
		return ProcessResult{Stdout: stdout, TimedOut: timedOut}, nil
	}})
	for i, row := range rows {
		environment := map[string]string{"CLAUDE_CODE_PWSH_PARSE_TIMEOUT_MS": row["timeout"].(string)}
		parser.UpdateEnvironment(environment)
		environment["CLAUDE_CODE_PWSH_PARSE_TIMEOUT_MS"] = "1" // Caller cannot mutate the admitted snapshot.
		processes = append([]map[string]any{}, row["results"].([]map[string]any)...)
		observed = []float64{}
		actual := parser.Parse(context.Background(), row["command"].(string))
		sameJSON(t, "current process environment / cached AST", actual, source.ParserEnvironmentRows[i].Result)
		if fmt.Sprint(observed) != fmt.Sprint(source.ParserEnvironmentRows[i].Timeouts) || len(processes) != source.ParserEnvironmentRows[i].RemainingProcesses {
			t.Fatalf("Source parser process admission row%d timeouts=%v/%v unused=%d/%d", i, observed, source.ParserEnvironmentRows[i].Timeouts, len(processes), source.ParserEnvironmentRows[i].RemainingProcesses)
		}
	}
}
func TestPowerShellParserConcurrentInflightCacheCancellationAndIsolation(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	parser := NewParser(ParserOptions{Executable: "/fixture/pwsh", Run: func(context.Context, string, []string, map[string]string, string) (ProcessResult, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return ProcessResult{Stdout: `{"valid":true,"errors":[],"statements":[{"type":"PipelineAst","text":"Get-Process","elements":[{"type":"CommandAst","text":"Get-Process","commandElements":[{"type":"StringConstantExpressionAst","text":"Get-Process","value":"Get-Process"}]}]}],"variables":[],"hasStopParsing":false,"originalCommand":"Get-Process"}`}, nil
	}})
	first := make(chan Parsed, 1)
	go func() { first <- parser.Parse(context.Background(), "Get-Process") }()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if parsed := parser.Parse(ctx, "Get-Process"); len(parsed.Errors) != 1 || parsed.Errors[0].ErrorID != "Cancelled" {
		t.Fatal("canceled second waiter retained cache lock")
	}
	close(release)
	result := <-first
	if !result.Valid || calls.Load() != 1 {
		t.Fatal("actual in-flight cache", result, calls.Load())
	}
	result.Statements[0].Commands[0].Name = "mutated caller"
	again := parser.Parse(context.Background(), "Get-Process")
	if again.Statements[0].Commands[0].Name != "Get-Process" || calls.Load() != 1 {
		t.Fatal("mutable AST escaped Source cache ownership")
	}
}
