package mcpconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Update func(*protocol.JSONObject) (*protocol.JSONObject, error)
type EditorOptions struct {
	Configurations          *Service
	GlobalPath, ProjectKey  string
	SaveGlobal, SaveProject func(context.Context, Update) error
	WriteProjectFile        func(context.Context, string, []byte) error
}
type Editor struct{ options EditorOptions }

func NewEditor(o EditorOptions) (*Editor, error) {
	if o.Configurations == nil || !filepath.IsAbs(o.GlobalPath) || !filepath.IsAbs(o.ProjectKey) || o.SaveGlobal == nil || o.SaveProject == nil || o.WriteProjectFile == nil {
		return nil, errors.New("MCP editing requires original shared persistence, canonical project identity and atomic file writer")
	}
	return &Editor{options: o}, nil
}
func (e *Editor) readRaw(path string) (protocol.JSONObject, error) {
	data, err := e.options.Configurations.options.ReadFile(path)
	if os.IsNotExist(err) {
		return protocol.NewJSONObject(), nil
	}
	if err != nil {
		return protocol.JSONObject{}, err
	}
	value := object(bytes.TrimPrefix(protocol.DecodeJavaScriptUTF8(data), []byte{0xef, 0xbb, 0xbf}))
	if !value.IsObject() {
		return protocol.NewJSONObject(), nil
	}
	return value, nil
}
func (e *Editor) ProjectFile(name string) (string, error) {
	s := e.options.Configurations
	if !s.options.SourceEnabled("projectSettings") {
		return "", nil
	}
	for dir := s.options.CWD; filepath.Dir(dir) != dir; dir = filepath.Dir(dir) {
		path := filepath.Join(dir, ".mcp.json")
		value, err := e.readRaw(path)
		if err != nil {
			return "", err
		}
		servers := object(value.Get("mcpServers"))
		if servers.IsObject() && servers.Get(name) != nil {
			return path, nil
		}
	}
	return "", nil
}
func (e *Editor) Location(name, scope string) (string, error) {
	s := e.options.Configurations
	switch scope {
	case "project":
		path, err := e.ProjectFile(name)
		if err != nil || path != "" {
			return path, err
		}
		return filepath.Join(s.options.CWD, ".mcp.json"), nil
	case "enterprise":
		return filepath.Join(s.options.ManagedRoot, "managed-mcp.json"), nil
	case "local":
		return e.options.GlobalPath + " [project: " + s.options.CWD + "]", nil
	case "dynamic":
		return "Dynamically configured", nil
	case "claudeai":
		return "claude.ai", nil
	default:
		if scope == "user" {
			return e.options.GlobalPath, nil
		}
		return scope, nil
	}
}
func (e *Editor) ProjectKey() string { return e.options.ProjectKey }
func (e *Editor) ProjectPath(name, scope string) (string, error) {
	if scope == "local" {
		return strings.ReplaceAll(e.options.ProjectKey, "\\", "/"), nil
	}
	if scope == "project" {
		path, err := e.ProjectFile(name)
		if err != nil || path == "" {
			return "", err
		}
		return strings.ReplaceAll(filepath.Dir(path), "\\", "/"), nil
	}
	return "", nil
}
func (e *Editor) Declares(path string) (bool, error) {
	config, err := e.readRaw(filepath.Join(path, ".mcp.json"))
	if err != nil {
		return false, err
	}
	servers := object(config.Get("mcpServers"))
	return servers.IsObject() && len(servers.OwnProperties()) > 0, nil
}
func (e *Editor) RegisterProject(ctx context.Context) error {
	s := e.options.Configurations
	global, err := s.options.Global(ctx)
	if err != nil {
		return err
	}
	if truthy(object(global.Get("projects")).Get(e.options.ProjectKey)) {
		return nil
	}
	declares, err := e.Declares(s.options.CWD)
	if err != nil || !declares {
		return err
	}
	return e.options.SaveProject(ctx, func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		next := current.Clone()
		return &next, nil
	})
}
func (e *Editor) ProjectPaths(ctx context.Context) ([]string, error) {
	global, err := e.options.Configurations.options.Global(ctx)
	if err != nil {
		return nil, err
	}
	paths := []string{}
	for _, p := range object(global.Get("projects")).OwnProperties() {
		if len(object(object(p.Value).Get("mcpServers")).OwnProperties()) > 0 {
			paths = append(paths, name(p))
			continue
		}
		declares, err := e.Declares(name(p))
		if err != nil {
			return nil, err
		}
		if declares {
			paths = append(paths, name(p))
		}
	}
	return paths, nil
}
func Denied(name string, config, settings protocol.JSONObject) bool {
	for _, raw := range list(settings.Get("deniedMcpServers")) {
		entry := object(raw)
		for _, kind := range []string{"name", "command", "url"} {
			if matchEntry(entry, name, config, kind) {
				return true
			}
		}
	}
	return false
}
func (e *Editor) Add(ctx context.Context, name, scope string, raw json.RawMessage) error {
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_' && r != '-' {
			return fmt.Errorf("Invalid name %s. Names can only contain letters, numbers, hyphens, and underscores.", name)
		}
	}
	if normalized := mcpclient.NormalizeName(name); normalized == "claude-in-chrome" || normalized == "computer-use" {
		return fmt.Errorf("Cannot add MCP server %q: this name is reserved.", name)
	}
	s := e.options.Configurations
	if s.EnterpriseExists() {
		return errors.New("Cannot add MCP server: enterprise MCP configuration is active and has exclusive control over MCP servers")
	}
	config, issues := ValidateServer(raw)
	if len(issues) > 0 {
		formatted := []string{}
		for _, path := range issues {
			message := "Invalid input"
			if path == "command" {
				message = "Too small: expected string to have >=1 characters"
			}
			if path == "oauth.callbackPort" {
				message = "Too small: expected number to be >0"
			}
			formatted = append(formatted, path+": "+message)
		}
		return errors.New("Invalid configuration: " + strings.Join(formatted, ", "))
	}
	settings, err := s.options.Settings(ctx)
	if err != nil {
		return err
	}
	policy, err := s.options.Policy(ctx)
	if err != nil {
		return err
	}
	if Denied(name, config, settings) {
		return fmt.Errorf("Cannot add MCP server %q: server is explicitly blocked by enterprise policy", name)
	}
	if !Allowed(name, config, settings, policy) {
		return fmt.Errorf("Cannot add MCP server %q: not allowed by enterprise policy", name)
	}
	var existing protocol.JSONObject
	switch scope {
	case "project":
		existing, err = e.readRaw(filepath.Join(s.options.CWD, ".mcp.json"))
	case "user":
		existing, err = s.options.Global(ctx)
	case "local":
		existing, err = s.options.Project(ctx)
	default:
		return fmt.Errorf("Cannot add MCP server to scope: %s", scope)
	}
	if err != nil {
		return err
	}
	if truthy(object(existing.Get("mcpServers")).Get(name)) {
		label := map[string]string{"project": ".mcp.json", "user": "user config", "local": "local config"}[scope]
		return fmt.Errorf("MCP server %s already exists in %s", name, label)
	}
	update := func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		next := current.Clone()
		servers := object(next.Get("mcpServers"))
		if !servers.IsObject() {
			servers = protocol.NewJSONObject()
		}
		servers.Set(name, encode(config))
		next.Set("mcpServers", encode(servers))
		return &next, nil
	}
	switch scope {
	case "project":
		value, _ := update(&existing)
		data, err := pretty(*value)
		if err != nil {
			return err
		}
		if err = e.options.WriteProjectFile(ctx, filepath.Join(s.options.CWD, ".mcp.json"), data); err != nil {
			return fmt.Errorf("Failed to write to .mcp.json: %s", err)
		}
		return e.RegisterProject(ctx)
	case "user":
		return e.options.SaveGlobal(ctx, update)
	default:
		return e.options.SaveProject(ctx, update)
	}
}
func pretty(value protocol.JSONObject) ([]byte, error) {
	var out bytes.Buffer
	if err := json.Indent(&out, protocol.StringifyValidatedJavaScriptJSON(encode(value)), "", "  "); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
func (e *Editor) Remove(ctx context.Context, name, scope string) error {
	s := e.options.Configurations
	update := func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		next := current.Clone()
		servers := object(next.Get("mcpServers"))
		servers.Delete(name)
		next.Set("mcpServers", encode(servers))
		return &next, nil
	}
	switch scope {
	case "project":
		path, err := e.ProjectFile(name)
		if err != nil {
			return err
		}
		if path == "" {
			return fmt.Errorf("No MCP server found with name: %s in .mcp.json", name)
		}
		current, err := e.readRaw(path)
		if err != nil {
			return err
		}
		next, _ := update(&current)
		data, err := pretty(*next)
		if err != nil {
			return err
		}
		if err = e.options.WriteProjectFile(ctx, path, data); err != nil {
			return fmt.Errorf("Failed to remove from %s: %s", path, err)
		}
		return nil
	case "user", "local":
		read, save := s.options.Global, e.options.SaveGlobal
		label := "user-scoped"
		if scope == "local" {
			read, save = s.options.Project, e.options.SaveProject
			label = "project-local"
		}
		current, err := read(ctx)
		if err != nil {
			return err
		}
		if !truthy(object(current.Get("mcpServers")).Get(name)) {
			return fmt.Errorf("No %s MCP server found with name: %s", label, name)
		}
		return save(ctx, update)
	default:
		return fmt.Errorf("Cannot remove MCP server from scope: %s", scope)
	}
}
func (e *Editor) SetEnabled(ctx context.Context, name string, enabled bool) error {
	return e.options.SaveProject(ctx, func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		var prev []string
		if raw := current.Get("disabledMcpServers"); raw != nil {
			if err := json.Unmarshal(raw, &prev); err != nil {
				return nil, err
			}
		}
		next := []string{}
		found := false
		for _, v := range prev {
			if v == name {
				found = true
				if enabled {
					continue
				}
			}
			next = append(next, v)
		}
		if !enabled && !found {
			next = append(next, name)
		}
		if enabled && !found || !enabled && found {
			return current, nil
		}
		updated := current.Clone()
		updated.Set("disabledMcpServers", encode(next))
		return &updated, nil
	})
}
