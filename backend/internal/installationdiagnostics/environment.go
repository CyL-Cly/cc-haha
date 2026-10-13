package installationdiagnostics

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type EnvironmentValidation struct {
	Effective float64 `json:"effective"`
	Status    string  `json:"status"`
	Message   string  `json:"message,omitempty"`
}

var decimalPrefix = regexp.MustCompile(`^[+-]?[0-9]+`)

func ValidateEnvironment(value string, defaultValue, upperLimit float64) EnvironmentValidation {
	if value == "" {
		return EnvironmentValidation{Effective: defaultValue, Status: "valid"}
	}
	prefix := decimalPrefix.FindString(protocol.TrimJavaScriptString(value))
	parsed := math.NaN()
	if prefix != "" {
		parsed, _ = strconv.ParseFloat(prefix, 64)
	}
	if math.IsNaN(parsed) || parsed <= 0 {
		return EnvironmentValidation{defaultValue, "invalid", fmt.Sprintf("Invalid value \"%s\" (using default: %s)", value, jsNumber(defaultValue))}
	}
	if parsed > upperLimit {
		return EnvironmentValidation{upperLimit, "capped", "Capped from " + jsNumber(parsed) + " to " + jsNumber(upperLimit)}
	}
	return EnvironmentValidation{Effective: parsed, Status: "valid"}
}
func jsNumber(value float64) string {
	if math.IsInf(value, 1) {
		return "Infinity"
	}
	text := strconv.FormatFloat(value, 'g', -1, 64)
	if value >= 1e-6 && value < 1e21 {
		text = strconv.FormatFloat(value, 'f', -1, 64)
	}
	text = strings.ReplaceAll(text, "e+0", "e+")
	text = strings.ReplaceAll(text, "e-0", "e-")
	return text
}
func EnvironmentWarnings(environment map[string]string) []Warning {
	result := []Warning{}
	for _, row := range []struct {
		name     string
		def, max float64
	}{{"BASH_MAX_OUTPUT_LENGTH", 30000, 150000}, {"TASK_MAX_OUTPUT_LENGTH", 32000, 160000}, {"CLAUDE_CODE_MAX_OUTPUT_TOKENS", 64000, 128000}} {
		validation := ValidateEnvironment(environment[row.name], row.def, row.max)
		if validation.Status != "valid" {
			result = append(result, Warning{Issue: row.name + ": " + validation.Message, Fix: ""})
		}
	}
	return result
}
