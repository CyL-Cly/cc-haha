// Package recovery preserves the deliberately reduced local Recovery CLI.
// It is independent of the Server and never falls back to the old TS runtime.
package recovery

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

const Help = `Usage: claude-haha [options] [prompt]

Local recovery mode for this leaked source tree.

Options:
  -h, --help                    Show help
  -v, --version                 Show version
  (no args)                     Start local interactive mode
  -p, --print                   Send a single prompt and print the result
  --model <model>               Override model
  --system-prompt <text>        Override system prompt
  --system-prompt-file <file>   Read system prompt from file
  --append-system-prompt <text> Append to the system prompt
  --output-format <format>      text (default) or json

Environment:
  ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN
  ANTHROPIC_BASE_URL
  ANTHROPIC_MODEL
  API_TIMEOUT_MS
`

const Version = "999.0.0-local (Claude Code local recovery)\n"

type IO struct {
	In         io.Reader
	Out, Err   io.Writer
	InputIsTTY bool
	Getenv     func(string) string
	LookupEnv  func(string) (string, bool)
	ReadFile   func(string) ([]byte, error)
	HTTPClient *http.Client
}

type options struct {
	command, model, format, prompt string
	print                          bool
	system, append                 *string
}

// Recovery has its own legacy argument rules, including ignored unknown flags.
// Do not apply the main CLI's Cobra defaults to this compatibility profile.
func parse(args []string, getenv func(string) string, readFile func(string) ([]byte, error)) (options, error) {
	parsed := options{model: getenv("ANTHROPIC_MODEL"), format: "text"}
	var positional []string
	for index := 0; index < len(args); index++ {
		arg := args[index]
		next := func() *string {
			index++
			if index >= len(args) {
				return nil
			}
			value := args[index]
			return &value
		}
		switch arg {
		case "-h", "--help":
			parsed.command = "help"
			return parsed, nil
		case "-v", "--version", "-V":
			parsed.command = "version"
			return parsed, nil
		case "-p", "--print":
			parsed.print = true
		case "--bare", "--dangerously-skip-permissions":
		case "--model":
			parsed.model = valueOrEmpty(next())
		case "--system-prompt":
			parsed.system = next()
		case "--append-system-prompt":
			parsed.append = next()
		case "--system-prompt-file":
			file := next()
			if file == nil {
				return parsed, fmt.Errorf("--system-prompt-file requires a file")
			}
			content, err := readFile(*file)
			if err != nil {
				return parsed, err
			}
			text := string(content)
			parsed.system = &text
		case "--output-format":
			value := valueOrEmpty(next())
			if value == "text" || value == "json" {
				parsed.format = value
			}
		default:
			if arg != "" && !strings.HasPrefix(arg, "-") {
				positional = append(positional, arg)
			}
		}
	}
	parsed.prompt = jsTrim(strings.Join(positional, " "))
	return parsed, nil
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func jsTrim(value string) string {
	return strings.TrimFunc(value, func(r rune) bool {
		return r == '\ufeff' || r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r' || r == '\u2028' || r == '\u2029' || unicode.Is(unicode.Zs, r)
	})
}

func systemPrompt(parsed options) *string {
	if valueOrEmpty(parsed.system) != "" && valueOrEmpty(parsed.append) != "" {
		combined := *parsed.system + "\n\n" + *parsed.append
		return &combined
	}
	if parsed.system != nil {
		return parsed.system
	}
	return parsed.append
}

var integerPrefix = regexp.MustCompile(`^[+-]?\d+`)

func timeout(getenv func(string) string) float64 {
	value := getenv("API_TIMEOUT_MS")
	if value == "" {
		return 600000
	}
	// parseInt(value, 10) consumes only a decimal prefix after ECMAScript
	// whitespace and produces a double, including NaN and overflow Infinity.
	number := integerPrefix.FindString(jsTrim(value))
	if number == "" {
		return math.NaN()
	}
	millis, _ := strconv.ParseFloat(number, 64)
	return millis
}

type message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func inputMessage(input string) message {
	content, _ := json.Marshal(input) // A Go string always has a JSON encoding.
	return message{Role: "user", Content: content}
}

type request struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    *string   `json:"system,omitempty"`
	Messages  []message `json:"messages"`
}

// Run uses injected IO and environment so deterministic checks cannot read
// credentials or configuration from a developer's home directory.
func Run(ctx context.Context, args []string, streams IO) (int, error) {
	if streams.Getenv == nil {
		streams.Getenv = os.Getenv
		if streams.LookupEnv == nil {
			streams.LookupEnv = os.LookupEnv
		}
	}
	if streams.LookupEnv == nil {
		streams.LookupEnv = func(key string) (string, bool) {
			value := streams.Getenv(key)
			return value, value != ""
		}
	}
	if streams.ReadFile == nil {
		streams.ReadFile = os.ReadFile
	}
	parsed, err := parse(args, streams.Getenv, streams.ReadFile)
	if err != nil {
		return 1, err
	}
	if parsed.command == "help" {
		_, err = io.WriteString(streams.Out, Help)
		return 0, err
	}
	if parsed.command == "version" {
		_, err = io.WriteString(streams.Out, Version)
		return 0, err
	}
	prompt := parsed.prompt
	if parsed.print && prompt == "" && !streams.InputIsTTY {
		input, readErr := io.ReadAll(streams.In)
		if readErr != nil {
			return 1, readErr
		}
		prompt = jsTrim(string(bytes.ToValidUTF8(input, []byte("\ufffd"))))
	}
	if parsed.print && prompt == "" {
		_, err = io.WriteString(streams.Err, "Error: prompt is required\n")
		return 1, err
	}
	apiKey, authToken := streams.Getenv("ANTHROPIC_API_KEY"), streams.Getenv("ANTHROPIC_AUTH_TOKEN")
	if apiKey == "" && authToken == "" {
		_, err = io.WriteString(streams.Err, "Error: set ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN\n")
		return 1, err
	}
	model := parsed.model
	if model == "" {
		model = streams.Getenv("ANTHROPIC_DEFAULT_SONNET_MODEL")
	}
	if model == "" {
		model = streams.Getenv("ANTHROPIC_MODEL")
	}
	if model == "" {
		_, err = io.WriteString(streams.Err, "Error: model is required\n")
		return 1, err
	}
	millis := timeout(streams.Getenv)
	_, apiKeySet := streams.LookupEnv("ANTHROPIC_API_KEY")
	_, authTokenSet := streams.LookupEnv("ANTHROPIC_AUTH_TOKEN")
	client := provider.NewAnthropic(provider.AnthropicConfig{APIKey: apiKey, AuthToken: authToken, APIKeySet: apiKeySet, AuthTokenSet: authTokenSet, BaseURL: streams.Getenv("ANTHROPIC_BASE_URL"), TimeoutMillis: &millis, HTTPClient: streams.HTTPClient})
	base := request{Model: model, MaxTokens: 4096, System: systemPrompt(parsed)}
	if !parsed.print {
		return interactive(ctx, client, base, streams)
	}
	base.Messages = []message{inputMessage(prompt)}
	response, err := client.CreateMessage(ctx, base)
	if err != nil {
		return 1, err
	}
	if parsed.format == "json" {
		serialized, serializeErr := protocol.ParseAndStringifyJavaScriptJSON(response.Raw)
		if serializeErr != nil {
			return 1, serializeErr
		}
		var pretty bytes.Buffer
		if err = json.Indent(&pretty, serialized, "", "  "); err != nil {
			return 1, err
		}
		_, err = fmt.Fprintln(streams.Out, pretty.String())
		return 0, err
	}
	text, err := response.Text()
	if err != nil {
		return 1, err
	}
	_, err = fmt.Fprintln(streams.Out, text)
	return 0, err
}

func interactive(ctx context.Context, client *provider.Anthropic, base request, streams IO) (int, error) {
	if _, err := fmt.Fprintf(streams.Out, "Claude Haha local interactive mode\nmodel: %s\ncommands: /exit, /clear\n\nyou> ", base.Model); err != nil {
		return 1, err
	}
	reader := bufio.NewReader(streams.In)
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return 1, readErr
		}
		if readErr == io.EOF && line == "" {
			return 0, nil
		}
		input := jsTrim(strings.ToValidUTF8(line, "\ufffd"))
		switch input {
		case "/exit", "/quit":
			return 0, nil
		case "/clear":
			base.Messages = nil
			if _, err := io.WriteString(streams.Out, "history cleared\n"); err != nil {
				return 1, err
			}
		case "":
		default:
			base.Messages = append(base.Messages, inputMessage(input))
			response, err := client.CreateMessage(ctx, base)
			var text string
			var content json.RawMessage
			if err == nil {
				content, err = response.TextJSON()
				if err == nil {
					err = json.Unmarshal(content, &text)
				}
			}
			if err != nil {
				if _, writeErr := fmt.Fprintf(streams.Err, "error: %s\n", err); writeErr != nil {
					return 1, writeErr
				}
			} else {
				if _, err := fmt.Fprintf(streams.Out, "claude> %s\n\n", text); err != nil {
					return 1, err
				}
				base.Messages = append(base.Messages, message{Role: "assistant", Content: content})
			}
		}
		if _, err := io.WriteString(streams.Out, "you> "); err != nil {
			return 1, err
		}
		if readErr == io.EOF {
			return 0, nil
		}
	}
}
