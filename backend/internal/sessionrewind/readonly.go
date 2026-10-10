package sessionrewind

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/dlclark/regexp2/v2"
	"github.com/dop251/goja"
	gojaparser "github.com/dop251/goja/parser"
)

//go:embed assets/readonly_policy.json assets/shell-quote/parse.js assets/shell-quote/LICENSE
var readonlyAssets embed.FS

const ShellQuoteVersion = "1.8.3"

type UnsupportedRecordedCommand struct{ Feature string }

func (e *UnsupportedRecordedCommand) Error() string {
	return "recorded Bash command requires unported native classification: " + e.Feature
}

type commandPolicy struct {
	Name       string            `json:"name"`
	SafeFlags  map[string]string `json:"safeFlags"`
	DoubleDash *bool             `json:"respectsDoubleDash"`
	Regex      string            `json:"regex"`
	Callback   bool              `json:"callback"`
}
type readonlyPolicy struct {
	Commands, Ant                   []commandPolicy
	Regexes                         []string
	SafeEnvironment, AntEnvironment []string
}

var readonlyCompile struct {
	sync.Once
	policy  readonlyPolicy
	parser  *goja.Program
	failure error
}

type RecordedCommandClassifier struct{ Platform, UserType string }

func NewRecordedCommandClassifier(platform, userType string) (*RecordedCommandClassifier, error) {
	readonlyCompile.Do(func() {
		raw, err := readonlyAssets.ReadFile("assets/readonly_policy.json")
		if err != nil {
			readonlyCompile.failure = err
			return
		}
		if err = json.Unmarshal(raw, &readonlyCompile.policy); err != nil {
			readonlyCompile.failure = err
			return
		}
		raw, err = readonlyAssets.ReadFile("assets/shell-quote/parse.js")
		if err != nil {
			readonlyCompile.failure = err
			return
		}
		readonlyCompile.parser, readonlyCompile.failure = goja.Compile("shell-quote-"+ShellQuoteVersion+"/parse.js", "(function(module){\n"+string(raw)+"\nreturn module.exports;})", false)
	})
	if readonlyCompile.failure != nil {
		return nil, readonlyCompile.failure
	}
	if platform == "" {
		return nil, errors.New("recorded Bash classifier requires actual native platform")
	}
	return &RecordedCommandClassifier{platform, userType}, nil
}

type shellToken struct {
	text, op, pattern, comment string
	isString, isComment        bool
}

func (c *RecordedCommandClassifier) parse(command string, preserve bool) ([]shellToken, error) {
	vm := goja.New()
	vm.SetParserOptions(gojaparser.WithDisableSourceMaps)
	value, err := vm.RunProgram(readonlyCompile.parser)
	if err != nil {
		return nil, err
	}
	factory, ok := goja.AssertFunction(value)
	if !ok {
		return nil, errors.New("pure shell parser factory unavailable")
	}
	module := vm.NewObject()
	parserValue, err := factory(goja.Undefined(), module)
	if err != nil {
		return nil, err
	}
	parse, ok := goja.AssertFunction(parserValue)
	if !ok {
		return nil, errors.New("pure shell parser unavailable")
	}
	env := goja.Undefined()
	if preserve {
		env = vm.ToValue(func(call goja.FunctionCall) goja.Value { return vm.ToValue("$" + call.Argument(0).String()) })
	}
	parsed, err := parse(goja.Undefined(), vm.ToValue(command), env)
	if err != nil {
		return nil, err
	}
	var raw []any
	if err = vm.ExportTo(parsed, &raw); err != nil {
		return nil, err
	}
	result := []shellToken{}
	for _, token := range raw {
		switch value := token.(type) {
		case string:
			result = append(result, shellToken{text: value, isString: true})
		case map[string]any:
			t := shellToken{}
			if op, ok := value["op"].(string); ok {
				t.op = op
			}
			if pattern, ok := value["pattern"].(string); ok {
				t.pattern = pattern
			}
			if comment, ok := value["comment"].(string); ok {
				t.comment = comment
				t.isComment = true
			}
			result = append(result, t)
		default:
			return nil, &UnsupportedRecordedCommand{"pure shell token shape"}
		}
	}
	return result, nil
}

var regexCache sync.Map

func jsMatch(pattern, value string) bool {
	cached, ok := regexCache.Load(pattern)
	if !ok {
		compiled, err := regexp2.Compile(pattern, regexp2.ECMAScript)
		if err != nil {
			panic("invalid frozen command policy regexp: " + err.Error())
		}
		cached, _ = regexCache.LoadOrStore(pattern, compiled)
	}
	match, err := cached.(*regexp2.Regexp).MatchString(value)
	return err == nil && match
}
func jsTrimCommand(value string) string {
	return strings.TrimFunc(value, func(r rune) bool {
		return r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r' || r == ' ' || r == 0xa0 || r == 0x1680 || (r >= 0x2000 && r <= 0x200a) || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
	})
}
func flagArgument(value, kind string) bool {
	switch kind {
	case "number":
		return jsMatch(`^\d+$`, value)
	case "string":
		return true
	case "char":
		return len(utf16.Encode([]rune(value))) == 1
	case "{}":
		return value == "{}"
	case "EOF":
		return value == "EOF"
	}
	return false
}
func validateNativeFlags(tokens []string, start int, policy commandPolicy) bool {
	for i := start; i < len(tokens); {
		token := tokens[i]
		if token == "" {
			i++
			continue
		}
		if tokens[0] == "xargs" && (!strings.HasPrefix(token, "-") || token == "--") {
			if token == "--" && i+1 < len(tokens) {
				i++
				token = tokens[i]
			}
			switch token {
			case "echo", "printf", "wc", "grep", "head", "tail":
				return true
			}
			return false
		}
		if token == "--" {
			if policy.DoubleDash == nil || *policy.DoubleDash {
				return true
			}
			i++
			continue
		}
		if strings.HasPrefix(token, "-") && len(token) > 1 && jsMatch(`^-[a-zA-Z0-9_-]`, token) {
			flag, inline, hasEqual := strings.Cut(token, "=")
			kind := policy.SafeFlags[flag]
			if kind == "" {
				if tokens[0] == "git" && jsMatch(`^-\d+$`, flag) {
					i++
					continue
				}
				if (tokens[0] == "grep" || tokens[0] == "rg") && len(flag) > 2 && !strings.HasPrefix(flag, "--") {
					candidate, value := flag[:2], flag[2:]
					k := policy.SafeFlags[candidate]
					if (k == "number" || k == "string") && jsMatch(`^\d+$`, value) && flagArgument(value, k) {
						i++
						continue
					}
				}
				if strings.HasPrefix(flag, "--") || len(flag) <= 2 {
					return false
				}
				for _, r := range flag[1:] {
					if policy.SafeFlags["-"+string(r)] != "none" {
						return false
					}
				}
				i++
				continue
			}
			if kind == "none" {
				if hasEqual {
					return false
				}
				i++
				continue
			}
			arg := inline
			if hasEqual {
				i++
			} else {
				if i+1 >= len(tokens) || (len(tokens[i+1]) > 1 && jsMatch(`^-[a-zA-Z0-9_-]`, tokens[i+1])) {
					return false
				}
				arg = tokens[i+1]
				i += 2
			}
			if kind == "string" && strings.HasPrefix(arg, "-") && !(flag == "--sort" && tokens[0] == "git" && jsMatch(`^-[a-zA-Z]`, arg)) {
				return false
			}
			if !flagArgument(arg, kind) {
				return false
			}
		} else {
			i++
		}
	}
	return true
}
func (c *RecordedCommandClassifier) flagSafe(command string) (bool, error) {
	parsed, err := c.parse(command, true)
	if err != nil {
		return false, nil
	}
	tokens := []string{}
	for _, token := range parsed {
		if token.isString {
			tokens = append(tokens, token.text)
		} else if token.op == "glob" {
			tokens = append(tokens, token.pattern)
		} else {
			return false, nil
		}
	}
	if len(tokens) == 0 {
		return false, nil
	}
	policies := readonlyCompile.policy.Commands
	if c.UserType == "ant" {
		policies = append(append([]commandPolicy{}, policies...), readonlyCompile.policy.Ant...)
	}
	for _, policy := range policies {
		if (c.Platform == "windows" || c.Platform == "win32") && policy.Name == "xargs" {
			continue
		}
		parts := strings.Split(policy.Name, " ")
		if len(tokens) < len(parts) {
			continue
		}
		matches := true
		for i, v := range parts {
			if tokens[i] != v {
				matches = false
				break
			}
		}
		if !matches {
			continue
		}
		for _, token := range tokens[len(parts):] {
			if strings.Contains(token, "$") || strings.Contains(token, "{") && (strings.Contains(token, ",") || strings.Contains(token, "..")) {
				return false, nil
			}
			if policy.Name == "git ls-remote" && !strings.HasPrefix(token, "-") && strings.ContainsAny(token, "@:") {
				return false, nil
			}
		}
		if !validateNativeFlags(tokens, len(parts), policy) {
			return false, nil
		}
		if policy.Regex != "" && !jsMatch(policy.Regex, command) {
			return false, nil
		}
		if policy.Regex == "" && strings.Contains(command, "`") {
			return false, nil
		}
		if policy.Regex == "" && (tokens[0] == "rg" || tokens[0] == "grep") && strings.ContainsAny(command, "\n\r") {
			return false, nil
		}
		if policy.Callback {
			danger, err := c.policyDangerous(policy.Name, command, tokens[len(parts):])
			if err != nil {
				return false, err
			}
			if danger {
				return false, nil
			}
		}
		return true, nil
	}
	return false, nil
}
func unquotedExpansion(command string) bool {
	single, double, escaped := false, false, false
	for i, r := range command {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && !single {
			escaped = true
			continue
		}
		if r == '\'' && !double {
			single = !single
			continue
		}
		if r == '"' && !single {
			double = !double
			continue
		}
		if single {
			continue
		}
		if r == '$' && i+1 < len(command) && jsMatch(`^[A-Za-z_@*#?!$0-9-]`, command[i+1:]) {
			return true
		}
		if !double && strings.ContainsRune("?*[]", r) {
			return true
		}
	}
	return false
}
func (c *RecordedCommandClassifier) commandReadOnly(command string) (bool, error) {
	command = jsTrimCommand(command)
	if strings.HasSuffix(command, " 2>&1") {
		command = jsTrimCommand(command[:len(command)-5])
	}
	if c.vulnerableUNC(command) || unquotedExpansion(command) {
		return false, nil
	}
	flag, err := c.flagSafe(command)
	if err != nil {
		return false, err
	}
	if flag {
		return true, nil
	}
	for _, pattern := range readonlyCompile.policy.Regexes {
		if jsMatch(pattern, command) {
			if strings.Contains(command, "git") && (jsMatch(`\s-c[\s=]`, command) || jsMatch(`\s--exec-path[\s=]`, command) || jsMatch(`\s--config-env[\s=]`, command)) {
				return false, nil
			}
			return true, nil
		}
	}
	return false, nil
}
func (c *RecordedCommandClassifier) vulnerableUNC(command string) bool {
	if c.Platform != "windows" && c.Platform != "win32" {
		return false
	}
	for _, pattern := range []string{`\\\\[^\s\\/]+(?:@(?:\d+|ssl))?(?:[\\/]|$|\s)`, `(?<!:)//[^\s\\/]+(?:@(?:\d+|ssl))?(?:[\\/]|$|\s)`, `/\\{2,}[^\s\\/]`, `\\{2,}/[^\s\\/]`, `@SSL@\d+`, `@\d+@SSL`, `DavWWWRoot`} {
		if jsMatch("(?i)"+pattern, command) {
			return true
		}
	}
	return false
}

// Classify is a text-only replay decision. No command is ever executed.
func (c *RecordedCommandClassifier) Classify(command string) (bool, error) {
	if !utf8.ValidString(command) {
		return false, &UnsupportedRecordedCommand{"invalid native UTF-8 command"}
	}
	if jsTrimCommand(command) == "" {
		return false, nil
	}
	if _, err := c.parse(command, true); err != nil {
		return false, nil
	}
	if c.vulnerableUNC(command) {
		return false, nil
	}
	safe, err := c.security(command)
	if err != nil || !safe {
		return false, err
	}
	commands, err := c.split(command)
	if err != nil {
		return false, err
	}
	hasGit, hasCd := false, false
	for _, part := range commands {
		git, cd := c.normalizedCommands(part)
		hasGit = hasGit || git
		hasCd = hasCd || cd
	}
	if hasGit && hasCd {
		return false, nil
	}
	if hasGit {
		writes, err := c.writesGitInternals(commands)
		if err != nil || writes {
			return false, err
		}
	}
	for _, part := range commands {
		safe, err := c.security(part)
		if err != nil || !safe {
			return false, err
		}
		readOnly, err := c.commandReadOnly(part)
		if err != nil || !readOnly {
			return false, err
		}
	}
	return true, nil
}
func (c *RecordedCommandClassifier) String() string {
	return fmt.Sprintf("Source recorded Bash classifier (%s)", c.Platform)
}
