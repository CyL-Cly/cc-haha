package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	weburl "github.com/nlnwa/whatwg-url/url"
)

// RemoteDependencies are required actual runtime capabilities. The adapter
// cannot synthesize a connected state or a successful OAuth flow. Options are
// the same shared plugin settings/secure storage read by normal MCP loading.
type RemoteDependencies struct {
	Probe               func(context.Context, string, protocol.JSONObject) (RemoteProbe, error)
	OAuth               func(context.Context, string, protocol.JSONObject, func(string) error) error
	Disconnect          func(context.Context, string, protocol.JSONObject) error
	LoadOptions         func(context.Context, string) (protocol.JSONObject, error)
	SaveOptions         func(context.Context, string, protocol.JSONObject, protocol.JSONObject) error
	DeleteOptions       func(context.Context, string) error
	ClearOAuth          func(context.Context, string, protocol.JSONObject) error
	Environment         func(string) (string, bool)
	PluginDataDirectory func(context.Context, string) (string, error)
}
type RemoteProbe struct {
	Status    string
	ToolCount int
}
type RemoteCheck struct {
	Authenticated bool   `json:"authenticated"`
	Verification  string `json:"verification"`
	ToolCount     int    `json:"toolCount"`
}
type RemoteBridge struct {
	recipe       RemoteRecipe
	dependencies RemoteDependencies
	template     protocol.JSONObject
	ServerName   string
}

func NewRemoteBridge(recipe RemoteRecipe, d RemoteDependencies) (*RemoteBridge, error) {
	plugin, err := BuildRemotePlugin(recipe)
	if err != nil {
		return nil, err
	}
	if d.Probe == nil || d.OAuth == nil || d.Disconnect == nil || d.LoadOptions == nil || d.SaveOptions == nil || d.DeleteOptions == nil || d.ClearOAuth == nil || d.Environment == nil || d.PluginDataDirectory == nil {
		return nil, errors.New("remote connector requires actual MCP, OAuth, shared plugin options and environment primitives")
	}
	return &RemoteBridge{recipe, d, nested(nested(plugin.MCPConfig.Get("mcpServers")).Get("service")), "plugin:office-" + recipe.ID + ":service"}, nil
}
func (b *RemoteBridge) IsConfigured(ctx context.Context) (bool, error) {
	if b.recipe.Auth.Type != "api-key" {
		return true, nil
	}
	options, err := b.dependencies.LoadOptions(ctx, b.recipe.PluginID)
	if err != nil {
		return false, err
	}
	units, err := protocol.DecodeJavaScriptJSONString(options.Get("apiKey"))
	return err == nil && len(units) > 0, nil
}

var remoteUserVariable = regexp.MustCompile(`\$\{user_config\.([^}]+)\}`)
var remoteEnvironmentVariable = regexp.MustCompile(`\$\{([^}]+)\}`)

func (b *RemoteBridge) resolveValue(ctx context.Context, value string, options protocol.JSONObject) (string, error) {
	value = strings.ReplaceAll(value, "${CLAUDE_PLUGIN_ROOT}", "")
	if strings.Contains(value, "${CLAUDE_PLUGIN_DATA}") {
		directory, err := b.dependencies.PluginDataDirectory(ctx, b.recipe.PluginID)
		if err != nil {
			return "", err
		}
		if runtime.GOOS == "windows" {
			directory = strings.ReplaceAll(directory, "\\", "/")
		}
		value = strings.ReplaceAll(value, "${CLAUDE_PLUGIN_DATA}", directory)
	}
	var substitutionError error
	value = remoteUserVariable.ReplaceAllStringFunc(value, func(match string) string {
		key := remoteUserVariable.FindStringSubmatch(match)[1]
		raw := options.Get(key)
		if raw == nil {
			substitutionError = errors.New("Missing required user configuration value: " + key + ". This should have been validated before variable substitution.")
			return match
		}
		text, err := protocol.JavaScriptStringJSON(raw)
		if err != nil {
			substitutionError = err
			return match
		}
		units, err := protocol.DecodeJavaScriptJSONString(text)
		if err != nil {
			substitutionError = err
			return match
		}
		return string(utf16.Decode(units))
	})
	if substitutionError != nil {
		return "", substitutionError
	}
	return remoteEnvironmentVariable.ReplaceAllStringFunc(value, func(match string) string {
		content := remoteEnvironmentVariable.FindStringSubmatch(match)[1]
		parts := strings.Split(content, ":-")
		name := parts[0]
		if text, exists := b.dependencies.Environment(name); exists {
			return text
		}
		if len(parts) > 1 {
			return parts[1]
		}
		return match
	}), nil
}
func (b *RemoteBridge) Config(ctx context.Context) (protocol.JSONObject, error) {
	configured, err := b.IsConfigured(ctx)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if !configured {
		return protocol.JSONObject{}, errors.New("Configure the connector API key first")
	}
	options, err := b.dependencies.LoadOptions(ctx, b.recipe.PluginID)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	config := b.template.Clone()
	if value := config.String("url"); value != "" {
		value, err = b.resolveValue(ctx, value, options)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		config.Set("url", quote(value))
	}
	if headers := nested(config.Get("headers")); headers.IsObject() {
		resolved := protocol.NewJSONObject()
		for _, property := range headers.OwnProperties() {
			var name string
			_ = json.Unmarshal(property.NameJSON, &name)
			value := headers.String(name)
			value, err = b.resolveValue(ctx, value, options)
			if err != nil {
				return protocol.JSONObject{}, err
			}
			resolved.Set(name, quote(value))
		}
		config.Set("headers", raw(resolved))
	}
	config.Set("scope", quote("dynamic"))
	config.Set("pluginSource", quote(b.recipe.PluginID))
	return config, nil
}
func (b *RemoteBridge) Check(ctx context.Context) (RemoteCheck, error) {
	config, err := b.Config(ctx)
	if err != nil {
		return RemoteCheck{}, err
	}
	failed := func() (RemoteCheck, error) {
		return RemoteCheck{}, errors.New("Remote connector connection or tool discovery failed")
	}
	if ctx.Err() != nil {
		return failed()
	}
	if err = b.dependencies.Disconnect(ctx, b.ServerName, config); err != nil {
		return failed()
	}
	if ctx.Err() != nil {
		return failed()
	}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	type reply struct {
		result RemoteProbe
		err    error
	}
	done := make(chan reply, 1)
	go func() {
		result, err := b.dependencies.Probe(probeCtx, b.ServerName, config)
		done <- reply{result, err}
	}()
	var result RemoteProbe
	select {
	case response := <-done:
		result, err = response.result, response.err
	case <-probeCtx.Done():
		// Source abortable cancels its cached client without waiting for a
		// transport that may still be completing a request.
		go func() { _ = b.dependencies.Disconnect(context.WithoutCancel(ctx), b.ServerName, config) }()
		return failed()
	}
	if err != nil || probeCtx.Err() != nil {
		return failed()
	}
	if result.Status == "needs-auth" {
		return RemoteCheck{false, "remote", 0}, nil
	}
	if result.Status != "connected" || result.ToolCount < 1 {
		return RemoteCheck{}, errors.New("Unable to connect to the remote connector")
	}
	return RemoteCheck{true, "remote", result.ToolCount}, nil
}
func (b *RemoteBridge) Authenticate(ctx context.Context, p Progress) error {
	if b.recipe.Auth.Type != "oauth" {
		configured, err := b.IsConfigured(ctx)
		if err != nil {
			return err
		}
		if !configured {
			return errors.New("Configure the connector API key first")
		}
		return nil
	}
	if p != nil {
		p("authorizing", nil)
	}
	config, err := b.Config(ctx)
	if err != nil {
		return err
	}
	authCtx, cancel := context.WithTimeout(ctx, 300*time.Second)
	defer cancel()
	err = b.dependencies.OAuth(authCtx, b.ServerName, config, func(candidate string) error {
		url, err := weburl.Parse(candidate)
		if err != nil {
			return err
		}
		if url.Scheme() != "https" || url.Username() != "" || url.Password() != "" {
			return errors.New("Invalid remote connector authorization URL")
		}
		value := url.Href(false)
		if p != nil {
			p("awaiting-authorization", &value)
		}
		return nil
	})
	if err != nil || authCtx.Err() != nil {
		return errors.New("Remote connector authorization did not complete")
	}
	return b.dependencies.Disconnect(ctx, b.ServerName, config)
}
func (b *RemoteBridge) Deactivate(ctx context.Context) error {
	configured, err := b.IsConfigured(ctx)
	if err != nil {
		return err
	}
	if !configured {
		return nil
	}
	config, err := b.Config(ctx)
	if err != nil {
		return err
	}
	return b.dependencies.Disconnect(ctx, b.ServerName, config)
}
func (b *RemoteBridge) RemoveCredentials(ctx context.Context) error {
	configured, err := b.IsConfigured(ctx)
	if err != nil {
		return err
	}
	if configured {
		config, err := b.Config(ctx)
		if err != nil {
			return err
		}
		if err = b.dependencies.Disconnect(ctx, b.ServerName, config); err != nil {
			return err
		}
		if b.recipe.Auth.Type == "oauth" {
			if err = b.dependencies.ClearOAuth(ctx, b.ServerName, config); err != nil {
				return err
			}
		}
	}
	return b.dependencies.DeleteOptions(ctx, b.recipe.PluginID)
}
func remoteAPIKeySchema() protocol.JSONObject {
	return record(field("apiKey", record(field("type", "string"), field("title", "API Key"), field("description", "Service API key for this connector"), field("sensitive", true), field("required", true))))
}
func (b *RemoteBridge) SaveAPIKey(ctx context.Context, value string) error {
	if err := validateRemoteRecipe(b.recipe); err != nil {
		return err
	}
	if b.recipe.Auth.Type != "api-key" {
		return errors.New("This connector does not use an API key")
	}
	key := trimJS(value)
	if !validRemoteKey(key) {
		return errors.New("A valid connector API key is required")
	}
	if b.recipe.Auth.In == "query" {
		key = remoteEncodeURIComponent(key)
	}
	return b.dependencies.SaveOptions(ctx, b.recipe.PluginID, record(field("apiKey", key)), remoteAPIKeySchema())
}
func validRemoteKey(value string) bool {
	if value == "" || len(utf16.Encode([]rune(value))) > 8192 || strings.Contains(value, "${") {
		return false
	}
	for _, r := range value {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}
func remoteEncodeURIComponent(value string) string {
	const digits = "0123456789ABCDEF"
	var out strings.Builder
	for _, b := range []byte(value) {
		if b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || strings.ContainsRune("-_.!~*'()", rune(b)) {
			out.WriteByte(b)
		} else {
			out.WriteByte('%')
			out.WriteByte(digits[b>>4])
			out.WriteByte(digits[b&15])
		}
	}
	return out.String()
}

type remoteAdapter struct {
	installation Installation
	bridge       *RemoteBridge
	recipe       RemoteRecipe
}
type remoteAbortError struct{ cause error }

func (e remoteAbortError) Error() string {
	if errors.Is(e.cause, context.DeadlineExceeded) {
		return "The operation timed out."
	}
	return "The operation was aborted."
}
func (e remoteAbortError) Unwrap() error { return e.cause }
func remoteContextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return remoteAbortError{err}
	}
	return nil
}

var remoteVersion = regexp.MustCompile(`^\d+\.\d+\.\d+(?:[-+][A-Za-z0-9.-]+)?$`)

func RemoteAdapter(def protocol.JSONObject, root string, recipe RemoteRecipe, d RemoteDependencies) (Adapter, error) {
	if _, err := BuildRemotePlugin(recipe); err != nil {
		return nil, err
	}
	if def.String("id") != recipe.ID || def.String("pluginId") != recipe.PluginID || def.String("version") != recipe.Version || !remoteVersion.MatchString(def.String("version")) {
		return nil, errors.New("Invalid managed remote connector definition")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	bridge, err := NewRemoteBridge(recipe, d)
	if err != nil {
		return nil, err
	}
	return &remoteAdapter{Installation{Directory: filepath.Join(root, "remote", recipe.ID, def.String("version")), Command: "", Args: []string{}, Environment: map[string]string{}}, bridge, recipe}, nil
}
func (a *remoteAdapter) owned(i Installation) error {
	directory, err := filepath.Abs(i.Directory)
	if err != nil || directory != a.installation.Directory || i.Command != "" || len(i.Args) != 0 || len(i.Environment) != 0 {
		return errors.New("Invalid managed remote connector installation")
	}
	return nil
}
func (a *remoteAdapter) Configure(ctx context.Context, configuration map[string]string) error {
	for key := range configuration {
		if key != "apiKey" {
			return errors.New("Unsupported remote connector configuration field")
		}
	}
	key := trimJS(configuration["apiKey"])
	if key == "" {
		return nil
	}
	if a.recipe.Auth.Type != "api-key" {
		return errors.New("This connector does not use an API key")
	}
	if !validRemoteKey(key) {
		return errors.New("A valid connector API key is required")
	}
	if err := a.bridge.Deactivate(ctx); err != nil {
		return err
	}
	return a.bridge.SaveAPIKey(ctx, key)
}
func (a *remoteAdapter) Prepare(ctx context.Context, p Progress) (Installation, error) {
	if err := remoteContextError(ctx); err != nil {
		return Installation{}, err
	}
	if p != nil {
		p("preparing-plugin", nil)
	}
	if err := os.MkdirAll(a.installation.Directory, 0700); err != nil {
		return Installation{}, err
	}
	if err := remoteContextError(ctx); err != nil {
		return Installation{}, err
	}
	return Installation{Directory: a.installation.Directory, Command: "", Args: []string{}, Environment: map[string]string{}}, nil
}
func (a *remoteAdapter) Authenticate(ctx context.Context, i Installation, p Progress) error {
	if err := a.owned(i); err != nil {
		return err
	}
	if err := remoteContextError(ctx); err != nil {
		return err
	}
	configured, err := a.bridge.IsConfigured(ctx)
	if err != nil {
		return err
	}
	if !configured {
		return errors.New("Configure the connector API key first")
	}
	return a.bridge.Authenticate(ctx, p)
}
func (a *remoteAdapter) Check(ctx context.Context, i Installation) (Check, error) {
	if err := a.owned(i); err != nil {
		return Check{}, err
	}
	if err := remoteContextError(ctx); err != nil {
		return Check{}, err
	}
	configured, err := a.bridge.IsConfigured(ctx)
	if err != nil {
		return Check{}, err
	}
	if !configured {
		return Check{Verification: "remote"}, nil
	}
	result, err := a.bridge.Check(ctx)
	if err != nil {
		return Check{}, err
	}
	count := result.ToolCount
	return Check{Authenticated: result.Authenticated, Verification: result.Verification, ToolCount: &count}, nil
}
func (a *remoteAdapter) Deactivate(ctx context.Context) error { return a.bridge.Deactivate(ctx) }
func (a *remoteAdapter) Remove(ctx context.Context, i Installation) error {
	if err := a.owned(i); err != nil {
		return err
	}
	if err := a.bridge.RemoveCredentials(ctx); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Dir(a.installation.Directory))
}
