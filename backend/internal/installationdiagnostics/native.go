package installationdiagnostics

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpheaders"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

// NativeRunner executes only diagnostic probes in the caller's explicit
// environment. It never installs, removes or modifies an installation.
func NativeRunner(cwd string, environment map[string]string) func(context.Context, string, []string, RunOptions) (ProcessResult, error) {
	env := map[string]string{}
	for key, value := range environment {
		env[key] = value
	}
	return func(ctx context.Context, name string, args []string, options RunOptions) (ProcessResult, error) {
		timeout := options.Timeout
		if timeout <= 0 {
			timeout = 10 * time.Minute
		}
		active, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		directory := ""
		if options.UseCWD {
			directory = cwd
		}
		if options.Shell {
			result, e := mcpheaders.NativeRun(active, mcpheaders.Request{Command: name + " " + strings.Join(args, " "), CWD: directory, Environment: env, Timeout: timeout})
			return ProcessResult{Code: result.Code, Stdout: result.Stdout}, e
		}
		result, e := worktree.NativeRun(active, worktree.ProcessRequest{Command: name, Arguments: args, CWD: directory, Environment: env})
		return ProcessResult{Code: result.Code, Stdout: stripFinalNewline(result.Stdout), Stderr: stripFinalNewline(result.Stderr)}, e
	}
}
func stripFinalNewline(value string) string {
	if strings.HasSuffix(value, "\n") {
		return strings.TrimSuffix(strings.TrimSuffix(value, "\n"), "\r")
	}
	return value
}
func NativeWhich(environment map[string]string, cwd string) func(context.Context, string) (string, error) {
	return func(ctx context.Context, name string) (string, error) {
		if e := ctx.Err(); e != nil {
			return "", e
		}
		if runtime.GOOS == "windows" {
			value := windowshell.FindNativeExecutable("windows", name, environment)
			if value != "" {
				return value, nil
			}
			return "", os.ErrNotExist
		}
		for _, directory := range filepath.SplitList(environment["PATH"]) {
			if !filepath.IsAbs(directory) {
				directory = filepath.Join(cwd, directory)
			}
			path := filepath.Join(directory, name)
			if info, e := os.Stat(path); e == nil && !info.IsDir() && info.Mode()&0111 != 0 {
				return path, nil
			}
		}
		return "", os.ErrNotExist
	}
}

// LinuxGlobPatterns is the same permission surface used by Source's sandbox
// diagnostic. Only Read/Edit rule contents containing a remaining glob count.
func LinuxGlobPatterns(settings protocol.JSONObject) []string {
	sandbox := protocol.NewJSONObject()
	_ = sandbox.UnmarshalJSON(settings.Get("sandbox"))
	if !rawTruthy(sandbox.Get("enabled")) {
		return []string{}
	}
	permissions := protocol.NewJSONObject()
	_ = permissions.UnmarshalJSON(settings.Get("permissions"))
	result := []string{}
	for _, behavior := range []string{"allow", "deny"} {
		var rules []json.RawMessage
		rawList := permissions.Get(behavior)
		if json.Unmarshal(rawList, &rules) != nil {
			var characters string
			if rawTruthy(rawList) && json.Unmarshal(rawList, &characters) != nil {
				// The original producer spreads both arrays inside a single try.
				// A truthy, non-iterable list aborts the whole diagnostic.
				return []string{}
			}
			continue
		}
		for _, raw := range rules {
			if linuxGlobRuleThrows(raw) {
				return []string{}
			}
			parsed, e := sdkpermissions.ParsePermissionRuleValue(raw)
			if e != nil {
				continue
			}
			tool := parsed.String("toolName")
			if tool != "Read" && tool != "Edit" {
				continue
			}
			content := parsed.String("ruleContent")
			content = strings.TrimSuffix(content, "/**")
			if strings.ContainsAny(content, "*?[]") {
				var rule string
				_ = json.Unmarshal(raw, &rule)
				result = append(result, rule)
			}
		}
	}
	return result
}

// The Source permission parser normally receives strings. Null throws while
// other JSON primitives have no string length and are ignored. Arrays only
// throw when the parser reaches their missing substring method.
func linuxGlobRuleThrows(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return true
	}
	values, ok := value.([]any)
	if !ok {
		return false
	}
	find := func(character string, backwards bool) int {
		found := -1
		for i, v := range values {
			if v != character {
				continue
			}
			slashes := 0
			for j := i - 1; j >= 0 && values[j] == `\`; j-- {
				slashes++
			}
			if slashes%2 == 0 {
				found = i
				if !backwards {
					break
				}
			}
		}
		return found
	}
	open, close := find("(", false), find(")", true)
	return open >= 0 && close > open && close == len(values)-1
}

func rawTruthy(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case float64:
		return v != 0
	}
	return true
}
