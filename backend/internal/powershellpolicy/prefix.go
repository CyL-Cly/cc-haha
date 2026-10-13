package powershellpolicy

import (
	_ "embed"
	"encoding/json"
	"strings"
)

// These are inert Source registry/NEVER_SUGGEST data. The frozen dependency
// lock has no @withfig/autocomplete runtime package, so dynamic spec imports
// resolve to null; the seven built-in registry entries remain available.
// The paired Source corpus verifies both the data and dynamic null profiles.
//
//go:embed prefix_specs.json
var prefixSpecsJSON []byte

//go:embed never_suggest.json
var neverSuggestJSON []byte

type prefixArgument struct {
	IsCommand   bool            `json:"isCommand"`
	IsModule    json.RawMessage `json:"isModule"`
	IsOptional  bool            `json:"isOptional"`
	IsVariadic  bool            `json:"isVariadic"`
	IsDangerous bool            `json:"isDangerous"`
}
type prefixOption struct {
	Name arrayOrOne[string]         `json:"name"`
	Args arrayOrOne[prefixArgument] `json:"args"`
}
type prefixSpec struct {
	Name        arrayOrOne[string] `json:"name"`
	Args        json.RawMessage    `json:"args"`
	Options     []prefixOption     `json:"options"`
	Subcommands []prefixSpec       `json:"subcommands"`
}
type PrefixCatalog struct {
	specs map[string]*prefixSpec
	never map[string]bool
}

func NewPrefixCatalog() (*PrefixCatalog, error) {
	var specs []prefixSpec
	var never []string
	if err := json.Unmarshal(prefixSpecsJSON, &specs); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(neverSuggestJSON, &never); err != nil {
		return nil, err
	}
	c := &PrefixCatalog{specs: map[string]*prefixSpec{}, never: map[string]bool{}}
	for i := range specs {
		for _, name := range specs[i].Name {
			c.specs[name] = &specs[i]
		}
	}
	for _, name := range never {
		c.never[name] = true
	}
	return c, nil
}

var prefixDepth = map[string]int{"rg": 2, "pre-commit": 2, "gcloud": 4, "gcloud compute": 6, "gcloud beta": 6, "aws": 4, "az": 4, "kubectl": 3, "docker": 3, "dotnet": 3, "git push": 2}

func prefixArguments(spec *prefixSpec) []prefixArgument {
	if spec == nil {
		return nil
	}
	var args arrayOrOne[prefixArgument]
	_ = json.Unmarshal(spec.Args, &args)
	return args
}
func optionFor(spec *prefixSpec, flag string) *prefixOption {
	if spec != nil {
		for i := range spec.Options {
			for _, name := range spec.Options[i].Name {
				if name == flag {
					return &spec.Options[i]
				}
			}
		}
	}
	return nil
}
func argumentCommandOrModule(a prefixArgument) bool {
	return a.IsCommand || len(a.IsModule) > 0 && string(a.IsModule) != "false" && string(a.IsModule) != "null" && string(a.IsModule) != `""`
}
func knownSubcommand(arg string, spec *prefixSpec) *prefixSpec {
	if spec != nil {
		for i := range spec.Subcommands {
			for _, name := range spec.Subcommands[i].Name {
				if jsEqual(arg, name) {
					return &spec.Subcommands[i]
				}
			}
		}
	}
	return nil
}
func flagTakesArg(flag, next string, spec *prefixSpec) bool {
	if opt := optionFor(spec, flag); opt != nil {
		return opt.Args != nil
	}
	return spec != nil && len(spec.Subcommands) > 0 && next != "" && !strings.HasPrefix(next, "-") && knownSubcommand(next, spec) == nil
}
func firstSubcommand(args []string, spec *prefixSpec) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "" {
			continue
		}
		if strings.HasPrefix(a, "-") {
			next := ""
			if i+1 < len(args) {
				next = args[i+1]
			}
			if flagTakesArg(a, next, spec) {
				i++
			}
			continue
		}
		if spec == nil || len(spec.Subcommands) == 0 || knownSubcommand(a, spec) != nil {
			return a
		}
	}
	return ""
}
func calculatePrefixDepth(command string, args []string, spec *prefixSpec) int {
	name := jsLower(command)
	first := firstSubcommand(args, spec)
	key := name
	if first != "" {
		key += " " + jsLower(first)
	}
	if depth := prefixDepth[key]; depth != 0 {
		return depth
	}
	if depth := prefixDepth[name]; depth != 0 {
		return depth
	}
	if spec == nil {
		return 2
	}
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			if opt := optionFor(spec, arg); opt != nil {
				for _, a := range opt.Args {
					if argumentCommandOrModule(a) {
						return 3
					}
				}
			}
		}
	}
	if sub := knownSubcommand(first, spec); first != "" && sub != nil {
		for _, a := range prefixArguments(sub) {
			if a.IsCommand {
				return 3
			}
			if a.IsVariadic {
				return 2
			}
		}
		if len(sub.Subcommands) > 0 {
			return 4
		}
		if len(sub.Args) == 0 || string(sub.Args) == "null" {
			return 2
		}
		return 3
	}
	argsSpec := prefixArguments(spec)
	for i, a := range argsSpec {
		if a.IsCommand {
			if len(spec.Args) > 0 && spec.Args[0] != '[' {
				return 2
			}
			return min(2+i, 3)
		}
	}
	if len(spec.Subcommands) == 0 {
		for _, a := range argsSpec {
			if a.IsVariadic {
				return 1
			}
		}
		if len(argsSpec) > 0 && !argsSpec[0].IsOptional {
			return 2
		}
	}
	for _, a := range argsSpec {
		if a.IsDangerous {
			return 3
		}
	}
	return 2
}
func shouldStopPrefixArg(arg string, previous []string, spec *prefixSpec) bool {
	if strings.HasPrefix(arg, "-") {
		return true
	}
	dot := strings.LastIndex(arg, ".")
	extension := dot > 0 && dot < len(arg)-1 && !strings.Contains(arg[dot+1:], ":")
	file := strings.Contains(arg, "/") || extension
	url := strings.HasPrefix(arg, "http://") || strings.HasPrefix(arg, "https://") || strings.HasPrefix(arg, "ftp://")
	if !file && !url {
		return false
	}
	if len(previous) > 0 && previous[len(previous)-1] == "-m" {
		if opt := optionFor(spec, "-m"); opt != nil {
			for _, a := range opt.Args {
				if len(a.IsModule) > 0 && string(a.IsModule) != "false" && string(a.IsModule) != "null" && string(a.IsModule) != `""` {
					return false
				}
			}
		}
	}
	return true
}
func buildPrefix(command string, args []string, spec *prefixSpec) string {
	depth := calculatePrefixDepth(command, args, spec)
	parts := []string{command}
	hasSubs := spec != nil && len(spec.Subcommands) > 0
	found := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "" || len(parts) >= depth {
			break
		}
		if strings.HasPrefix(a, "-") {
			if a == "-c" && (jsEqual(command, "python") || jsEqual(command, "python3")) {
				break
			}
			opt := optionFor(spec, a)
			special := false
			if opt != nil {
				for _, arg := range opt.Args {
					special = special || argumentCommandOrModule(arg)
				}
			}
			if special {
				parts = append(parts, a)
				continue
			}
			if hasSubs && !found {
				next := ""
				if i+1 < len(args) {
					next = args[i+1]
				}
				if flagTakesArg(a, next, spec) {
					i++
				}
				continue
			}
			break
		}
		if shouldStopPrefixArg(a, args[:i], spec) {
			break
		}
		if hasSubs && !found {
			found = knownSubcommand(a, spec) != nil
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}
func (c *PrefixCatalog) ElementPrefix(command Command) *string {
	if command.NameType == "application" || command.Name == "" || c.never[jsLower(command.Name)] {
		return nil
	}
	if command.NameType == "cmdlet" {
		value := command.Name
		return &value
	}
	if argType(command, 0) != "StringConstant" {
		return nil
	}
	for i := range command.Args {
		kind := argType(command, i+1)
		if kind != "StringConstant" && kind != "Parameter" {
			return nil
		}
	}
	nameLower := jsLower(command.Name)
	spec := c.specs[nameLower]
	prefix := buildPrefix(command.Name, command.Args, spec)
	argIndex := 0
	for _, word := range strings.Split(prefix, " ")[1:] {
		if strings.Contains(word, `\`) {
			return nil
		}
		for argIndex < len(command.Args) {
			a := command.Args[argIndex]
			if a == word {
				break
			}
			if strings.HasPrefix(a, "-") {
				argIndex++
				if spec != nil && argIndex < len(command.Args) && command.Args[argIndex] != word && !strings.HasPrefix(command.Args[argIndex], "-") {
					if opt := optionFor(spec, jsLower(a)); opt != nil && opt.Args != nil {
						argIndex++
					}
				}
				continue
			}
			return nil
		}
		if argIndex >= len(command.Args) {
			return nil
		}
		argIndex++
	}
	if !strings.Contains(prefix, " ") && (spec != nil && len(spec.Subcommands) > 0 || prefixDepth[nameLower] != 0) {
		return nil
	}
	return &prefix
}

type PrefixResult struct {
	CommandPrefix *string `json:"commandPrefix"`
}

func (c *PrefixCatalog) First(parsed Parsed) *PrefixResult {
	if !parsed.Valid {
		return nil
	}
	for _, command := range AllCommands(parsed) {
		if command.ElementType == "CommandAst" {
			return &PrefixResult{CommandPrefix: c.ElementPrefix(command)}
		}
	}
	return &PrefixResult{}
}
func wordAlignedPrefix(values []string) string {
	if len(values) == 0 {
		return ""
	}
	first := strings.Split(values[0], " ")
	count := len(first)
	for _, value := range values[1:] {
		words := strings.Split(value, " ")
		matched := 0
		for matched < count && matched < len(words) && jsEqual(words[matched], first[matched]) {
			matched++
		}
		count = matched
		if count == 0 {
			break
		}
	}
	return strings.Join(first[:count], " ")
}
func (c *PrefixCatalog) Compound(parsed Parsed, exclude func(Command) bool) []string {
	result := []string{}
	if !parsed.Valid {
		return result
	}
	commands := []Command{}
	for _, command := range AllCommands(parsed) {
		if command.ElementType == "CommandAst" {
			commands = append(commands, command)
		}
	}
	if len(commands) <= 1 {
		if len(commands) == 1 {
			if prefix := c.ElementPrefix(commands[0]); prefix != nil {
				result = append(result, *prefix)
			}
		}
		return result
	}
	keys := []string{}
	groups := map[string][]string{}
	for _, command := range commands {
		if exclude != nil && exclude(command) {
			continue
		}
		prefix := c.ElementPrefix(command)
		if prefix == nil {
			continue
		}
		key := jsLower(strings.Split(*prefix, " ")[0])
		if _, ok := groups[key]; !ok {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], *prefix)
	}
	for _, key := range keys {
		lcp := wordAlignedPrefix(groups[key])
		if !strings.Contains(lcp, " ") {
			spec := c.specs[key]
			if spec != nil && len(spec.Subcommands) > 0 || prefixDepth[key] != 0 {
				continue
			}
		}
		result = append(result, lcp)
	}
	return result
}
