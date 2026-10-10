// Package cliargs adapts the existing Commander registration and parse contract
// to Cobra. It never runs business handlers or reads configuration.
package cliargs

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

//go:embed spec.json
var specification []byte

type Argument struct {
	Name       string `json:"name"`
	Required   bool   `json:"required"`
	Variadic   bool   `json:"variadic"`
	HasDefault bool   `json:"hasDefault"`
	Default    any    `json:"default"`
	ParserKind string `json:"parserKind"`
}

type Option struct {
	Flags             string         `json:"flags"`
	Name              string         `json:"name"`
	Attribute         string         `json:"attribute"`
	Description       string         `json:"description"`
	Short             *string        `json:"short"`
	Long              *string        `json:"long"`
	Required          bool           `json:"required"`
	Optional          bool           `json:"optional"`
	Variadic          bool           `json:"variadic"`
	Negate            bool           `json:"negate"`
	Mandatory         bool           `json:"mandatory"`
	Hidden            bool           `json:"hidden"`
	HasDefault        bool           `json:"hasDefault"`
	Default           any            `json:"default"`
	Preset            any            `json:"preset"`
	Choices           []string       `json:"choices"`
	Environment       *string        `json:"environment"`
	Conflicts         []string       `json:"conflicts"`
	Implies           map[string]any `json:"implies"`
	ParserKind        string         `json:"parserKind"`
	RegistrationIndex int            `json:"registrationIndex"`
}

type CommandSpec struct {
	Name                 string         `json:"name"`
	Aliases              []string       `json:"aliases"`
	Description          string         `json:"description"`
	Usage                string         `json:"usage"`
	Version              *string        `json:"version"`
	Hidden               bool           `json:"hidden"`
	HasAction            bool           `json:"hasAction"`
	HelpText             string         `json:"helpText"`
	HelpOption           string         `json:"helpOption"`
	PositionalOptions    bool           `json:"positionalOptions"`
	PassThroughOptions   bool           `json:"passThroughOptions"`
	AllowUnknownOption   bool           `json:"allowUnknownOption"`
	AllowExcessArguments bool           `json:"allowExcessArguments"`
	Arguments            []Argument     `json:"arguments"`
	Options              []Option       `json:"options"`
	Commands             []*CommandSpec `json:"commands"`
	RegistrationIndex    int            `json:"registrationIndex"`
}

type Spec struct {
	SchemaVersion int                     `json:"schemaVersion"`
	Profiles      map[string]*CommandSpec `json:"profiles"`
}

// Specification returns the independently captured TS registration resource.
// Tests compare it with the guarded production TS tree, not Go-generated output.
func Specification() []byte { return append([]byte(nil), specification...) }

func LoadSpecification(data []byte) (*Spec, error) {
	var spec Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, err
	}
	if spec.SchemaVersion != 1 || len(spec.Profiles) != 4 {
		return nil, fmt.Errorf("unsupported CLI registration specification")
	}
	for _, name := range []string{"normal", "bare", "print", "xaa"} {
		if spec.Profiles[name] == nil {
			return nil, fmt.Errorf("missing CLI registration profile %s", name)
		}
		if err := validateSpec(spec.Profiles[name]); err != nil {
			return nil, err
		}
	}
	return &spec, nil
}

func validateSpec(command *CommandSpec) error {
	if command.Name == "" || command.HelpText == "" || command.HelpOption != "-h, --help" {
		return fmt.Errorf("incomplete CLI command registration")
	}
	kinds := map[string]bool{"none": true, "string": true, "number": true, "boolean": true, "true": true, "optional-or-true": true, "choices": true, "collect": true, "finite-number": true, "effort": true, "positive-number": true, "positive-integer": true}
	names := map[string]bool{}
	for _, option := range command.Options {
		if option.Environment != nil || len(option.Conflicts) > 0 || len(option.Implies) > 0 || option.Preset != nil {
			return fmt.Errorf("unsupported CLI option environment/conflict/implied/preset registration: %s", option.Flags)
		}
		if option.Attribute == "" || option.Name == "" || option.Long == nil && option.Short == nil || !kinds[option.ParserKind] {
			return fmt.Errorf("unsupported CLI option registration: %s", option.Flags)
		}
		if option.ParserKind == "choices" && len(option.Choices) == 0 {
			return fmt.Errorf("missing CLI option choices: %s", option.Flags)
		}
		for _, flag := range []*string{option.Long, option.Short} {
			if flag != nil {
				if names[*flag] {
					return fmt.Errorf("duplicate CLI flag %s", *flag)
				}
				names[*flag] = true
			}
		}
	}
	for _, argument := range command.Arguments {
		if argument.Name == "" || argument.ParserKind != "none" && argument.ParserKind != "string" {
			return fmt.Errorf("unsupported CLI argument registration")
		}
	}
	sort.SliceStable(command.Options, func(i, j int) bool {
		return command.Options[i].RegistrationIndex < command.Options[j].RegistrationIndex
	})
	for _, child := range command.Commands {
		if child == nil {
			return fmt.Errorf("missing CLI child registration")
		}
		if err := validateSpec(child); err != nil {
			return err
		}
	}
	sort.SliceStable(command.Commands, func(i, j int) bool {
		return command.Commands[i].RegistrationIndex < command.Commands[j].RegistrationIndex
	})
	return nil
}

type binding struct {
	value   any
	boolean bool
}

func (b *binding) String() string {
	if b.boolean && b.value == nil {
		return "false"
	}
	data, _ := json.Marshal(b.value)
	return string(data)
}
func (b *binding) Set(value string) error {
	if b.boolean {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		b.value = parsed
		return nil
	}
	b.value = value
	return nil
}
func (b *binding) Type() string {
	if b.boolean {
		return "bool"
	}
	return "commander"
}

type commandNode struct {
	spec     *CommandSpec
	command  *cobra.Command
	parent   *commandNode
	children []*commandNode
	values   map[string]any
	provided []string
}

func buildCommand(spec *CommandSpec, parent *commandNode) *commandNode {
	node := &commandNode{spec: spec, parent: parent, values: map[string]any{}}
	command := &cobra.Command{Use: strings.TrimSpace(spec.Name + " " + spec.Usage), Aliases: append([]string(nil), spec.Aliases...), Short: spec.Description, Hidden: spec.Hidden, DisableFlagParsing: true, SilenceErrors: true, SilenceUsage: true}
	if spec.Version != nil {
		command.Version = *spec.Version
	}
	command.CompletionOptions.DisableDefaultCmd = true
	command.SetHelpCommand(&cobra.Command{Use: "__commander_help_disabled", Hidden: true})
	command.SetHelpFunc(func(c *cobra.Command, _ []string) { fmt.Fprint(c.OutOrStdout(), spec.HelpText) })
	if spec.HasAction {
		command.RunE = func(*cobra.Command, []string) error { return errGuardedAction }
	}
	node.command = command
	for _, option := range spec.Options {
		if option.HasDefault {
			node.values[option.Attribute] = cloneValue(option.Default)
		}
		if option.Negate && !hasPositiveFlag(spec, option) {
			if !option.HasDefault {
				node.values[option.Attribute] = true
			}
		}
		flags := []*string{option.Long}
		if option.Long == nil {
			flags = []*string{option.Short}
		} else if option.Short != nil && strings.HasPrefix(*option.Short, "--") {
			flags = append(flags, option.Short)
		}
		for index, flag := range flags {
			if flag == nil {
				continue
			}
			name := strings.TrimLeft(*flag, "-")
			short := ""
			if index == 0 && option.Short != nil && len(*option.Short) == 2 {
				short = (*option.Short)[1:]
			}
			// Cobra checks its reserved version flag even with DisableFlagParsing.
			// Keep it a false bool until the compatibility parser handles version.
			value := &binding{value: node.values[option.Attribute], boolean: option.Name == "version"}
			pflagValue := &pflag.Flag{Name: name, Shorthand: short, Usage: option.Description, Hidden: option.Hidden, Value: value, DefValue: value.String()}
			if option.Optional || !option.Required {
				pflagValue.NoOptDefVal = "true"
			}
			command.Flags().AddFlag(pflagValue)
		}
	}
	for _, childSpec := range spec.Commands {
		child := buildCommand(childSpec, node)
		node.children = append(node.children, child)
		command.AddCommand(child.command)
	}
	return node
}

func hasPositiveFlag(spec *CommandSpec, option Option) bool {
	if option.Long == nil {
		return false
	}
	positive := strings.Replace(*option.Long, "--no-", "--", 1)
	for _, candidate := range spec.Options {
		if candidate.Long != nil && *candidate.Long == positive {
			return true
		}
	}
	return false
}

func cloneValue(value any) any {
	if list, ok := value.([]any); ok {
		return append([]any{}, list...)
	}
	return value
}

// NewCommand builds the real Cobra tree and its aliases/flags. Commander value
// conversion and token consumption are handled before Cobra's guarded dispatch.
func NewCommand(profile string) (*cobra.Command, error) {
	spec, err := LoadSpecification(specification)
	if err != nil {
		return nil, err
	}
	if spec.Profiles[profile] == nil {
		return nil, fmt.Errorf("unknown CLI profile %s", profile)
	}
	return buildCommand(spec.Profiles[profile], nil).command, nil
}
