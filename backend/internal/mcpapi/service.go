// Package mcpapi exposes original desktop MCP management operations over the
// native shared configuration, persistence and transport services.
package mcpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

type Options struct {
	CWD            string
	Configurations *mcpconfig.Service
	Editor         *mcpconfig.Editor
	Runtime        *mcpruntime.Service
	EnableProbes   *EnableQueue
	Preflight      func(context.Context, protocol.JSONObject) error
	SyncToggle     func(context.Context, string, string, protocol.JSONObject, bool) (protocol.JSONObject, error)
	ForCWD         func(context.Context, string) (*Service, error)
	// All resolves original getAllMcpConfigs using the shared process catalog.
	// Nil retains the code-only profile used by existing native component fixtures.
	All func(context.Context) (mcpconfig.Result, error)
}
type Service struct{ options Options }

func New(o Options) (*Service, error) {
	if o.CWD == "" || o.Configurations == nil || o.Editor == nil || o.Runtime == nil || o.EnableProbes == nil || o.Preflight == nil || o.SyncToggle == nil || o.ForCWD == nil {
		return nil, errors.New("MCP API requires native configuration, editor, transport, host preflight and active-session bindings")
	}
	return &Service{options: o}, nil
}
func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func object(v json.RawMessage) protocol.JSONObject {
	var o protocol.JSONObject
	_ = o.UnmarshalJSON(v)
	return o
}
func Scope(value string) (string, error) {
	if value == "" {
		return "local", nil
	}
	for _, s := range []string{"local", "user", "project", "dynamic", "enterprise", "claudeai", "managed"} {
		if s == value {
			return value, nil
		}
	}
	return "", &Error{400, "Invalid scope: " + value + ". Must be one of: local, user, project, dynamic, enterprise, claudeai, managed"}
}
func editable(scope string) bool { return scope == "local" || scope == "user" || scope == "project" }
func BuildConfig(input json.RawMessage) (protocol.JSONObject, error) {
	o := object(input)
	if !o.IsObject() {
		return protocol.JSONObject{}, &Error{400, `Missing or invalid "config" in request body`}
	}
	kind := o.String("type")
	out := protocol.NewJSONObject()
	stringsMap := func(key string) {
		values := object(o.Get(key))
		filtered := protocol.NewJSONObject()
		for _, p := range values.OwnProperties() {
			var name, value string
			if json.Unmarshal(p.NameJSON, &name) == nil && json.Unmarshal(p.Value, &value) == nil && strings.TrimSpace(name) != "" {
				_ = filtered.SetPropertyJSON(p.NameJSON, p.Value)
			}
		}
		if len(filtered.OwnProperties()) > 0 {
			out.Set(key, raw(filtered))
		}
	}
	if kind == "" || kind == "stdio" {
		command := strings.TrimSpace(o.String("command"))
		if command == "" {
			return out, &Error{400, "Command is required for stdio MCP servers"}
		}
		out.Set("type", raw("stdio"))
		out.Set("command", raw(command))
		args := []json.RawMessage{}
		var values []json.RawMessage
		_ = json.Unmarshal(o.Get("args"), &values)
		for _, v := range values {
			var text string
			if json.Unmarshal(v, &text) == nil && strings.TrimSpace(text) != "" {
				args = append(args, v)
			}
		}
		out.Set("args", raw(args))
		stringsMap("env")
		return out, nil
	}
	if kind == "http" || kind == "sse" {
		address := strings.TrimSpace(o.String("url"))
		if address == "" {
			return out, &Error{400, "URL is required for remote MCP servers"}
		}
		out.Set("type", raw(kind))
		out.Set("url", raw(address))
		stringsMap("headers")
		if helper := strings.TrimSpace(o.String("headersHelper")); helper != "" {
			out.Set("headersHelper", raw(helper))
		}
		oauth := object(o.Get("oauth"))
		filtered := protocol.NewJSONObject()
		if client := strings.TrimSpace(oauth.String("clientId")); client != "" {
			filtered.Set("clientId", raw(client))
		}
		if v := oauth.Get("callbackPort"); v != nil {
			var number float64
			if json.Unmarshal(v, &number) != nil {
				var text string
				if json.Unmarshal(v, &text) == nil {
					number = parseNumber(strings.TrimSpace(text))
				}
			}
			if number != 0 && !math.IsNaN(number) {
				v := raw(number)
				if v == nil {
					v = json.RawMessage("null")
				}
				filtered.Set("callbackPort", v)
			}
		}
		if len(filtered.OwnProperties()) > 0 {
			out.Set("oauth", raw(filtered))
		}
		return out, nil
	}
	return out, &Error{400, "Unsupported MCP transport: " + kind}
}
func parseNumber(text string) float64 {
	if text == "" {
		return 0
	}
	if len(text) > 2 && text[0] == '0' {
		base := 0
		switch text[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 {
			n, err := strconv.ParseUint(text[2:], base, 64)
			if err == nil {
				return float64(n)
			}
			return math.NaN()
		}
	}
	n, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return math.NaN()
	}
	return n
}
func (s *Service) Resolve(ctx context.Context, name string) (*protocol.JSONObject, error) {
	if value, err := s.options.Configurations.ByName(ctx, name); err != nil || value != nil {
		return value, err
	}
	result, err := s.all(ctx)
	if err != nil {
		return nil, err
	}
	if value := result.Servers.Get(name); value != nil {
		o := object(value)
		return &o, nil
	}
	return nil, nil
}
func (s *Service) existing(ctx context.Context, name string) (protocol.JSONObject, error) {
	value, err := s.Resolve(ctx, name)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if value == nil {
		return protocol.JSONObject{}, &Error{404, "MCP server not found: " + name}
	}
	return value.Clone(), nil
}
func (s *Service) Server(ctx context.Context, name string, config protocol.JSONObject, probe bool) (protocol.JSONObject, error) {
	enabled, err := s.options.Runtime.ExecutionEnabled(ctx, name, config)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	status, detail := "checking", ""
	if !enabled {
		status, detail = "disabled", "Server disabled for the current project"
	} else if probe {
		if err = s.options.Preflight(ctx, config); err != nil {
			status, detail = "failed", err.Error()
		} else {
			connection, failure := s.options.Runtime.ConnectTransport(ctx, name, config)
			if failure != nil {
				status, detail = "failed", failure.Error()
			} else {
				status = connection.Status
				if status != "connected" && status != "needs-auth" {
					status = "failed"
				}
				if connection.Failure != nil {
					detail = connection.Failure.Error()
				}
				_ = s.options.Runtime.CloseProbe(ctx, connection)
			}
		}
	}
	// Enablement may have changed while the status probe was in flight.
	enabled, err = s.options.Runtime.ExecutionEnabled(ctx, name, config)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if !enabled {
		status, detail = "disabled", "Server disabled for the current project"
	}
	scope, kind := config.String("scope"), config.String("type")
	if kind == "" {
		kind = "stdio"
	}
	location, err := s.options.Editor.Location(name, scope)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	project, err := s.options.Editor.ProjectPath(name, scope)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	summary := kind
	dto := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw(kind)})
	switch kind {
	case "stdio":
		dto.Set("command", config.Get("command"))
		args := config.Get("args")
		if args == nil {
			args = raw([]string{})
		}
		dto.Set("args", args)
		env := config.Get("env")
		if env == nil {
			env = raw(protocol.NewJSONObject())
		}
		dto.Set("env", env)
		var values []string
		_ = json.Unmarshal(args, &values)
		summary = strings.TrimSpace(strings.Join(append([]string{config.String("command")}, values...), " "))
	case "http", "sse":
		summary = config.String("url")
		dto.Set("url", config.Get("url"))
		headers := config.Get("headers")
		if headers == nil {
			headers = raw(protocol.NewJSONObject())
		}
		dto.Set("headers", headers)
		if v := config.Get("headersHelper"); v != nil {
			dto.Set("headersHelper", v)
		}
		if v := config.Get("oauth"); v != nil {
			source := object(v)
			oauth := protocol.NewJSONObject()
			for _, k := range []string{"clientId", "callbackPort"} {
				if v := source.Get(k); v != nil {
					oauth.Set(k, v)
				}
			}
			dto.Set("oauth", raw(oauth))
		}
	default:
		if config.String("url") != "" {
			summary = config.String("url")
		}
	}
	label := map[string]string{"checking": "Checking", "connected": "Connected", "needs-auth": "Needs auth", "failed": "Unavailable", "disabled": "Disabled"}[status]
	out := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: raw(name)}, protocol.JSONField{Name: "scope", Value: raw(scope)}, protocol.JSONField{Name: "transport", Value: raw(kind)}, protocol.JSONField{Name: "enabled", Value: raw(enabled)}, protocol.JSONField{Name: "status", Value: raw(status)}, protocol.JSONField{Name: "statusLabel", Value: raw(label)}, protocol.JSONField{Name: "configLocation", Value: raw(location)}, protocol.JSONField{Name: "summary", Value: raw(summary)}, protocol.JSONField{Name: "canEdit", Value: raw(editable(scope) && (kind == "stdio" || kind == "http" || kind == "sse"))}, protocol.JSONField{Name: "canRemove", Value: raw(editable(scope))}, protocol.JSONField{Name: "canReconnect", Value: raw(enabled)}, protocol.JSONField{Name: "canToggle", Value: raw(true)}, protocol.JSONField{Name: "config", Value: raw(dto)})
	if detail != "" {
		out.Set("statusDetail", raw(detail))
	}
	if project != "" {
		out.Set("projectPath", raw(project))
	}
	return out, nil
}
func (s *Service) List(ctx context.Context) ([]protocol.JSONObject, error) {
	if err := s.options.Editor.RegisterProject(ctx); err != nil {
		return nil, err
	}
	configs, err := s.all(ctx)
	if err != nil {
		return nil, err
	}
	out := []protocol.JSONObject{}
	for _, p := range configs.Servers.OwnProperties() {
		var name string
		_ = json.Unmarshal(p.NameJSON, &name)
		config := object(p.Value)
		if name == "ide" || config.String("type") == "sse-ide" || config.String("type") == "ws-ide" {
			continue
		}
		value, err := s.Server(ctx, name, config, false)
		if err != nil {
			return nil, err
		}
		// The lookup uses the native string boundary, while the DTO retains
		// original JSON key units (including a lone UTF-16 surrogate).
		value.Set("name", p.NameJSON)
		out = append(out, value)
	}
	if err := sortServerNames(out); err != nil {
		return nil, err
	}
	return out, nil
}
func mutationError(err error) error {
	if err == nil {
		return nil
	}
	code := 400
	if strings.Contains(err.Error(), "already exists") {
		code = 409
	}
	return &Error{code, err.Error()}
}
func (s *Service) Create(ctx context.Context, body protocol.JSONObject) (protocol.JSONObject, error) {
	name := strings.TrimSpace(body.String("name"))
	if name == "" {
		return protocol.JSONObject{}, &Error{400, `Missing or invalid "name" in request body`}
	}
	scope, err := Scope(body.String("scope"))
	if err != nil {
		return protocol.JSONObject{}, &Error{500, "An unexpected error occurred"}
	}
	config, err := BuildConfig(body.Get("config"))
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if err = s.options.Preflight(ctx, config); err != nil {
		return protocol.JSONObject{}, mutationError(err)
	}
	if err = s.options.Editor.Add(ctx, name, scope, raw(config)); err != nil {
		return protocol.JSONObject{}, mutationError(err)
	}
	created, err := s.options.Configurations.ByName(ctx, name)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if created == nil {
		return protocol.JSONObject{}, &Error{500, fmt.Sprintf("Created MCP server %q could not be reloaded", name)}
	}
	return s.Server(ctx, name, *created, false)
}
func strip(config protocol.JSONObject) protocol.JSONObject {
	config = config.Clone()
	config.Delete("scope")
	config.Delete("pluginSource")
	return config
}
func (s *Service) Update(ctx context.Context, name string, body protocol.JSONObject) (protocol.JSONObject, error) {
	previous := s
	if cwd := body.String("previousCwd"); cwd != "" {
		var err error
		previous, err = s.options.ForCWD(ctx, cwd)
		if err != nil {
			return protocol.JSONObject{}, err
		}
	}
	existing, err := previous.options.Configurations.ByName(ctx, name)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if existing == nil {
		return protocol.JSONObject{}, &Error{404, "MCP server not found: " + name}
	}
	oldScope := existing.String("scope")
	if !editable(oldScope) {
		return protocol.JSONObject{}, &Error{400, fmt.Sprintf("MCP server %q cannot be edited from scope %q", name, oldScope)}
	}
	scope := body.String("scope")
	if scope == "" {
		scope = oldScope
	}
	scope, err = Scope(scope)
	if err != nil {
		return protocol.JSONObject{}, &Error{500, "An unexpected error occurred"}
	}
	next, err := BuildConfig(body.Get("config"))
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if err = s.options.Preflight(ctx, next); err != nil {
		return protocol.JSONObject{}, mutationError(err)
	}
	if err = previous.options.Editor.Remove(ctx, name, oldScope); err == nil {
		err = s.options.Editor.Add(ctx, name, scope, raw(next))
	}
	if err != nil {
		restored, _ := previous.options.Configurations.ByName(ctx, name)
		if restored == nil {
			_ = previous.options.Editor.Add(ctx, name, oldScope, raw(strip(*existing)))
		}
		return protocol.JSONObject{}, mutationError(err)
	}
	updated, err := s.options.Configurations.ByName(ctx, name)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if updated == nil {
		return protocol.JSONObject{}, &Error{500, fmt.Sprintf("Updated MCP server %q could not be reloaded", name)}
	}
	return s.Server(ctx, name, *updated, false)
}
func (s *Service) Remove(ctx context.Context, name, scope string) error {
	scope, err := Scope(scope)
	if err != nil {
		return err
	}
	config, err := s.options.Configurations.ByName(ctx, name)
	if err != nil {
		return err
	}
	if config == nil {
		return &Error{404, "MCP server not found: " + name}
	}
	if !editable(scope) {
		return &Error{400, fmt.Sprintf("MCP server %q cannot be removed from scope %q", name, scope)}
	}
	if err = s.options.Editor.Remove(ctx, name, scope); err != nil {
		return mutationError(err)
	}
	if config.String("type") == "http" || config.String("type") == "sse" {
		if err = s.options.Runtime.ClearOAuth(ctx, name, *config); err != nil {
			return err
		}
	}
	_ = s.options.Runtime.Disconnect(ctx, name, *config)
	return nil
}
func (s *Service) Toggle(ctx context.Context, name, session string) (protocol.JSONObject, protocol.JSONObject, error) {
	config, err := s.existing(ctx, name)
	if err != nil {
		return protocol.JSONObject{}, protocol.JSONObject{}, err
	}
	enabled, err := s.options.Runtime.ExecutionEnabled(ctx, name, config)
	if err != nil {
		return protocol.JSONObject{}, protocol.JSONObject{}, err
	}
	enabled = !enabled
	if err = s.options.Editor.SetEnabled(ctx, name, enabled); err != nil {
		return protocol.JSONObject{}, protocol.JSONObject{}, err
	}
	if !enabled {
		_ = s.options.Runtime.Disconnect(ctx, name, config)
	}
	synced, err := s.options.SyncToggle(ctx, session, name, config, enabled)
	if err != nil {
		return protocol.JSONObject{}, protocol.JSONObject{}, err
	}
	if enabled {
		release := s.options.EnableProbes.Enter(string(raw([]string{s.ProjectKey(), name})))
		defer release()
		current, err := s.options.Runtime.ExecutionEnabled(ctx, name, config)
		if err != nil {
			return protocol.JSONObject{}, protocol.JSONObject{}, err
		}
		if !current {
			value, err := s.Server(ctx, name, config, false)
			return value, synced, err
		}
		_ = s.options.Runtime.Disconnect(ctx, name, config)
	}
	value, err := s.Server(ctx, name, config, enabled)
	return value, synced, err
}
func (s *Service) Status(ctx context.Context, name string) (protocol.JSONObject, error) {
	config, err := s.existing(ctx, name)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	return s.Server(ctx, name, config, true)
}
func (s *Service) Reconnect(ctx context.Context, name string) (protocol.JSONObject, error) {
	config, err := s.existing(ctx, name)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	_ = s.options.Runtime.Disconnect(ctx, name, config)
	enabled, err := s.options.Runtime.ExecutionEnabled(ctx, name, config)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if enabled && s.options.Preflight(ctx, config) != nil {
		return s.Server(ctx, name, config, true)
	}
	first, err := s.options.Runtime.Connect(ctx, name, config)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	_ = s.options.Runtime.CloseProbe(ctx, first)
	value, err := s.Server(ctx, name, config, true)
	if err == nil && first.Status == "failed" && first.Failure != nil {
		value.Set("statusDetail", raw(first.Failure.Error()))
	}
	return value, err
}
func (s *Service) ProjectKey() string { return s.options.Editor.ProjectKey() }
func (s *Service) ProjectPath(name, scope string) (string, error) {
	return s.options.Editor.ProjectPath(name, scope)
}
func (s *Service) ProjectPaths(ctx context.Context) ([]string, error) {
	return s.options.Editor.ProjectPaths(ctx)
}

func (s *Service) all(ctx context.Context) (mcpconfig.Result, error) {
	if s.options.All != nil {
		return s.options.All(ctx)
	}
	return s.options.Configurations.AllConfigurations(ctx, protocol.NewJSONObject())
}
