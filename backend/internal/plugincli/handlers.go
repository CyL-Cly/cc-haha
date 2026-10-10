package plugincli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"sort"
	"strings"
)

type Result struct {
	Stdout, Stderr string
	ExitCode       int
	StreamedBytes  int
}
type Command struct {
	Action, Argument, Scope                string
	JSON, Available, Cowork, All, KeepData bool
	Sparse                                 []string
}

func Preflight(c Command) *Result {
	fail := func(message string) *Result { return &Result{Stderr: message + "\n", ExitCode: 1} }
	if c.Action == "disable" {
		if c.All && c.Argument != "" {
			return fail("Cannot use --all with a specific plugin")
		}
		if !c.All && c.Argument == "" {
			return fail("Please specify a plugin name or use --all to disable all plugins")
		}
		if c.All && c.Scope != "" {
			return fail("Cannot use --scope with --all")
		}
	}
	if c.Action == "install" || c.Action == "uninstall" {
		scope := c.Scope
		if scope == "" {
			scope = "user"
		}
		if c.Cowork && scope != "user" {
			return fail("--cowork can only be used with user scope")
		}
		if !installableScope(scope) {
			return fail(fmt.Sprintf("Invalid scope: %s. Must be one of: user, project, local.", scope))
		}
	}
	if c.Action == "enable" || c.Action == "disable" && !c.All || c.Action == "update" {
		if c.Scope != "" && !installableScope(c.Scope) && !(c.Action == "update" && c.Scope == "managed") {
			valid := "user, project, local"
			if c.Action == "update" {
				valid += ", managed"
			}
			return fail(fmt.Sprintf("Invalid scope \"%s\". Valid scopes: %s", c.Scope, valid))
		}
		if c.Cowork && c.Scope != "" && c.Scope != "user" {
			return fail("--cowork can only be used with user scope")
		}
	}
	return nil
}
func installableScope(scope string) bool {
	return scope == "user" || scope == "project" || scope == "local"
}

func InstalledList(ctx context.Context, snapshot ListSnapshot, options ListOptions, jsonMode bool) (Result, error) {
	if jsonMode {
		rows, err := InstalledJSON(ctx, snapshot, options)
		if err != nil {
			return Result{}, err
		}
		return Result{Stdout: pretty(rows) + "\n"}, nil
	}
	installed := object(snapshot.Installed.Get("plugins"))
	ids := sortedKeys(installed)
	loaded := append(append([]pluginregistry.Loaded{}, snapshot.Loaded.Enabled...), snapshot.Loaded.Disabled...)
	inline := []pluginregistry.Loaded{}
	inlineErrors := []protocol.JSONObject{}
	for _, p := range loaded {
		if strings.HasSuffix(p.Source, "@inline") {
			inline = append(inline, p)
		}
	}
	for _, e := range snapshot.Loaded.Errors {
		if inlineError(e) {
			inlineErrors = append(inlineErrors, e)
		}
	}
	if len(ids) == 0 && len(inline) == 0 && len(inlineErrors) == 0 {
		return Result{Stdout: "No plugins installed. Use `claude plugin install` to install a plugin.\n"}, nil
	}
	var out strings.Builder
	if len(ids) > 0 {
		out.WriteString("Installed plugins:\n\n")
	}
	for _, id := range ids {
		name := strings.Split(id, "@")[0]
		issues := errorsFor(snapshot.Loaded.Errors, id, name, false)
		for _, entry := range array(installed.Get(id)) {
			row := object(entry)
			version := row.String("version")
			if !truthy(row.Get("version")) {
				version = "unknown"
			}
			status := "✘ disabled"
			if snapshot.Enabled[id] {
				status = "✔ enabled"
			}
			if len(issues) > 0 {
				status = "✘ failed to load"
			}
			fmt.Fprintf(&out, "  ❯ %s\n    Version: %s\n    Scope: %s\n    Status: %s\n", id, version, row.String("scope"), status)
			for _, issue := range issues {
				fmt.Fprintf(&out, "    Error: %s\n", issue)
			}
			out.WriteByte('\n')
		}
	}
	if len(inline) > 0 || len(inlineErrors) > 0 {
		out.WriteString("Session-only plugins (--plugin-dir):\n\n")
		for _, p := range inline {
			issues := errorsFor(inlineErrors, p.Source, p.Name, true)
			status := "✔ loaded"
			if len(issues) > 0 {
				status = "✘ loaded with errors"
			}
			version := p.Manifest.String("version")
			if p.Manifest.Get("version") == nil || string(p.Manifest.Get("version")) == "null" {
				version = "unknown"
			}
			fmt.Fprintf(&out, "  ❯ %s\n    Version: %s\n    Path: %s\n    Status: %s\n", p.Source, version, p.Path, status)
			for _, issue := range issues {
				fmt.Fprintf(&out, "    Error: %s\n", issue)
			}
			out.WriteByte('\n')
		}
		for _, e := range inlineErrors {
			if strings.HasPrefix(e.String("source"), "inline[") {
				fmt.Fprintf(&out, "  ❯ %s: ✘ %s\n\n", e.String("source"), pluginsapi.ErrorMessage(e))
			}
		}
	}
	return Result{Stdout: out.String()}, nil
}

func sortedKeys(object protocol.JSONObject) []string {
	keys := []string{}
	for _, p := range object.OwnProperties() {
		var k string
		_ = json.Unmarshal(p.NameJSON, &k)
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return compareUTF16(keys[i], keys[j]) < 0 })
	return keys
}
func pretty(value any) string {
	raw, _ := json.Marshal(value)
	normalized, _ := protocol.ParseAndStringifyJavaScriptJSON(raw)
	var out bytes.Buffer
	_ = json.Indent(&out, normalized, "", "  ")
	return out.String()
}

type Operations interface {
	Install(context.Context, string, string) (protocol.JSONObject, error)
	SetEnabled(context.Context, string, bool, string, string) (protocol.JSONObject, error)
	Uninstall(context.Context, string, string, string, bool) (protocol.JSONObject, error)
	Update(context.Context, string, string, string) (protocol.JSONObject, error)
	DisableAll(context.Context) (protocol.JSONObject, error)
}

func RunOperation(ctx context.Context, c Command, operations Operations, emit ...func(string) int) (Result, error) {
	if result := Preflight(c); result != nil {
		return *result, nil
	}
	scope := c.Scope
	if scope == "" && (c.Action == "install" || c.Action == "uninstall" || c.Action == "update" || c.Cowork) {
		scope = "user"
	}
	result := Result{}
	var value protocol.JSONObject
	var err error
	switch c.Action {
	case "install":
		result.Stdout = "Installing plugin \"" + c.Argument + "\"...\n"
		if len(emit) > 0 && emit[0] != nil {
			result.StreamedBytes += emit[0](result.Stdout)
		}
		value, err = operations.Install(ctx, c.Argument, scope)
	case "uninstall":
		value, err = operations.Uninstall(ctx, c.Argument, scope, "", c.KeepData)
	case "enable":
		value, err = operations.SetEnabled(ctx, c.Argument, true, scope, "")
	case "disable":
		if c.All {
			value, err = operations.DisableAll(ctx)
		} else {
			value, err = operations.SetEnabled(ctx, c.Argument, false, scope, "")
		}
	case "update":
		result.Stdout = "Checking for updates for plugin \"" + c.Argument + "\" at " + scope + " scope…\n"
		if len(emit) > 0 && emit[0] != nil {
			result.StreamedBytes += emit[0](result.Stdout)
		}
		value, err = operations.Update(ctx, c.Argument, scope, "")
	default:
		return Result{}, fmt.Errorf("ordinary CLI plugin operation %s has no producer", c.Action)
	}
	if err != nil {
		operation := c.Action + " plugin \"" + c.Argument + "\""
		if c.All {
			operation = "disable all plugins"
		}
		result.Stderr = "✘ Failed to " + operation + ": " + err.Error() + "\n"
		result.ExitCode = 1
		return result, nil
	}
	result.Stdout += "✔ " + value.String("message") + "\n"
	return result, nil
}
