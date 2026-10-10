package mcpconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	weburl "github.com/nlnwa/whatwg-url/url"
)

type Result struct {
	Servers protocol.JSONObject   `json:"servers"`
	Errors  []protocol.JSONObject `json:"errors"`
}
type Options struct {
	CWD, Platform, ManagedRoot        string
	Environment                       func(string) (string, bool)
	ReadFile                          func(string) ([]byte, error)
	SourceEnabled                     func(string) bool
	Settings, Policy, Global, Project func(context.Context) (protocol.JSONObject, error)
	Plugins                           func(context.Context) (Result, error)
	Enabled                           func(context.Context, string, protocol.JSONObject) (bool, error)
	BypassConfirmed                   func(context.Context) (bool, error)
	NonInteractive                    bool
}
type Service struct {
	options          Options
	mu               sync.Mutex
	enterpriseExists *bool
}

func New(o Options) (*Service, error) {
	if !filepath.IsAbs(o.CWD) || !filepath.IsAbs(o.ManagedRoot) || o.Platform == "" || o.Environment == nil || o.ReadFile == nil || o.SourceEnabled == nil || o.Settings == nil || o.Policy == nil || o.Global == nil || o.Project == nil || o.Plugins == nil || o.Enabled == nil || o.BypassConfirmed == nil {
		return nil, errors.New("MCP configuration requires actual paths, files, settings, plugins, enablement and approval producers")
	}
	return &Service{options: o}, nil
}
func empty() Result { return Result{protocol.NewJSONObject(), []protocol.JSONObject{}} }
func problem(file, path, message, suggestion, scope, server, severity string) protocol.JSONObject {
	r := protocol.NewJSONObject()
	if file != "" {
		r.Set("file", quote(file))
	}
	r.Set("path", quote(path))
	r.Set("message", quote(message))
	if suggestion != "" {
		r.Set("suggestion", quote(suggestion))
	}
	meta := protocol.NewJSONObject(protocol.JSONField{Name: "scope", Value: quote(scope)})
	if server != "" {
		meta.Set("serverName", quote(server))
	}
	meta.Set("severity", quote(severity))
	r.Set("mcpErrorMetadata", encode(meta))
	return r
}

var envPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

func (s *Service) expand(raw json.RawMessage, missing *[]string) json.RawMessage {
	units, _ := protocol.DecodeJavaScriptJSONString(raw)
	value := string(utf16.Decode(units))
	expanded := envPattern.ReplaceAllStringFunc(value, func(match string) string {
		contents := strings.TrimSuffix(strings.TrimPrefix(match, "${"), "}")
		parts := strings.Split(contents, ":-")
		if v, ok := s.options.Environment(parts[0]); ok {
			return v
		}
		if len(parts) > 1 {
			return parts[1]
		}
		*missing = append(*missing, parts[0])
		return match
	})
	// Keep untouched UTF-16 code units instead of round-tripping through Go.
	if expanded == value {
		return raw
	}
	return quote(expanded)
}
func (s *Service) expandServer(config protocol.JSONObject) (protocol.JSONObject, []string) {
	result := config.Clone()
	missing := []string{}
	expand := func(k string) {
		if v := config.Get(k); v != nil {
			result.Set(k, s.expand(v, &missing))
		}
	}
	expandMap := func(k string) {
		if v := config.Get(k); v != nil {
			out := protocol.NewJSONObject()
			for _, p := range object(v).OwnProperties() {
				if name(p) != "__proto__" {
					_ = out.SetPropertyJSON(p.NameJSON, s.expand(p.Value, &missing))
				}
			}
			result.Set(k, encode(out))
		}
	}
	switch config.String("type") {
	case "", "stdio":
		expand("command")
		items := []json.RawMessage{}
		for _, v := range list(config.Get("args")) {
			items = append(items, s.expand(v, &missing))
		}
		result.Set("args", encode(items))
		expandMap("env")
	case "sse", "http", "ws":
		expand("url")
		expandMap("headers")
	}
	unique := []string{}
	seen := map[string]bool{}
	for _, key := range missing {
		if !seen[key] {
			unique = append(unique, key)
			seen[key] = true
		}
	}
	return result, unique
}
func (s *Service) Parse(raw json.RawMessage, expand bool, scope, file string) (*protocol.JSONObject, []protocol.JSONObject) {
	failures := []protocol.JSONObject{}
	config := object(raw)
	servers := object(config.Get("mcpServers"))
	if !config.IsObject() || !servers.IsObject() {
		path := ""
		if config.IsObject() {
			path = "mcpServers"
		}
		return nil, []protocol.JSONObject{problem(file, path, "Does not adhere to MCP server configuration schema", "", scope, "", "fatal")}
	}
	parsed := protocol.NewJSONObject()
	for _, p := range servers.OwnProperties() {
		value, issues := ValidateServer(p.Value)
		if len(issues) > 0 {
			for _, issue := range issues {
				path := "mcpServers." + name(p)
				if issue != "" {
					path += "." + issue
				}
				failures = append(failures, problem(file, path, "Does not adhere to MCP server configuration schema", "", scope, "", "fatal"))
			}
		} else if name(p) != "__proto__" {
			_ = parsed.SetPropertyJSON(p.NameJSON, encode(value))
		}
	}
	if len(failures) > 0 {
		return nil, failures
	}
	for _, p := range parsed.OwnProperties() {
		value := object(p.Value)
		if expand {
			var missing []string
			value, missing = s.expandServer(value)
			if len(missing) > 0 {
				keys := strings.Join(missing, ", ")
				failures = append(failures, problem(file, "mcpServers."+name(p), "Missing environment variables: "+keys, "Set the following environment variables: "+keys, scope, name(p), "warning"))
			}
		}
		if (s.options.Platform == "windows" || s.options.Platform == "win32") && (value.String("type") == "" || value.String("type") == "stdio") {
			command := value.String("command")
			if command == "npx" || strings.HasSuffix(command, `\npx`) || strings.HasSuffix(command, "/npx") {
				failures = append(failures, problem(file, "mcpServers."+name(p), "Windows requires 'cmd /c' wrapper to execute npx", `Change command to "cmd" with args ["/c", "npx", ...]. See: https://code.claude.com/docs/en/mcp#configure-mcp-servers`, scope, name(p), "warning"))
			}
		}
		_ = parsed.SetPropertyJSON(p.NameJSON, encode(value))
	}
	result := protocol.NewJSONObject(protocol.JSONField{Name: "mcpServers", Value: encode(parsed)})
	return &result, failures
}
func (s *Service) ParseFile(path string, expand bool, scope string) (*protocol.JSONObject, []protocol.JSONObject) {
	data, err := s.options.ReadFile(path)
	if err != nil {
		message, suggestion := "Failed to read file: "+storage.JavaScriptErrorText(err), "Check file permissions and ensure the file exists"
		if os.IsNotExist(err) {
			message = "MCP config file not found: " + path
			suggestion = "Check that the file path is correct"
		}
		return nil, []protocol.JSONObject{problem(path, "", message, suggestion, scope, "", "fatal")}
	}
	raw := bytes.TrimPrefix(protocol.DecodeJavaScriptUTF8(data), []byte{0xef, 0xbb, 0xbf})
	if !json.Valid(raw) || !truthy(raw) {
		return nil, []protocol.JSONObject{problem(path, "", "MCP config is not a valid JSON", "Fix the JSON syntax errors in the file", scope, "", "fatal")}
	}
	return s.Parse(raw, expand, scope, path)
}
func addScope(servers protocol.JSONObject, scope string) protocol.JSONObject {
	out := protocol.NewJSONObject()
	for _, p := range servers.OwnProperties() {
		if name(p) == "__proto__" {
			continue
		}
		config := object(p.Value).Clone()
		config.Set("scope", quote(scope))
		_ = out.SetPropertyJSON(p.NameJSON, encode(config))
	}
	return out
}
func (s *Service) Scope(ctx context.Context, scope string) (Result, error) {
	r := empty()
	source := map[string]string{"user": "userSettings", "project": "projectSettings", "local": "localSettings"}[scope]
	if source != "" && !s.options.SourceEnabled(source) {
		return r, nil
	}
	if err := ctx.Err(); err != nil {
		return r, err
	}
	if scope == "project" || scope == "enterprise" {
		paths := []string{filepath.Join(s.options.ManagedRoot, "managed-mcp.json")}
		if scope == "project" {
			paths = []string{}
			for dir := s.options.CWD; filepath.Dir(dir) != dir; dir = filepath.Dir(dir) {
				paths = append(paths, filepath.Join(dir, ".mcp.json"))
			}
			for i, j := 0, len(paths)-1; i < j; i, j = i+1, j-1 {
				paths[i], paths[j] = paths[j], paths[i]
			}
		}
		for _, path := range paths {
			config, failures := s.ParseFile(path, true, scope)
			for _, failure := range failures {
				if !strings.HasPrefix(failure.String("message"), "MCP config file not found") {
					r.Errors = append(r.Errors, failure)
				}
			}
			if config != nil {
				r.Servers.Assign(addScope(object(config.Get("mcpServers")), scope))
			}
		}
		return r, nil
	}
	var value protocol.JSONObject
	var err error
	switch scope {
	case "user":
		value, err = s.options.Global(ctx)
	case "local":
		value, err = s.options.Project(ctx)
	default:
		return r, fmt.Errorf("Unknown MCP config scope: %s", scope)
	}
	if err != nil {
		return r, err
	}
	if !truthy(value.Get("mcpServers")) {
		return r, nil
	}
	config, failures := s.Parse(encode(protocol.NewJSONObject(protocol.JSONField{Name: "mcpServers", Value: value.Get("mcpServers")})), true, scope, "")
	r.Errors = failures
	if config != nil {
		r.Servers = addScope(object(config.Get("mcpServers")), scope)
	}
	return r, nil
}
func (s *Service) EnterpriseExists() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.enterpriseExists == nil {
		config, _ := s.ParseFile(filepath.Join(s.options.ManagedRoot, "managed-mcp.json"), true, "enterprise")
		value := config != nil
		s.enterpriseExists = &value
	}
	return *s.enterpriseExists
}

func (s *Service) ByName(ctx context.Context, name string) (*protocol.JSONObject, error) {
	enterprise, err := s.Scope(ctx, "enterprise")
	if err != nil {
		return nil, err
	}
	policy, err := s.options.Policy(ctx)
	if err != nil {
		return nil, err
	}
	if restricted(policy) {
		if value := enterprise.Servers.Get(name); value != nil {
			config := object(value)
			return &config, nil
		}
		return nil, nil
	}
	user, err := s.Scope(ctx, "user")
	if err != nil {
		return nil, err
	}
	project, err := s.Scope(ctx, "project")
	if err != nil {
		return nil, err
	}
	local, err := s.Scope(ctx, "local")
	if err != nil {
		return nil, err
	}
	for _, servers := range []protocol.JSONObject{enterprise.Servers, local.Servers, project.Servers, user.Servers} {
		if value := servers.Get(name); value != nil {
			config := object(value)
			return &config, nil
		}
	}
	return nil, nil
}
func restricted(policy protocol.JSONObject) bool {
	if string(policy.Get("strictPluginOnlyCustomization")) == "true" {
		return true
	}
	for _, v := range list(policy.Get("strictPluginOnlyCustomization")) {
		if string(v) == `"mcp"` {
			return true
		}
	}
	return false
}
func command(config protocol.JSONObject) json.RawMessage {
	if k := config.String("type"); k != "" && k != "stdio" {
		return nil
	}
	items := []json.RawMessage{config.Get("command")}
	items = append(items, list(config.Get("args"))...)
	return encode(items)
}
func UnwrapProxyURL(value string) string {
	if !strings.Contains(value, "/v2/session_ingress/shttp/mcp/") && !strings.Contains(value, "/v2/ccr-sessions/") {
		return value
	}
	parsed, err := weburl.Parse(value)
	if err != nil {
		return value
	}
	if original := parsed.SearchParams().Get("mcp_url"); original != "" {
		return original
	}
	return value
}
func Signature(config protocol.JSONObject) string {
	if cmd := command(config); cmd != nil {
		return "stdio:" + string(protocol.StringifyValidatedJavaScriptJSON(cmd))
	}
	if value := config.String("url"); value != "" {
		return "url:" + UnwrapProxyURL(value)
	}
	return ""
}
func wildcard(value, pattern string) bool {
	expression := "^" + strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*") + "$"
	compiled, err := regexp.Compile(expression)
	return err == nil && compiled.MatchString(value)
}
func matchEntry(entry protocol.JSONObject, name string, config protocol.JSONObject, kind string) bool {
	switch kind {
	case "name":
		return entry.Get("serverName") != nil && entry.String("serverName") == name
	case "command":
		return entry.Get("serverCommand") != nil && command(config) != nil && bytesEqualJS(entry.Get("serverCommand"), command(config))
	case "url":
		return entry.Get("serverUrl") != nil && config.String("url") != "" && wildcard(config.String("url"), entry.String("serverUrl"))
	}
	return false
}
func bytesEqualJS(a, b json.RawMessage) bool {
	return string(protocol.StringifyValidatedJavaScriptJSON(a)) == string(protocol.StringifyValidatedJavaScriptJSON(b))
}
func Allowed(name string, config, settings, policy protocol.JSONObject) bool {
	if Denied(name, config, settings) {
		return false
	}
	allow := settings
	if string(policy.Get("allowManagedMcpServersOnly")) == "true" {
		allow = policy
	}
	if allow.Get("allowedMcpServers") == nil {
		return true
	}
	entries := list(allow.Get("allowedMcpServers"))
	kind := "name"
	if command(config) != nil {
		for _, raw := range entries {
			if object(raw).Get("serverCommand") != nil {
				kind = "command"
				break
			}
		}
	} else if config.String("url") != "" {
		for _, raw := range entries {
			if object(raw).Get("serverUrl") != nil {
				kind = "url"
				break
			}
		}
	}
	for _, raw := range entries {
		if matchEntry(object(raw), name, config, kind) {
			return true
		}
	}
	return false
}

type Suppression struct {
	Name        string `json:"name"`
	DuplicateOf string `json:"duplicateOf"`
}

func Dedup(plugins, manual protocol.JSONObject, amongPlugins bool) (protocol.JSONObject, []Suppression) {
	out := protocol.NewJSONObject()
	suppressed := []Suppression{}
	seen := map[string]string{}
	for _, p := range manual.OwnProperties() {
		if signature := Signature(object(p.Value)); signature != "" {
			if _, exists := seen[signature]; !exists {
				seen[signature] = name(p)
			}
		}
	}
	for _, p := range plugins.OwnProperties() {
		signature := Signature(object(p.Value))
		if previous, exists := seen[signature]; signature != "" && exists {
			suppressed = append(suppressed, Suppression{name(p), previous})
			continue
		}
		if amongPlugins && signature != "" {
			seen[signature] = name(p)
		}
		_ = out.SetPropertyJSON(p.NameJSON, p.Value)
	}
	return out, suppressed
}
func (s *Service) ProjectStatus(ctx context.Context, name string) (string, error) {
	settings, err := s.options.Settings(ctx)
	if err != nil {
		return "", err
	}
	normalized := mcpclient.NormalizeName(name)
	for _, raw := range list(settings.Get("disabledMcpjsonServers")) {
		var key string
		_ = json.Unmarshal(raw, &key)
		if mcpclient.NormalizeName(key) == normalized {
			return "rejected", nil
		}
	}
	for _, raw := range list(settings.Get("enabledMcpjsonServers")) {
		var key string
		_ = json.Unmarshal(raw, &key)
		if mcpclient.NormalizeName(key) == normalized {
			return "approved", nil
		}
	}
	if truthy(settings.Get("enableAllProjectMcpServers")) {
		return "approved", nil
	}
	confirmed, err := s.options.BypassConfirmed(ctx)
	if err != nil {
		return "", err
	}
	if s.options.SourceEnabled("projectSettings") && (s.options.NonInteractive || confirmed) {
		return "approved", nil
	}
	return "pending", nil
}
func (s *Service) Configurations(ctx context.Context, dynamic, extra protocol.JSONObject) (Result, error) {
	return s.ConfigurationsAwait(ctx, dynamic, func(context.Context) (protocol.JSONObject, error) { return extra, nil })
}

// ConfigurationsAwait observes the already-started extra producer at the
// original after-plugin boundary, so plugin work overlaps the remote fetch.
func (s *Service) ConfigurationsAwait(ctx context.Context, dynamic protocol.JSONObject, awaitExtra func(context.Context) (protocol.JSONObject, error)) (Result, error) {
	enterprise, err := s.Scope(ctx, "enterprise")
	if err != nil {
		return empty(), err
	}
	settings, err := s.options.Settings(ctx)
	if err != nil {
		return empty(), err
	}
	policy, err := s.options.Policy(ctx)
	if err != nil {
		return empty(), err
	}
	filter := func(in protocol.JSONObject) protocol.JSONObject {
		out := protocol.NewJSONObject()
		for _, p := range in.OwnProperties() {
			if Allowed(name(p), object(p.Value), settings, policy) {
				_ = out.SetPropertyJSON(p.NameJSON, p.Value)
			}
		}
		return out
	}
	if s.EnterpriseExists() {
		return Result{filter(enterprise.Servers), []protocol.JSONObject{}}, nil
	}
	user, project, local := empty(), empty(), empty()
	if !restricted(policy) {
		if user, err = s.Scope(ctx, "user"); err != nil {
			return empty(), err
		}
		if project, err = s.Scope(ctx, "project"); err != nil {
			return empty(), err
		}
		if local, err = s.Scope(ctx, "local"); err != nil {
			return empty(), err
		}
	}
	plugins, err := s.options.Plugins(ctx)
	if err != nil {
		return empty(), err
	}
	approved := protocol.NewJSONObject()
	for _, p := range project.Servers.OwnProperties() {
		status, err := s.ProjectStatus(ctx, name(p))
		if err != nil {
			return empty(), err
		}
		if status == "approved" {
			_ = approved.SetPropertyJSON(p.NameJSON, p.Value)
		}
	}
	extra, err := awaitExtra(ctx)
	if err != nil {
		return empty(), err
	}
	manual := protocol.NewJSONObject()
	for _, configs := range []protocol.JSONObject{user.Servers, approved, local.Servers, dynamic, extra} {
		manual.Assign(configs)
	}
	targets := protocol.NewJSONObject()
	for _, p := range manual.OwnProperties() {
		config := object(p.Value)
		enabled, err := s.options.Enabled(ctx, name(p), config)
		if err != nil {
			return empty(), err
		}
		if enabled && Allowed(name(p), config, settings, policy) {
			_ = targets.SetPropertyJSON(p.NameJSON, p.Value)
		}
	}
	enabledPlugins, disabledPlugins := protocol.NewJSONObject(), protocol.NewJSONObject()
	for _, p := range plugins.Servers.OwnProperties() {
		config := object(p.Value)
		enabled, err := s.options.Enabled(ctx, name(p), config)
		if err != nil {
			return empty(), err
		}
		target := &enabledPlugins
		if !enabled || !Allowed(name(p), config, settings, policy) {
			target = &disabledPlugins
		}
		_ = target.SetPropertyJSON(p.NameJSON, p.Value)
	}
	deduped, suppressed := Dedup(enabledPlugins, targets, true)
	deduped.Assign(disabledPlugins)
	for _, item := range suppressed {
		parts := strings.Split(item.Name, ":")
		if len(parts) >= 3 && parts[0] == "plugin" {
			plugins.Errors = append(plugins.Errors, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("mcp-server-suppressed-duplicate")}, protocol.JSONField{Name: "source", Value: quote(item.Name)}, protocol.JSONField{Name: "plugin", Value: quote(parts[1])}, protocol.JSONField{Name: "serverName", Value: quote(strings.Join(parts[2:], ":"))}, protocol.JSONField{Name: "duplicateOf", Value: quote(item.DuplicateOf)}))
		}
	}
	for _, configs := range []protocol.JSONObject{user.Servers, approved, local.Servers} {
		deduped.Assign(configs)
	}
	return Result{filter(deduped), plugins.Errors}, nil
}
