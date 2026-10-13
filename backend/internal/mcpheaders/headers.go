// Package mcpheaders implements the original MCP headersHelper admission and
// merge. Failed helpers leave the static headers usable; they never fabricate
// authentication or grant workspace trust.
package mcpheaders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type ProcessResult struct {
	Stdout string
	Code   int
}
type Request struct {
	Command     string
	Environment map[string]string
	CWD         string
	Timeout     time.Duration
}
type Options struct {
	NonInteractive, TrustAccepted bool
	Environment                   map[string]string
	CWD                           string
	Run                           func(context.Context, Request) (ProcessResult, error)
	Error                         func(error)
}

func Resolve(ctx context.Context, name string, config protocol.JSONObject, options Options) map[string]string {
	result := map[string]string{}
	_ = json.Unmarshal(config.Get("headers"), &result)
	if result == nil {
		result = map[string]string{}
	}
	helper := config.String("headersHelper")
	if helper == "" {
		return result
	}
	report := func(e error) {
		if options.Error != nil {
			options.Error(e)
		}
	}
	scope := config.String("scope")
	if (scope == "project" || scope == "local") && !options.NonInteractive && !options.TrustAccepted {
		report(fmt.Errorf("Security: headersHelper for MCP server '%s' executed before workspace trust is confirmed", name))
		return result
	}
	if options.Run == nil {
		report(errors.New("MCP headersHelper requires actual native process binding"))
		return result
	}
	env := map[string]string{}
	for k, v := range options.Environment {
		env[k] = v
	}
	env["CLAUDE_CODE_MCP_SERVER_NAME"] = name
	env["CLAUDE_CODE_MCP_SERVER_URL"] = config.String("url")
	output, e := options.Run(ctx, Request{Command: helper, Environment: env, CWD: options.CWD, Timeout: 10 * time.Second})
	if e != nil || output.Code != 0 || output.Stdout == "" {
		report(fmt.Errorf("headersHelper for MCP server '%s' did not return a valid value", name))
		return result
	}
	var headers protocol.JSONObject
	if headers.UnmarshalJSON([]byte(protocol.TrimJavaScriptString(output.Stdout))) != nil || !headers.IsObject() {
		report(fmt.Errorf("headersHelper for MCP server '%s' must return a JSON object with string key-value pairs", name))
		return result
	}
	for _, field := range headers.OwnProperties() {
		var value string
		if json.Unmarshal(field.Value, &value) != nil || len(field.Value) == 0 || field.Value[0] != '"' {
			report(fmt.Errorf("headersHelper for MCP server '%s' returned non-string value", name))
			return result
		}
	}
	for _, field := range headers.OwnProperties() {
		var name, value string
		_ = json.Unmarshal(field.NameJSON, &name)
		_ = json.Unmarshal(field.Value, &value)
		result[name] = value
	}
	return result
}
