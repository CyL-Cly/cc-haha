package powershellpolicy

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed path_configs.json
var pathConfigsJSON []byte

type PathConfig struct {
	OperationType      string   `json:"operationType"`
	PathParams         []string `json:"pathParams"`
	KnownSwitches      []string `json:"knownSwitches"`
	KnownValueParams   []string `json:"knownValueParams"`
	LeafOnlyPathParams []string `json:"leafOnlyPathParams,omitempty"`
	PositionalSkip     int      `json:"positionalSkip,omitempty"`
	OptionalWrite      bool     `json:"optionalWrite,omitempty"`
}
type ExtractedPaths struct {
	Paths                   []string `json:"paths"`
	OperationType           string   `json:"operationType"`
	HasUnvalidatablePathArg bool     `json:"hasUnvalidatablePathArg"`
	OptionalWrite           bool     `json:"optionalWrite"`
}

var commonSwitches = []string{"-verbose", "-debug"}
var commonValues = []string{"-erroraction", "-warningaction", "-informationaction", "-progressaction", "-errorvariable", "-warningvariable", "-informationvariable", "-outvariable", "-outbuffer", "-pipelinevariable"}

func PathConfigs() (map[string]PathConfig, error) {
	var configs map[string]PathConfig
	err := json.Unmarshal(pathConfigsJSON, &configs)
	return configs, err
}
func matchesParameter(parameter string, parameters []string) bool {
	for _, p := range parameters {
		if p == parameter || len(parameter) > 1 && strings.HasPrefix(p, parameter) {
			return true
		}
	}
	return false
}
func complexColonValue(value string) bool {
	return strings.Contains(value, ",") || strings.HasPrefix(value, "(") || strings.HasPrefix(value, "[") || strings.Contains(value, "`") || strings.Contains(value, "@(") || strings.HasPrefix(value, "@{") || strings.Contains(value, "$")
}
func ExtractPaths(command Command, configs map[string]PathConfig) ExtractedPaths {
	config, exists := configs[ResolveCanonical(command.Name)]
	result := ExtractedPaths{Paths: []string{}, OperationType: "read"}
	if !exists {
		return result
	}
	result.OperationType = config.OperationType
	result.OptionalWrite = config.OptionalWrite
	switches := append(append([]string{}, config.KnownSwitches...), commonSwitches...)
	values := append(append([]string{}, config.KnownValueParams...), commonValues...)
	checkType := func(index int) {
		kind := argType(command, index+1)
		if kind != "" && kind != "StringConstant" && kind != "Parameter" {
			result.HasUnvalidatablePathArg = true
		}
	}
	positionals := 0
	for i := 0; i < len(command.Args); i++ {
		arg := command.Args[i]
		if arg == "" {
			continue
		}
		if IsParameter(arg, argType(command, i+1)) {
			normalized := arg
			for _, prefix := range []string{"-", "–", "—", "―"} {
				if strings.HasPrefix(arg, prefix) {
					normalized = "-" + strings.TrimPrefix(arg, prefix)
					break
				}
			}
			name, value, colon := strings.Cut(normalized, ":")
			lower := jsLower(name)
			pathParam := matchesParameter(lower, config.PathParams)
			leafParam := matchesParameter(lower, config.LeafOnlyPathParams)
			if pathParam || leafParam {
				found := colon
				if colon {
					if complexColonValue(value) {
						result.HasUnvalidatablePathArg = true
						found = false
					}
				} else if i+1 < len(command.Args) && command.Args[i+1] != "" && !IsParameter(command.Args[i+1], argType(command, i+2)) {
					value = command.Args[i+1]
					checkType(i + 1)
					i++
					found = true
				}
				if found && value != "" {
					if leafParam && (strings.ContainsAny(value, `\/`) || value == "." || value == "..") {
						result.HasUnvalidatablePathArg = true
					} else {
						result.Paths = append(result.Paths, value)
					}
				}
			} else if matchesParameter(lower, switches) {
			} else if matchesParameter(lower, values) {
				if colon {
					if complexColonValue(value) {
						result.HasUnvalidatablePathArg = true
					}
				} else if i+1 < len(command.Args) && command.Args[i+1] != "" && !IsParameter(command.Args[i+1], argType(command, i+2)) {
					checkType(i + 1)
					i++
				}
			} else {
				result.HasUnvalidatablePathArg = true
				if colon && !complexColonValue(value) {
					result.Paths = append(result.Paths, value)
				}
			}
			continue
		}
		if positionals < config.PositionalSkip {
			positionals++
			continue
		}
		positionals++
		checkType(i)
		result.Paths = append(result.Paths, arg)
	}
	return result
}
