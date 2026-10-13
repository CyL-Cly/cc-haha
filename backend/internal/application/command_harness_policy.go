package application

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"math"
	"strconv"
)

func applyPublicHarnessPolicyArguments(options map[string]any, arguments *clibootstrap.PrintArguments) error {
	if value, ok := options["maxBudgetUsd"].(float64); ok {
		arguments.MaxBudgetUSD = &value
	}
	if value, ok := options["taskBudget"].(float64); ok {
		arguments.TaskBudget = &value
	}
	arguments.FallbackModel, _ = options["fallbackModel"].(string)
	arguments.Workload, _ = options["workload"].(string)
	arguments.EnableAuthStatus, _ = options["enableAuthStatus"].(bool)
	if arguments.FallbackModel != "" && arguments.Model != "" && arguments.FallbackModel == arguments.Model {
		return &commandValidationError{output: "Error: Fallback model cannot be the same as the main model. Please specify a different model for --fallback-model.\n"}
	}
	return nil
}

func publicBudgetNumber(value float64) string {
	if math.IsInf(value, 1) {
		return "Infinity"
	}
	quoted, _ := protocol.JavaScriptStringJSON(json.RawMessage(strconv.FormatFloat(value, 'g', -1, 64)))
	var text string
	_ = json.Unmarshal(quoted, &text)
	return text
}
