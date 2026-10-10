package cliargs

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

var errGuardedAction = errors.New("CLI business handlers are not migrated")
var negativeNumber = regexp.MustCompile(`^-(\d+|\d*\.\d+)(e[+-]?\d+)?$`)
var decimalNumber = regexp.MustCompile(`^[+-]?(?:(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?|Infinity)$`)
var integerEffort = regexp.MustCompile(`^-?\d+$`)

type Config struct {
	Version string
	XAA     bool
}
type Parsed struct {
	Path          string         `json:"path"`
	Args          []string       `json:"args"`
	ProcessedArgs []any          `json:"processedArgs"`
	Options       map[string]any `json:"options"`
	Globals       map[string]any `json:"globals"`
	// Handler admission consumes the parser's actual option matches; operands
	// and required values must not be reparsed as flags. This is host metadata.
	ProvidedOptions []string `json:"-"`
}
type Diagnostic struct {
	Kind          string `json:"kind"`
	Message       string `json:"message"`
	StdioComplete bool   `json:"stdioComplete"`
}
type Result struct {
	ExitCode   int         `json:"exitCode"`
	Stdout     string      `json:"stdout"`
	Stderr     string      `json:"stderr"`
	Parsed     *Parsed     `json:"parsed"`
	Diagnostic *Diagnostic `json:"diagnostic,omitempty"`
}

// Parse performs only registration, help/version and pre-action argument work.
// Exit 86 is a fail-closed handler boundary; this API does not implement CLI
// business behavior. Raw Error stack/source excerpts remain a separate pending
// stdio contract and are explicitly marked incomplete.
func Parse(args []string, config Config) Result {
	if config.Version == "" {
		config.Version = "999.0.0-local"
	}
	if len(args) == 1 && (args[0] == "--version" || args[0] == "-v" || args[0] == "-V") {
		return Result{Stdout: config.Version + " (Claude Code)\n"}
	}
	spec, err := LoadSpecification(specification)
	if err != nil {
		return errorResult(err.Error())
	}
	profile := "normal"
	if config.XAA {
		profile = "xaa"
	}
	for _, arg := range args {
		if arg == "--print" || arg == "-p" {
			profile = "print"
			break
		}
	}
	node := buildCommand(spec.Profiles[profile], nil)
	parser := &commandParser{root: node, version: config.Version}
	return parser.command(node, []string{}, append([]string{}, args...))
}

type commandParser struct {
	root    *commandNode
	version string
}
type tokenResult struct {
	operands, unknown []string
	terminal          *Result
}

func errorResult(message string) Result { return Result{ExitCode: 1, Stderr: message + "\n"} }
func helpResult(node *commandNode, isError bool) Result {
	if isError {
		return Result{ExitCode: 1, Stderr: node.spec.HelpText}
	}
	return Result{Stdout: node.spec.HelpText}
}

func (p *commandParser) command(node *commandNode, operands, unknown []string) Result {
	tokens := p.tokens(node, unknown)
	if tokens.terminal != nil {
		return *tokens.terminal
	}
	operands = append(operands, tokens.operands...)
	unknown = tokens.unknown
	allArgs := append(append([]string{}, operands...), unknown...)
	if len(operands) > 0 {
		if child := node.child(operands[0]); child != nil {
			return p.command(child, operands[1:], unknown)
		}
		if operands[0] == "help" && node.implicitHelp() {
			if len(operands) == 1 {
				return helpResult(node, false)
			}
			if child := node.child(operands[1]); child != nil {
				return helpResult(child, false)
			}
			return helpResult(node, true)
		}
	}
	if len(node.children) > 0 && len(allArgs) == 0 && !node.spec.HasAction {
		return helpResult(node, true)
	}
	for _, arg := range unknown {
		if arg == "-h" || arg == "--help" {
			return helpResult(node, false)
		}
	}
	for ancestor := node; ancestor != nil; ancestor = ancestor.parent {
		for _, option := range ancestor.spec.Options {
			if option.Mandatory {
				if _, ok := ancestor.values[option.Attribute]; !ok {
					return errorResult(fmt.Sprintf("error: required option '%s' not specified", option.Flags))
				}
			}
		}
	}
	if !node.spec.HasAction && len(operands) > 0 && len(node.children) > 0 {
		candidates := []string{}
		for _, child := range node.children {
			if !child.spec.Hidden {
				candidates = append(candidates, child.spec.Name)
				if len(child.spec.Aliases) > 0 {
					candidates = append(candidates, child.spec.Aliases[0])
				}
			}
		}
		if node.implicitHelp() {
			candidates = append(candidates, "help")
		}
		return errorResult(fmt.Sprintf("error: unknown command '%s'%s", allArgs[0], suggestSimilar(allArgs[0], candidates)))
	}
	if len(unknown) > 0 && !node.spec.AllowUnknownOption {
		candidates := []string{"--help"}
		for _, option := range node.spec.Options {
			if !option.Hidden && option.Long != nil {
				candidates = append(candidates, *option.Long)
			}
		}
		suggestion := ""
		if strings.HasPrefix(unknown[0], "--") {
			suggestion = suggestSimilar(unknown[0], candidates)
		}
		return errorResult(fmt.Sprintf("error: unknown option '%s'%s", unknown[0], suggestion))
	}
	for index, argument := range node.spec.Arguments {
		if argument.Required && index >= len(allArgs) {
			return errorResult(fmt.Sprintf("error: missing required argument '%s'", argument.Name))
		}
	}
	variadic := len(node.spec.Arguments) > 0 && node.spec.Arguments[len(node.spec.Arguments)-1].Variadic
	if !variadic && !node.spec.AllowExcessArguments && len(allArgs) > len(node.spec.Arguments) {
		plural := "s"
		if len(node.spec.Arguments) == 1 {
			plural = ""
		}
		subcommand := ""
		if node.parent != nil {
			subcommand = " for '" + node.spec.Name + "'"
		}
		return errorResult(fmt.Sprintf("error: too many arguments%s. Expected %d argument%s but got %d.", subcommand, len(node.spec.Arguments), plural, len(allArgs)))
	}
	processed := make([]any, len(node.spec.Arguments))
	for index, argument := range node.spec.Arguments {
		if argument.HasDefault {
			processed[index] = cloneValue(argument.Default)
		}
		if argument.Variadic {
			values := []string{}
			if index < len(allArgs) {
				values = append(values, allArgs[index:]...)
			}
			processed[index] = values
		} else if index < len(allArgs) {
			processed[index] = allArgs[index]
		}
	}
	if !node.spec.HasAction {
		return Result{}
	}
	path := []string{}
	globals := map[string]any{}
	for ancestor := node; ancestor != nil; ancestor = ancestor.parent {
		path = append([]string{ancestor.spec.Name}, path...)
		for key, value := range ancestor.values {
			globals[key] = sanitize(value)
		}
	}
	options := map[string]any{}
	provided := []string{}
	for ancestor := node; ancestor != nil; ancestor = ancestor.parent {
		provided = append(provided, ancestor.provided...)
	}
	for key, value := range node.values {
		options[key] = sanitize(value)
	}
	// The actual Cobra dispatch must reach the already resolved command. Its
	// handlers are guards; no default help/completion or pflag parser may alter
	// the Commander result.
	p.root.command.SetArgs(append([]string{}, path[1:]...))
	selected, dispatchErr := p.root.command.ExecuteC()
	if !errors.Is(dispatchErr, errGuardedAction) || selected != node.command {
		return errorResult("CLI Cobra dispatch did not reach the guarded command")
	}
	return Result{ExitCode: 86, Parsed: &Parsed{Path: strings.Join(path, " "), Args: allArgs, ProcessedArgs: processed, Options: options, Globals: globals, ProvidedOptions: provided}}
}

func (node *commandNode) child(name string) *commandNode {
	for _, child := range node.children {
		if child.spec.Name == name {
			return child
		}
		for _, alias := range child.spec.Aliases {
			if alias == name {
				return child
			}
		}
	}
	return nil
}
func (node *commandNode) implicitHelp() bool { return len(node.children) > 0 && !node.spec.HasAction }
func (node *commandNode) option(flag string) *Option {
	for index := range node.spec.Options {
		option := &node.spec.Options[index]
		if option.Short != nil && *option.Short == flag || option.Long != nil && *option.Long == flag {
			return option
		}
	}
	return nil
}
func maybeOption(value string) bool { return len(value) > 1 && value[0] == '-' }

func (p *commandParser) tokens(node *commandNode, args []string) tokenResult {
	result := tokenResult{operands: []string{}, unknown: []string{}}
	unknownDestination := false
	group := ""
	var active *Option
	push := func(values ...string) {
		if unknownDestination {
			result.unknown = append(result.unknown, values...)
		} else {
			result.operands = append(result.operands, values...)
		}
	}
	emit := func(option *Option, value *string, noOptionalValue bool) *Result {
		if option.Name == "version" {
			terminal := Result{Stdout: p.version + " (Claude Code)\n"}
			return &terminal
		}
		return p.emit(node, option, value, noOptionalValue)
	}
	for index := 0; index < len(args) || group != ""; {
		arg := group
		group = ""
		if arg == "" {
			arg = args[index]
			index++
		}
		if arg == "--" {
			if unknownDestination {
				push(arg)
			}
			push(args[index:]...)
			break
		}
		if active != nil && (!maybeOption(arg) || negativeNumber.MatchString(arg)) {
			if terminal := emit(active, &arg, false); terminal != nil {
				result.terminal = terminal
				return result
			}
			continue
		}
		active = nil
		if maybeOption(arg) {
			if option := node.option(arg); option != nil {
				var value *string
				optionalMissing := false
				if option.Required {
					if index == len(args) {
						terminal := errorResult(fmt.Sprintf("error: option '%s' argument missing", option.Flags))
						result.terminal = &terminal
						return result
					}
					value = &args[index]
					index++
				} else if option.Optional {
					optionalMissing = true
					if index < len(args) && (!maybeOption(args[index]) || negativeNumber.MatchString(args[index])) {
						value = &args[index]
						index++
						optionalMissing = false
					}
				}
				if terminal := emit(option, value, optionalMissing); terminal != nil {
					result.terminal = terminal
					return result
				}
				if option.Variadic {
					active = option
				}
				continue
			}
		}
		if len(arg) > 2 && arg[0] == '-' && arg[1] != '-' {
			if option := node.option(arg[:2]); option != nil {
				var value *string
				if option.Required || option.Optional {
					suffix := arg[2:]
					value = &suffix
				} else {
					group = "-" + arg[2:]
				}
				if terminal := emit(option, value, false); terminal != nil {
					result.terminal = terminal
					return result
				}
				continue
			}
		}
		if strings.HasPrefix(arg, "--") {
			if position := strings.IndexByte(arg, '='); position > 2 {
				if option := node.option(arg[:position]); option != nil && (option.Required || option.Optional) {
					value := arg[position+1:]
					if terminal := emit(option, &value, false); terminal != nil {
						result.terminal = terminal
						return result
					}
					continue
				}
			}
		}
		if !unknownDestination && maybeOption(arg) && !(len(node.children) == 0 && negativeNumber.MatchString(arg)) {
			unknownDestination = true
		}
		if (node.spec.PositionalOptions || node.spec.PassThroughOptions) && len(result.operands) == 0 && len(result.unknown) == 0 {
			if node.child(arg) != nil {
				result.operands = append(result.operands, arg)
				result.unknown = append(result.unknown, args[index:]...)
				break
			}
			if arg == "help" && node.implicitHelp() {
				result.operands = append(result.operands, arg)
				result.operands = append(result.operands, args[index:]...)
				break
			}
		}
		if node.spec.PassThroughOptions {
			push(append([]string{arg}, args[index:]...)...)
			break
		}
		push(arg)
	}
	return result
}

func (p *commandParser) emit(node *commandNode, option *Option, value *string, noOptionalValue bool) *Result {
	var parsed any
	if value != nil {
		parsed = *value
	}
	if !noOptionalValue {
		var problem *Result
		parsed, problem = parseValue(option, parsed, node.values[option.Attribute])
		if problem != nil {
			return problem
		}
	}
	if parsed == nil {
		if option.Negate {
			parsed = false
		} else if option.Optional || !option.Required {
			parsed = true
		} else {
			parsed = ""
		}
	}
	node.values[option.Attribute] = parsed
	if option.Long != nil {
		node.provided = append(node.provided, *option.Long)
	} else if option.Short != nil {
		node.provided = append(node.provided, *option.Short)
	}
	for _, flag := range []*string{option.Long, option.Short} {
		if flag != nil {
			bindingFlag := node.command.Flags().Lookup(strings.TrimLeft(*flag, "-"))
			if len(*flag) == 2 {
				bindingFlag = node.command.Flags().ShorthandLookup((*flag)[1:])
			}
			if bindingFlag != nil {
				bindingFlag.Changed = true
				bindingFlag.Value.(*binding).value = parsed
			}
		}
	}
	return nil
}

func parseValue(option *Option, value, previous any) (any, *Result) {
	text, _ := value.(string)
	invalid := func(message string) (any, *Result) {
		result := errorResult(fmt.Sprintf("error: option '%s' argument '%s' is invalid. %s", option.Flags, text, message))
		return nil, &result
	}
	rawError := func(message string) (any, *Result) {
		result := errorResult("error: " + message)
		result.Diagnostic = &Diagnostic{Kind: "raw-parser-error", Message: message, StdioComplete: false}
		return nil, &result
	}
	switch option.ParserKind {
	case "none":
		if option.Variadic && value != nil {
			values, _ := previous.([]any)
			return append(append([]any{}, values...), value), nil
		}
		return value, nil
	case "string":
		return text, nil
	case "true":
		return true, nil
	case "boolean":
		return value != nil && text != "", nil
	case "optional-or-true":
		if text == "" {
			return true, nil
		}
		return text, nil
	case "choices":
		for _, choice := range option.Choices {
			if text == choice {
				return text, nil
			}
		}
		return invalid("Allowed choices are " + strings.Join(option.Choices, ", ") + ".")
	case "collect":
		values, _ := previous.([]any)
		return append(append([]any{}, values...), value), nil
	case "number":
		return jsNumber(text), nil
	case "finite-number":
		number := jsNumber(text)
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, nil
		}
		return number, nil
	case "positive-number":
		number := jsNumber(text)
		if math.IsNaN(number) || number <= 0 {
			return rawError("--max-budget-usd must be a positive number greater than 0")
		}
		return number, nil
	case "positive-integer":
		number := jsNumber(text)
		if math.IsNaN(number) || math.IsInf(number, 0) || number <= 0 || math.Trunc(number) != number {
			return rawError("--task-budget must be a positive integer")
		}
		return number, nil
	case "effort":
		if strings.ToLower(text) == "ultracode" {
			return "ultracode", nil
		}
		normalized := strings.ToLower(jsTrim(text))
		for _, level := range []string{"low", "medium", "high", "xhigh", "max"} {
			if normalized == level {
				return level, nil
			}
		}
		if integerEffort.MatchString(normalized) {
			number := jsNumber(normalized)
			if !math.IsInf(number, 0) && math.Trunc(number) == number {
				return number, nil
			}
		}
		return invalid("It must be one of: low, medium, high, xhigh, max, ultracode, or an integer")
	}
	return invalid("Unsupported CLI parser contract")
}

func jsTrim(value string) string {
	return strings.TrimFunc(value, func(r rune) bool {
		switch r {
		case '\t', '\n', '\v', '\f', '\r', ' ', '\u00a0', '\u1680', '\u2028', '\u2029', '\u202f', '\u205f', '\u3000', '\ufeff':
			return true
		}
		return r >= '\u2000' && r <= '\u200a'
	})
}
func jsNumber(value string) float64 {
	value = jsTrim(value)
	if value == "" {
		return 0
	}
	if len(value) > 2 && value[0] == '0' {
		base := 0
		switch value[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 {
			if strings.HasPrefix(value[2:], "+") {
				return math.NaN()
			}
			number, ok := new(big.Int).SetString(value[2:], base)
			if !ok || number.Sign() < 0 {
				return math.NaN()
			}
			result, _ := new(big.Float).SetInt(number).Float64()
			return result
		}
	}
	if !decimalNumber.MatchString(value) {
		return math.NaN()
	}
	if value == "Infinity" || value == "+Infinity" {
		return math.Inf(1)
	}
	if value == "-Infinity" {
		return math.Inf(-1)
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil && !math.IsInf(number, 0) {
		return math.NaN()
	}
	if number == 0 {
		return 0
	}
	return number
}
func sanitize(value any) any {
	switch item := value.(type) {
	case float64:
		if math.IsNaN(item) || math.IsInf(item, 0) {
			return nil
		}
	case []any:
		result := make([]any, len(item))
		for index, value := range item {
			result[index] = sanitize(value)
		}
		return result
	}
	return value
}

func suggestSimilar(word string, candidates []string) string {
	option := strings.HasPrefix(word, "--")
	if option {
		word = word[2:]
	}
	best := 3
	matches := map[string]bool{}
	for _, candidate := range candidates {
		if option {
			candidate = strings.TrimPrefix(candidate, "--")
		}
		if len(candidate) <= 1 {
			continue
		}
		distance := editDistance(utf16.Encode([]rune(word)), utf16.Encode([]rune(candidate)))
		length := max(len(utf16.Encode([]rune(word))), len(utf16.Encode([]rune(candidate))))
		if float64(length-distance)/float64(length) <= 0.4 {
			continue
		}
		if distance < best {
			best = distance
			matches = map[string]bool{candidate: true}
		} else if distance == best {
			matches[candidate] = true
		}
	}
	list := []string{}
	for candidate := range matches {
		if option {
			candidate = "--" + candidate
		}
		list = append(list, candidate)
	}
	sort.Strings(list)
	if len(list) == 1 {
		return "\n(Did you mean " + list[0] + "?)"
	}
	if len(list) > 1 {
		return "\n(Did you mean one of " + strings.Join(list, ", ") + "?)"
	}
	return ""
}
func editDistance(a, b []uint16) int {
	if abs(len(a)-len(b)) > 3 {
		return max(len(a), len(b))
	}
	d := make([][]int, len(a)+1)
	for index := range d {
		d[index] = make([]int, len(b)+1)
		d[index][0] = index
	}
	for index := range d[0] {
		d[0][index] = index
	}
	for j := 1; j <= len(b); j++ {
		for i := 1; i <= len(a); i++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(a)][len(b)]
}
func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
