// Package mcpcli runs the original CLI management handlers against the same
// native MCP configuration/editor/OAuth/connection producers as the desktop.
package mcpcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliargs"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Options struct {
	Configurations *mcpconfig.Service
	Editor         *mcpconfig.Editor
	Auth           *mcpauth.Service
	Runtime        *mcpruntime.Service
	Environment    map[string]string
	Output, Error  io.Writer
	All            func(context.Context) (mcpconfig.Result, error)
	Settings       func(context.Context) (protocol.JSONObject, error)
	ResetChoices   func(context.Context) error
	ReadSecret     func(context.Context) (string, error)
	ReadSecretJSON func(context.Context) (json.RawMessage, error)
	ImportDesktop  func(context.Context, string) error
	Serve          func(context.Context, bool, bool) error
}
type Service struct{ o Options }

func New(o Options) (*Service, error) {
	if o.Configurations == nil || o.Editor == nil || o.Auth == nil || o.Runtime == nil || o.Environment == nil || o.Output == nil || o.Error == nil || o.All == nil || o.Settings == nil || o.ResetChoices == nil || (o.ReadSecret == nil && o.ReadSecretJSON == nil) {
		return nil, errors.New("CLI MCP requires shared native configuration/editor/auth/runtime and CLI IO producers")
	}
	return &Service{o}, nil
}
func (s *Service) Run(ctx context.Context, p cliargs.Parsed) (int, error) {
	switch p.Path {
	case "claude mcp add":
		return s.add(ctx, p)
	case "claude mcp add-json":
		return s.addJSON(ctx, p)
	case "claude mcp remove":
		return s.remove(ctx, p)
	case "claude mcp list":
		return s.list(ctx)
	case "claude mcp get":
		return s.get(ctx, arg(p, 0))
	case "claude mcp reset-project-choices":
		if err := s.o.ResetChoices(ctx); err != nil {
			return 1, err
		}
		_, err := fmt.Fprintln(s.o.Output, "All project-scoped (.mcp.json) server approvals and rejections have been reset.\nYou will be prompted for approval next time you start Claude Code.")
		return 0, err
	case "claude mcp add-from-claude-desktop":
		scope, err := mcpapi.Scope(option(p, "scope"))
		if err != nil {
			return 1, err
		}
		if s.o.ImportDesktop == nil {
			return 1, errors.New("CLI MCP capability is not migrated: actual desktop import multi-selection")
		}
		err = s.o.ImportDesktop(ctx, scope)
		if err != nil {
			return 1, err
		}
		return 0, nil
	case "claude mcp serve":
		if s.o.Serve == nil {
			return 1, errors.New("CLI MCP capability is not migrated: native stdio server and ordinary tool context")
		}
		err := s.o.Serve(ctx, boolean(p, "debug"), boolean(p, "verbose"))
		if err != nil {
			return 1, err
		}
		return 0, nil
	default:
		return 1, fmt.Errorf("CLI MCP capability is not migrated: %s", p.Path)
	}
}
func arg(p cliargs.Parsed, i int) string {
	if len(p.ProcessedArgs) > i {
		if v, ok := p.ProcessedArgs[i].(string); ok {
			return v
		}
	}
	if len(p.Args) > i {
		return p.Args[i]
	}
	return ""
}
func option(p cliargs.Parsed, name string) string { v, _ := p.Options[name].(string); return v }
func boolean(p cliargs.Parsed, name string) bool  { v, _ := p.Options[name].(bool); return v }
func optionList(p cliargs.Parsed, name string) []string {
	values := []string{}
	switch v := p.Options[name].(type) {
	case []string:
		return v
	case []any:
		for _, entry := range v {
			if text, ok := entry.(string); ok {
				values = append(values, text)
			}
		}
	}
	return values
}
func args(p cliargs.Parsed) []string {
	if len(p.ProcessedArgs) > 2 {
		switch v := p.ProcessedArgs[2].(type) {
		case []string:
			return v
		case []any:
			out := []string{}
			for _, entry := range v {
				if text, ok := entry.(string); ok {
					out = append(out, text)
				}
			}
			return out
		}
	}
	if len(p.Args) > 2 {
		return p.Args[2:]
	}
	return []string{}
}
func encode(v any) json.RawMessage { raw, _ := json.Marshal(v); return raw }
func (s *Service) readSecret(ctx context.Context) (json.RawMessage, error) {
	if s.o.ReadSecretJSON != nil {
		value, err := s.o.ReadSecretJSON(ctx)
		if err != nil {
			return nil, err
		}
		if _, err = protocol.DecodeJavaScriptJSONString(value); err != nil {
			return nil, err
		}
		return append(json.RawMessage(nil), value...), nil
	}
	value, err := s.o.ReadSecret(ctx)
	return encode(value), err
}
func secretPresent(raw json.RawMessage) bool {
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	return err == nil && len(units) > 0
}

func object(raw json.RawMessage) protocol.JSONObject {
	var v protocol.JSONObject
	_ = v.UnmarshalJSON(raw)
	return v
}
func text(raw json.RawMessage) string { var v string; _ = json.Unmarshal(raw, &v); return v }
func envTruthy(v string) bool {
	switch strings.ToLower(protocol.TrimJavaScriptString(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
func Transport(value string) (string, error) {
	if value == "" {
		return "stdio", nil
	}
	if value != "stdio" && value != "sse" && value != "http" {
		return "", fmt.Errorf("Invalid transport type: %s. Must be one of: stdio, sse, http", value)
	}
	return value, nil
}
func ParseEnvironment(values []string) (protocol.JSONObject, error) {
	out := protocol.NewJSONObject()
	for _, v := range values {
		key, value, ok := strings.Cut(v, "=")
		if key == "" || !ok {
			return out, fmt.Errorf("Invalid environment variable format: %s, environment variables should be added as: -e KEY1=value1 -e KEY2=value2", v)
		}
		if key != "__proto__" {
			out.Set(key, encode(value))
		}
	}
	return out, nil
}
func ParseHeaders(values []string) (protocol.JSONObject, error) {
	out := protocol.NewJSONObject()
	for _, v := range values {
		key, value, ok := strings.Cut(v, ":")
		if !ok {
			return out, fmt.Errorf("Invalid header format: \"%s\". Expected format: \"Header-Name: value\"", v)
		}
		key = strings.TrimFunc(key, protocol.IsJavaScriptWhitespace)
		value = strings.TrimFunc(value, protocol.IsJavaScriptWhitespace)
		if key == "" {
			return out, fmt.Errorf("Invalid header: \"%s\". Header name cannot be empty.", v)
		}
		if key != "__proto__" {
			out.Set(key, encode(value))
		}
	}
	return out, nil
}
func parseIntPrefix(value string) float64 {
	value = strings.TrimFunc(value, protocol.IsJavaScriptWhitespace)
	sign := 1.0
	if strings.HasPrefix(value, "-") {
		sign = -1
		value = value[1:]
	} else if strings.HasPrefix(value, "+") {
		value = value[1:]
	}
	i := 0
	for i < len(value) && value[i] >= '0' && value[i] <= '9' {
		i++
	}
	if i == 0 {
		return math.NaN()
	}
	number, _ := strconv.ParseFloat(value[:i], 64)
	return number * sign
}
func (s *Service) add(ctx context.Context, p cliargs.Parsed) (int, error) {
	name, command := arg(p, 0), arg(p, 1)
	scope, err := mcpapi.Scope(option(p, "scope"))
	if err != nil {
		return 1, err
	}
	transport, err := Transport(option(p, "transport"))
	if err != nil {
		return 1, err
	}
	xaa := boolean(p, "xaa")
	if xaa && !envTruthy(s.o.Environment["CLAUDE_CODE_ENABLE_XAA"]) {
		return 1, errors.New("Error: --xaa requires CLAUDE_CODE_ENABLE_XAA=1 in your environment")
	}
	client := option(p, "clientId")
	secretFlag := boolean(p, "clientSecret")
	callback := option(p, "callbackPort")
	if xaa {
		missing := []string{}
		if client == "" {
			missing = append(missing, "--client-id")
		}
		if !secretFlag {
			missing = append(missing, "--client-secret")
		}
		settings, err := s.o.Settings(ctx)
		if err != nil {
			return 1, err
		}
		if settings.Get("xaaIdp") == nil || string(settings.Get("xaaIdp")) == "null" {
			missing = append(missing, "'claude mcp xaa setup' (settings.xaaIdp not configured)")
		}
		if len(missing) > 0 {
			return 1, fmt.Errorf("Error: --xaa requires: %s", strings.Join(missing, ", "))
		}
	}
	config := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: encode(transport)})
	if transport == "stdio" {
		if client != "" || secretFlag || callback != "" || xaa {
			fmt.Fprintln(s.o.Error, "Warning: --client-id, --client-secret, --callback-port, and --xaa are only supported for HTTP/SSE transports and will be ignored for stdio.")
		}
		looks := strings.HasPrefix(command, "http://") || strings.HasPrefix(command, "https://") || strings.HasPrefix(command, "localhost") || strings.HasSuffix(command, "/sse") || strings.HasSuffix(command, "/mcp")
		if option(p, "transport") == "" && looks {
			fmt.Fprintf(s.o.Error, "\nWarning: The command \"%s\" looks like a URL, but is being interpreted as a stdio server as --transport was not specified.\nIf this is an HTTP server, use: claude mcp add --transport http %s %s\nIf this is an SSE server, use: claude mcp add --transport sse %s %s\n", command, name, command, name, command)
		}
		env, err := ParseEnvironment(optionList(p, "env"))
		if err != nil {
			return 1, err
		}
		config.Set("command", encode(command))
		config.Set("args", encode(args(p)))
		config.Set("env", encode(env))
		if err := s.o.Editor.Add(ctx, name, scope, encode(config)); err != nil {
			return 1, err
		}
		fmt.Fprintf(s.o.Output, "Added stdio MCP server %s with command: %s %s to %s config\n", name, command, strings.Join(args(p), " "), scope)
	} else {
		var headers *protocol.JSONObject
		if _, provided := p.Options["header"]; provided {
			value, err := ParseHeaders(optionList(p, "header"))
			if err != nil {
				return 1, err
			}
			headers = &value
		}
		config.Set("url", encode(command))
		if headers != nil {
			config.Set("headers", encode(*headers))
		}
		port := parseIntPrefix(callback)
		oauth := protocol.NewJSONObject()
		if client != "" {
			oauth.Set("clientId", encode(client))
		}
		if port != 0 && !math.IsNaN(port) {
			oauth.Set("callbackPort", encode(port))
		}
		if xaa {
			oauth.Set("xaa", encode(true))
		}
		if len(oauth.OwnProperties()) > 0 {
			config.Set("oauth", encode(oauth))
		}
		secret := json.RawMessage(`""`)
		if secretFlag && client != "" {
			secret, err = s.readSecret(ctx)
			if err != nil {
				return 1, err
			}
		}
		if err := s.o.Editor.Add(ctx, name, scope, encode(config)); err != nil {
			return 1, err
		}
		if secretPresent(secret) {
			if err := s.o.Auth.SaveClientSecretJSON(ctx, name, config, secret); err != nil {
				return 1, err
			}
		}
		fmt.Fprintf(s.o.Output, "Added %s MCP server %s with URL: %s to %s config\n", strings.ToUpper(transport), name, command, scope)
		if headers != nil {
			raw := protocol.StringifyValidatedJavaScriptJSON(encode(*headers))
			var b bytes.Buffer
			_ = json.Indent(&b, raw, "", "  ")
			fmt.Fprintf(s.o.Output, "Headers: %s\n", b.String())
		}
	}
	path, err := s.o.Editor.Location(name, scope)
	if err != nil {
		return 1, err
	}
	_, err = fmt.Fprintln(s.o.Output, "File modified: "+path)
	return 0, err
}

func (s *Service) addJSON(ctx context.Context, p cliargs.Parsed) (int, error) {
	scope, err := mcpapi.Scope(option(p, "scope"))
	if err != nil {
		return 1, err
	}
	name := arg(p, 0)
	parsed := json.RawMessage(arg(p, 1))
	if !json.Valid(parsed) {
		parsed = json.RawMessage("null")
	}
	config := object(parsed)
	oauth, _ := config.Object("oauth")
	remote := config.String("type") == "sse" || config.String("type") == "http"
	secret := json.RawMessage(`""`)
	if boolean(p, "clientSecret") && remote {
		if _, err := protocol.DecodeJavaScriptJSONString(config.Get("url")); err == nil && oauth.IsObject() && oauth.Get("clientId") != nil {
			secret, err = s.readSecret(ctx)
			if err != nil {
				return 1, err
			}
		}
	}
	if err := s.o.Editor.Add(ctx, name, scope, parsed); err != nil {
		return 1, err
	}
	kind := config.String("type")
	if kind == "" {
		kind = "stdio"
	}
	if secretPresent(secret) && remote {
		if err := s.o.Auth.SaveClientSecretJSON(ctx, name, config, secret); err != nil {
			return 1, err
		}
	}
	_, err = fmt.Fprintf(s.o.Output, "Added %s MCP server %s to %s config\n", kind, name, scope)
	return 0, err
}
func scopeLabel(scope string) string {
	switch scope {
	case "local":
		return "Local config (private to you in this project)"
	case "project":
		return "Project config (shared via .mcp.json)"
	case "user":
		return "User config (available in all your projects)"
	case "dynamic":
		return "Dynamic config (from command line)"
	case "enterprise":
		return "Enterprise config (managed by your organization)"
	case "claudeai":
		return "claude.ai config"
	}
	return scope
}
func (s *Service) remove(ctx context.Context, p cliargs.Parsed) (int, error) {
	name := arg(p, 0)
	before, err := s.o.Configurations.ByName(ctx, name)
	if err != nil {
		return 1, err
	}
	cleanup := func() error {
		if before != nil && (before.String("type") == "http" || before.String("type") == "sse") {
			return s.o.Auth.Clear(ctx, name, *before)
		}
		return nil
	}
	explicit := option(p, "scope")
	if explicit != "" {
		scope, err := mcpapi.Scope(explicit)
		if err != nil {
			return 1, err
		}
		if err := s.o.Editor.Remove(ctx, name, scope); err != nil {
			return 1, err
		}
		if err := cleanup(); err != nil {
			return 1, err
		}
		path, err := s.o.Editor.Location(name, scope)
		if err != nil {
			return 1, err
		}
		_, err = fmt.Fprintf(s.o.Output, "Removed MCP server %s from %s config\nFile modified: %s\n", name, scope, path)
		return 0, err
	}
	scopes := []string{}
	for _, scope := range []string{"local", "project", "user"} {
		value, err := s.o.Configurations.Scope(ctx, scope)
		if err != nil {
			return 1, err
		}
		if value.Servers.Get(name) != nil {
			scopes = append(scopes, scope)
		}
	}
	if len(scopes) == 0 {
		return 1, fmt.Errorf("No MCP server found with name: \"%s\"", name)
	}
	if len(scopes) == 1 {
		scope := scopes[0]
		if err := s.o.Editor.Remove(ctx, name, scope); err != nil {
			return 1, err
		}
		if err := cleanup(); err != nil {
			return 1, err
		}
		path, err := s.o.Editor.Location(name, scope)
		if err != nil {
			return 1, err
		}
		_, err = fmt.Fprintf(s.o.Output, "Removed MCP server \"%s\" from %s config\nFile modified: %s\n", name, scope, path)
		return 0, err
	}
	fmt.Fprintf(s.o.Error, "MCP server \"%s\" exists in multiple scopes:\n", name)
	for _, scope := range scopes {
		path, err := s.o.Editor.Location(name, scope)
		if err != nil {
			return 1, err
		}
		fmt.Fprintf(s.o.Error, "  - %s (%s)\n", scopeLabel(scope), path)
	}
	fmt.Fprint(s.o.Error, "\nTo remove from a specific scope, use:\n")
	for _, scope := range scopes {
		fmt.Fprintf(s.o.Error, "  claude mcp remove \"%s\" -s %s\n", name, scope)
	}
	return 1, nil
}
func (s *Service) health(ctx context.Context, name string, config protocol.JSONObject) string {
	value, err := s.o.Runtime.ConnectTransport(ctx, name, config)
	if err != nil {
		return "✗ Connection error"
	}
	switch value.Status {
	case "connected":
		return "✓ Connected"
	case "needs-auth":
		return "! Needs authentication"
	default:
		return "✗ Failed to connect"
	}
}
func (s *Service) list(ctx context.Context) (int, error) {
	result, err := s.o.All(ctx)
	if err != nil {
		return 1, err
	}
	fields := result.Servers.OwnProperties()
	if len(fields) == 0 {
		_, err = fmt.Fprintln(s.o.Output, "No MCP servers configured. Use `claude mcp add` to add a server.")
		return 0, err
	}
	fmt.Fprint(s.o.Output, "Checking MCP server health...\n\n")
	concurrency := parseIntPrefix(s.o.Environment["MCP_SERVER_CONNECTION_BATCH_SIZE"])
	if math.IsNaN(concurrency) || concurrency == 0 {
		concurrency = 3
	}
	if concurrency < 1 || math.IsInf(concurrency, 0) {
		return 1, errors.New("Expected `concurrency` to be an integer from 1 and up or `Infinity`")
	}
	count := int(concurrency)
	if count > len(fields) {
		count = len(fields)
	}
	statuses := make([]string, len(fields))
	jobs := make(chan int)
	var wait sync.WaitGroup
	for i := 0; i < count; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := range jobs {
				field := fields[index]
				statuses[index] = s.health(ctx, text(field.NameJSON), object(field.Value))
			}
		}()
	}
	for i := range fields {
		select {
		case jobs <- i:
		case <-ctx.Done():
			close(jobs)
			wait.Wait()
			return 1, context.Cause(ctx)
		}
	}
	close(jobs)
	wait.Wait()
	for i, field := range fields {
		name := text(field.NameJSON)
		config := object(field.Value)
		kind := config.String("type")
		switch kind {
		case "sse":
			fmt.Fprintf(s.o.Output, "%s: %s (SSE) - %s\n", name, config.String("url"), statuses[i])
		case "http":
			fmt.Fprintf(s.o.Output, "%s: %s (HTTP) - %s\n", name, config.String("url"), statuses[i])
		case "claudeai-proxy":
			fmt.Fprintf(s.o.Output, "%s: %s - %s\n", name, config.String("url"), statuses[i])
		case "", "stdio":
			var arguments []string
			_ = json.Unmarshal(config.Get("args"), &arguments)
			fmt.Fprintf(s.o.Output, "%s: %s %s - %s\n", name, config.String("command"), strings.Join(arguments, " "), statuses[i])
		}
	}
	return 0, nil
}
func (s *Service) get(ctx context.Context, name string) (int, error) {
	config, err := s.o.Configurations.ByName(ctx, name)
	if err != nil {
		return 1, err
	}
	if config == nil {
		return 1, fmt.Errorf("No MCP server found with name: %s", name)
	}
	fmt.Fprintf(s.o.Output, "%s:\n  Scope: %s\n  Status: %s\n", name, scopeLabel(config.String("scope")), s.health(ctx, name, *config))
	kind := config.String("type")
	switch kind {
	case "sse", "http":
		fmt.Fprintf(s.o.Output, "  Type: %s\n  URL: %s\n", kind, config.String("url"))
		if headers := config.Get("headers"); headers != nil && string(headers) != "null" {
			fmt.Fprintln(s.o.Output, "  Headers:")
			for _, field := range object(headers).OwnProperties() {
				fmt.Fprintf(s.o.Output, "    %s: %s\n", text(field.NameJSON), text(field.Value))
			}
		}
		oauth, _ := config.Object("oauth")
		client := oauth.String("clientId")
		port := parseIntPrefix(text(oauth.Get("callbackPort")))
		if raw := oauth.Get("callbackPort"); raw != nil {
			_ = json.Unmarshal(raw, &port)
		}
		if client != "" || port != 0 && !math.IsNaN(port) {
			parts := []string{}
			if client != "" {
				parts = append(parts, "client_id configured")
				saved, err := s.o.Auth.ClientConfig(ctx, name, *config)
				if err != nil {
					return 1, err
				}
				if saved.String("clientSecret") != "" {
					parts = append(parts, "client_secret configured")
				}
			}
			if port != 0 && !math.IsNaN(port) {
				parts = append(parts, "callback_port "+strconv.FormatFloat(port, 'f', -1, 64))
			}
			fmt.Fprintf(s.o.Output, "  OAuth: %s\n", strings.Join(parts, ", "))
		}
	case "stdio":
		var arguments []string
		_ = json.Unmarshal(config.Get("args"), &arguments)
		fmt.Fprintf(s.o.Output, "  Type: stdio\n  Command: %s\n  Args: %s\n", config.String("command"), strings.Join(arguments, " "))
		if env := config.Get("env"); env != nil && string(env) != "null" {
			fmt.Fprintln(s.o.Output, "  Environment:")
			for _, field := range object(env).OwnProperties() {
				fmt.Fprintf(s.o.Output, "    %s=%s\n", text(field.NameJSON), text(field.Value))
			}
		}
	}
	_, err = fmt.Fprintf(s.o.Output, "\nTo remove this server, run: claude mcp remove \"%s\" -s %s\n", name, config.String("scope"))
	return 0, err
}
