// Package cliauth renders the original general and OpenAI auth status contracts.
// Native command dispatch, credential mutation and initialization belong to
// clibootstrap.MainCommand and the existing shared storage producers.
package cliauth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Diagnostic struct {
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	Message       string `json:"message"`
	StdioComplete bool   `json:"stdioComplete"`
}

type Result struct {
	Stdout     string      `json:"stdout"`
	Stderr     string      `json:"stderr"`
	ExitCode   int         `json:"exitCode"`
	Diagnostic *Diagnostic `json:"diagnostic,omitempty"`
}

func incomplete(stdout, name, message string) Result {
	return Result{Stdout: stdout, ExitCode: 1, Diagnostic: &Diagnostic{Kind: "pending-native-diagnostic", Name: name, Message: message, StdioComplete: false}}
}

func nullish(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return json.RawMessage("null")
	}
	return raw
}

func stringUnits(raw json.RawMessage) []uint16 {
	units, _ := protocol.DecodeJavaScriptJSONString(raw)
	return units
}

func textString(raw json.RawMessage) string { return string(utf16.Decode(stringUnits(raw))) }

func truthy(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || bytes.Equal(trimmed, []byte("false")) || bytes.Equal(trimmed, []byte(`""`)) {
		return false
	}
	if isNumber(trimmed) {
		number, _ := strconv.ParseFloat(string(trimmed), 64)
		return number != 0 && !math.IsNaN(number)
	}
	return true
}

// OpenAIStatus implements authStatus's explicit --openai branch. json is
// intentionally not consulted: --text wins over --json in the Source handler.
// Expiry is tested as >0, not against the current time. Native diagnostic
// stacks from malformed external storage remain an explicit incomplete scope.
func OpenAIStatus(storage *OpenAIStorage, text bool) Result {
	tokens := storage.Tokens()
	refresh := rawField(tokens, "refreshToken")
	expires := rawField(tokens, "expiresAt")
	hasTokens := truthy(tokens)
	if hasTokens && (!isString(refresh) || !isNumber(expires)) {
		return incomplete("", "UnmigratedAuthCredentialShape", "Malformed external OpenAI credentials require the original runtime diagnostic contract")
	}
	number, _ := strconv.ParseFloat(string(expires), 64)
	loggedIn := hasTokens && len(stringUnits(refresh)) > 0 && number > 0
	code, method := 1, "none"
	if loggedIn {
		code, method = 0, "openai_oauth"
	}
	if text {
		if !loggedIn {
			return Result{ExitCode: code, Stdout: "Not logged in to OpenAI. Run claude auth login --openai to authenticate.\n"}
		}
		stdout := "Provider: openai\n"
		for _, property := range []struct{ name, label string }{{"email", "Email"}, {"accountId", "Account ID"}} {
			raw := rawField(tokens, property.name)
			if !isString(raw) && !bytes.Equal(nullish(raw), []byte("null")) {
				return incomplete(stdout, "UnmigratedAuthCredentialShape", "Malformed external OpenAI identity requires the original runtime diagnostic contract")
			}
			if isString(raw) && len(stringUnits(raw)) > 0 {
				stdout += property.label + ": " + textString(raw) + "\n"
			}
		}
		if math.IsInf(number, 0) || math.Abs(number) > 8_640_000_000_000_000 {
			return incomplete(stdout, "RangeError", "Invalid time value")
		}
		date := time.UnixMilli(int64(math.Trunc(number))).UTC()
		year := fmt.Sprintf("%04d", date.Year())
		if date.Year() > 9999 {
			year = fmt.Sprintf("+%06d", date.Year())
		}
		stdout += fmt.Sprintf("Expires At: %s-%02d-%02dT%02d:%02d:%02d.%03dZ\n", year, date.Month(), date.Day(), date.Hour(), date.Minute(), date.Second(), date.Nanosecond()/1_000_000)
		return Result{ExitCode: code, Stdout: stdout}
	}
	object := protocol.NewJSONObject(
		protocol.JSONField{Name: "loggedIn", Value: json.RawMessage(strconv.FormatBool(loggedIn))},
		protocol.JSONField{Name: "authMethod", Value: json.RawMessage(strconv.Quote(method))},
		protocol.JSONField{Name: "provider", Value: json.RawMessage(`"openai"`)},
		protocol.JSONField{Name: "email", Value: nullish(rawField(tokens, "email"))},
		protocol.JSONField{Name: "accountId", Value: nullish(rawField(tokens, "accountId"))},
		protocol.JSONField{Name: "expiresAt", Value: nullish(expires)},
	)
	raw, _ := object.MarshalJSON()
	normalized, _ := protocol.ParseAndStringifyJavaScriptJSON(raw)
	var pretty bytes.Buffer
	_ = json.Indent(&pretty, normalized, "", "  ")
	return Result{ExitCode: code, Stdout: pretty.String() + "\n"}
}
