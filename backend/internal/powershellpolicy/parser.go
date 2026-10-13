package powershellpolicy

import (
	"bytes"
	"container/list"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"sync"
	"time"
	"unicode/utf16"
)

// ParseScriptBody is the original native AST producer, copied from frozen
// MAIN src/utils/powershell/parser.ts PARSE_SCRIPT_BODY. No command being
// analyzed is executed: it is UTF-8/base64 data passed to Parser.ParseInput.
//
//go:embed parse_script.ps1
var ParseScriptBody string

type ProcessResult struct {
	Stdout, Stderr string
	ExitCode       int
	TimedOut       bool
}
type Runner func(context.Context, string, []string, map[string]string, string) (ProcessResult, error)
type ParserOptions struct {
	Platform, Executable, CWD string
	Environment               map[string]string
	Run                       Runner
}
type cachedParse struct {
	command string
	result  Parsed
	ready   chan struct{}
	element *list.Element
}
type Parser struct {
	o     ParserOptions
	mu    sync.Mutex
	cache map[string]*cachedParse
	lru   list.List
}

func NewParser(o ParserOptions) *Parser {
	copy := map[string]string{}
	for k, v := range o.Environment {
		copy[k] = v
	}
	o.Environment = copy
	if o.Run == nil {
		o.Run = runParseProcess
	}
	return &Parser{o: o, cache: map[string]*cachedParse{}}
}

// Source reads process.env on each cache miss. A settings refresh updates the
// process snapshot for later misses without invalidating already parsed ASTs
// or mutating the environment of an admitted physical process.
func (p *Parser) UpdateEnvironment(environment map[string]string) {
	copy := make(map[string]string, len(environment))
	for key, value := range environment {
		copy[key] = value
	}
	p.mu.Lock()
	p.o.Environment = copy
	p.mu.Unlock()
}
func WindowsMaxCommandLength() int {
	length := len(utf16.Encode([]rune(ParseScriptBody)))
	budget := (32767.0-200)*3/8 - float64(length) - float64(len("$EncodedCommand = ''\n"))
	return max(0, int(math.Floor(budget*3/4))-100)
}
func ParseArguments(command string) []string {
	script := "$EncodedCommand = '" + base64.StdEncoding.EncodeToString([]byte(command)) + "'\n" + ParseScriptBody
	units := utf16.Encode([]rune(script))
	data := make([]byte, len(units)*2)
	for i, u := range units {
		binary.LittleEndian.PutUint16(data[i*2:], u)
	}
	return []string{"-NoProfile", "-NonInteractive", "-NoLogo", "-EncodedCommand", base64.StdEncoding.EncodeToString(data)}
}
func invalid(command, message, id string) Parsed {
	return Parsed{Errors: []ParseError{{Message: message, ErrorID: id}}, Statements: []Statement{}, Variables: []Variable{}, OriginalCommand: command}
}
func transient(parsed Parsed) bool {
	if parsed.Valid || len(parsed.Errors) == 0 {
		return false
	}
	switch parsed.Errors[0].ErrorID {
	case "PwshSpawnError", "PwshError", "PwshTimeout", "EmptyOutput", "InvalidJson", "Cancelled":
		return true
	}
	return false
}
func (p *Parser) Parse(ctx context.Context, command string) Parsed {
	p.mu.Lock()
	if cached := p.cache[command]; cached != nil {
		p.lru.MoveToFront(cached.element)
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return invalid(command, context.Cause(ctx).Error(), "Cancelled")
		case <-cached.ready:
			return cloneParsed(cached.result)
		}
	}
	cached := &cachedParse{command: command, ready: make(chan struct{})}
	cached.element = p.lru.PushFront(cached)
	p.cache[command] = cached
	for p.lru.Len() > 256 {
		old := p.lru.Back()
		p.lru.Remove(old)
		delete(p.cache, old.Value.(*cachedParse).command)
	}
	p.mu.Unlock()
	parsed := p.parse(ctx, command)
	p.mu.Lock()
	cached.result = parsed
	close(cached.ready)
	if transient(parsed) && p.cache[command] == cached {
		delete(p.cache, command)
		p.lru.Remove(cached.element)
	}
	p.mu.Unlock()
	return cloneParsed(parsed)
}
func cloneParsed(parsed Parsed) Parsed {
	// Callers perform permission analysis and may attach local state. Cached
	// Source AST ownership must never be shared with such mutable consumers.
	b, _ := json.Marshal(parsed)
	var copy Parsed
	_ = json.Unmarshal(b, &copy)
	return copy
}
func (p *Parser) parse(ctx context.Context, command string) Parsed {
	p.mu.Lock()
	options := p.o
	p.mu.Unlock()
	limit := 4500
	if options.Platform == "win32" || options.Platform == "windows" {
		limit = WindowsMaxCommandLength()
	}
	if len([]byte(command)) > limit {
		return invalid(command, fmt.Sprintf("Command too long for parsing (%d bytes). Maximum supported length is %d bytes.", len([]byte(command)), limit), "CommandTooLong")
	}
	if options.Executable == "" {
		return invalid(command, "PowerShell is not available", "NoPowerShell")
	}
	ms := ParseTimeoutMilliseconds(options.Environment["CLAUDE_CODE_PWSH_PARSE_TIMEOUT_MS"])
	if math.IsInf(ms, 0) {
		return invalid(command, "Failed to spawn PowerShell: Expected the `timeout` option to be a non-negative integer, got `"+parseTimeoutNumber(ms)+"` (number)", "PwshSpawnError")
	}
	// Original execa uses Node timers, whose delays above signed-int32 range
	// are scheduled at 1ms. Do not overflow a Go time.Duration into a new wait.
	timeout := time.Millisecond
	if ms <= 2147483647 {
		timeout = time.Duration(ms) * time.Millisecond
	}
	var result ProcessResult
	for attempt := 0; attempt < 2; attempt++ {
		if ctx.Err() != nil {
			return invalid(command, context.Cause(ctx).Error(), "Cancelled")
		}
		request, cancel := context.WithTimeout(ctx, timeout)
		var err error
		result, err = options.Run(request, options.Executable, ParseArguments(command), options.Environment, options.CWD)
		cancel()
		if ctx.Err() != nil {
			return invalid(command, context.Cause(ctx).Error(), "Cancelled")
		}
		if err != nil {
			return invalid(command, "Failed to spawn PowerShell: "+err.Error(), "PwshSpawnError")
		}
		if !result.TimedOut {
			break
		}
	}
	if result.TimedOut {
		return invalid(command, "pwsh timed out after "+parseTimeoutNumber(ms)+"ms (2 attempts)", "PwshTimeout")
	}
	if result.ExitCode != 0 {
		return invalid(command, fmt.Sprintf("pwsh exited with code %d: %s", result.ExitCode, result.Stderr), "PwshError")
	}
	stdout := jsTrim(result.Stdout)
	if stdout == "" {
		return invalid(command, "No output from PowerShell parser", "EmptyOutput")
	}
	parsed, err := TransformRaw([]byte(stdout))
	if err != nil {
		return invalid(command, "Invalid JSON from PowerShell parser", "InvalidJson")
	}
	return parsed
}
func runParseProcess(ctx context.Context, path string, args []string, env map[string]string, cwd string) (ProcessResult, error) {
	command := exec.CommandContext(ctx, path, args...)
	command.Dir = cwd
	command.Env = []string{}
	for k, v := range env {
		command.Env = append(command.Env, k+"="+v)
	}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	command.WaitDelay = time.Second
	err := command.Run()
	result := ProcessResult{Stdout: stdout.String(), Stderr: stderr.String(), TimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded)}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		result.ExitCode = exit.ExitCode()
		return result, nil
	}
	return result, err
}
