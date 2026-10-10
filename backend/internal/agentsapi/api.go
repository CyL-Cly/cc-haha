package agentsapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"unicode/utf8"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentbuiltinmetadata"
	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

func (s *Service) Handle(ctx context.Context, method string, requestURL *url.URL, parts []string, body []byte) (int, any, error) {
	name := ""
	if len(parts) > 2 && parts[2] != "" {
		var err error
		name, err = url.PathUnescape(parts[2])
		if err != nil || !utf8.ValidString(name) {
			return 0, nil, bad("Invalid encoded agent name")
		}
	}
	if method == "POST" && name == "reload" {
		sessionID := trim(requestURL.Query().Get("sessionId"))
		if sessionID == "" {
			return 0, nil, bad("Missing required \"sessionId\" query parameter")
		}
		summary, err := s.options.Reload(ctx, sessionID)
		if err != nil {
			return 0, nil, err
		}
		return 200, map[string]any{"ok": true, "session": summary}, nil
	}
	if method == "GET" && name == "" {
		cwd := s.cwd(requestURL.Query().Get("cwd"))
		catalog, err := s.options.Load(ctx, cwd)
		if err != nil {
			return 0, nil, err
		}
		tools, err := s.options.AvailableTools(ctx, cwd)
		if err != nil {
			return 0, nil, err
		}
		comparer := collate.New(language.English)
		sortNames(tools, comparer)
		active, all := []protocol.JSONObject{}, []protocol.JSONObject{}
		for _, definition := range catalog.Active {
			value, err := s.serialize(ctx, definition, true, cwd)
			if err != nil {
				return 0, nil, err
			}
			active = append(active, value)
		}
		for _, definition := range agentcatalog.Resolve(catalog.All, catalog.Active) {
			value, err := s.serialize(ctx, definition, definition.OverriddenBy == "", cwd)
			if err != nil {
				return 0, nil, err
			}
			if definition.OverriddenBy != "" {
				value.Set("overriddenBy", quote(definition.OverriddenBy))
			}
			all = append(all, value)
		}
		return 200, map[string]any{"availableTools": tools, "activeAgents": active, "allAgents": all}, nil
	}
	if method == "GET" && name != "" {
		if err := ValidateName(name); err != nil {
			return 0, nil, err
		}
		cwd := s.cwd(requestURL.Query().Get("cwd"))
		catalog, err := s.options.Load(ctx, cwd)
		if err != nil {
			return 0, nil, err
		}
		for _, definition := range catalog.Active {
			if definition.Name == name {
				value, err := s.serialize(ctx, definition, true, cwd)
				return 200, map[string]any{"agent": value}, err
			}
		}
		return 0, nil, fail(404, "NOT_FOUND", "Agent not found: "+name)
	}
	if method == "POST" && name == "" {
		mutation, err := ParseMutation(body, false, "")
		if err != nil {
			return 0, nil, err
		}
		name = mutation.Fields.String("name")
		if err = s.invalidate(ctx); err != nil {
			return 0, nil, err
		}
		catalog, err := s.options.Load(ctx, s.cwd(mutation.Context.CWD))
		if err != nil {
			return 0, nil, err
		}
		source := "userSettings"
		if mutation.Context.Scope == "project" {
			source = "projectSettings"
		}
		for _, definition := range catalog.All {
			if definition.Name == name && definition.Source == source {
				return 0, nil, fail(409, "CONFLICT", "Agent already exists in "+mutation.Context.Scope+" scope: "+name)
			}
		}
		written, err := s.CreateFile(ctx, mutation.Fields, mutation.Context)
		if err != nil {
			return 0, nil, err
		}
		if err = s.invalidate(ctx); err != nil {
			return 0, nil, err
		}
		visible, err := s.loadMutated(ctx, name, mutation.Context, written)
		return 201, map[string]any{"agent": visible}, err
	}
	if len(parts) > 3 && parts[3] == "override" && name != "" {
		if method == "PUT" {
			parsed, err := parseBody(body)
			if err != nil {
				return 0, nil, err
			}
			if err = allowed(parsed, overrideFields); err != nil {
				return 0, nil, err
			}
			cwd, _ := jsString(parsed.Get("cwd"))
			cwd = s.cwd(cwd)
			if err = s.assertBuiltin(ctx, name, cwd); err != nil {
				return 0, nil, err
			}
			patch, _, err := ParseOverride(body)
			if err != nil {
				return 0, nil, err
			}
			fields := storage.Object{}
			for _, property := range patch.OwnProperties() {
				key, _ := jsString(property.NameJSON)
				fields[key] = property.Value
			}
			if err = s.options.UpdateOverride(ctx, name, fields); err != nil {
				return 0, nil, err
			}
			if err = s.invalidate(ctx); err != nil {
				return 0, nil, err
			}
			visible, err := s.loadBuiltin(ctx, name, cwd)
			return 200, map[string]any{"agent": visible}, err
		}
		if method == "DELETE" {
			cwd := s.cwd(requestURL.Query().Get("cwd"))
			if err := s.assertBuiltin(ctx, name, cwd); err != nil {
				return 0, nil, err
			}
			if err := s.options.UpdateOverride(ctx, name, nil); err != nil {
				return 0, nil, err
			}
			if err := s.invalidate(ctx); err != nil {
				return 0, nil, err
			}
			visible, err := s.loadBuiltin(ctx, name, cwd)
			return 200, map[string]any{"agent": visible}, err
		}
		return 0, nil, fail(405, "METHOD_NOT_ALLOWED", "Method "+method+" not allowed on /api/agents/"+name+"/override")
	}
	if method == "PUT" && name != "" {
		mutation, err := ParseMutation(body, true, name)
		if err != nil {
			return 0, nil, err
		}
		if err = s.assertMutable(ctx, name, mutation.Context); err != nil {
			return 0, nil, err
		}
		written, err := s.UpdateFile(ctx, name, mutation.Fields, mutation.Context)
		if err != nil {
			return 0, nil, err
		}
		if err = s.invalidate(ctx); err != nil {
			return 0, nil, err
		}
		visible, err := s.loadMutated(ctx, name, mutation.Context, written)
		return 200, map[string]any{"agent": visible}, err
	}
	if method == "DELETE" && name != "" {
		scopeName, err := scope(quote(requestURL.Query().Get("scope")))
		if err != nil {
			return 0, nil, err
		}
		mutation := MutationContext{scopeName, requestURL.Query().Get("cwd"), requestURL.Query().Get("target")}
		if err = s.assertMutable(ctx, name, mutation); err != nil {
			return 0, nil, err
		}
		if err = s.DeleteFile(ctx, name, mutation); err != nil {
			return 0, nil, err
		}
		if err = s.invalidate(ctx); err != nil {
			return 0, nil, err
		}
		return 200, map[string]any{"ok": true}, nil
	}
	suffix := ""
	if name != "" {
		suffix = "/" + name
	}
	return 0, nil, fail(405, "METHOD_NOT_ALLOWED", "Method "+method+" not allowed on /api/agents"+suffix)
}
func (s *Service) assertBuiltin(ctx context.Context, name, cwd string) error {
	locked, err := s.options.Locked(ctx)
	if err != nil {
		return err
	}
	if locked {
		return fail(403, "AGENT_CUSTOMIZATION_LOCKED", "Agent customization is restricted to plugins by managed settings")
	}
	catalog, err := s.options.Load(ctx, cwd)
	if err != nil {
		return err
	}
	for _, definition := range catalog.All {
		if definition.Name != name {
			continue
		}
		if definition.Source != "built-in" {
			return fail(403, "NOT_A_BUILT_IN_AGENT", "Agent is not built-in: "+name)
		}
		return nil
	}
	return fail(404, "NOT_FOUND", "Agent not found: "+name)
}
func (s *Service) assertMutable(ctx context.Context, name string, mutation MutationContext) error {
	editable, err := s.GetFile(ctx, name, mutation)
	if err != nil {
		return err
	}
	if editable != nil {
		return nil
	}
	catalog, err := s.options.Load(ctx, s.cwd(mutation.CWD))
	if err != nil {
		return err
	}
	for _, definition := range catalog.Active {
		if definition.Name == name && definition.Source != "userSettings" && definition.Source != "projectSettings" {
			return fail(403, "READ_ONLY_AGENT", "Agent is read-only: "+name)
		}
	}
	return fail(404, "NOT_FOUND", "Agent not found in "+mutation.Scope+" scope: "+name)
}
func normalized(path string) string {
	real, err := filepath.EvalSymlinks(path)
	if err == nil {
		return real
	}
	absolute, _ := filepath.Abs(path)
	return absolute
}
func sameDefinition(a, b agentcatalog.Definition) bool {
	return a.Name == b.Name && a.Source == b.Source && a.FilePath == b.FilePath
}
func (s *Service) loadMutated(ctx context.Context, name string, mutation MutationContext, written MutationResult) (protocol.JSONObject, error) {
	cwd := s.cwd(mutation.CWD)
	catalog, err := s.options.Load(ctx, cwd)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	source := "userSettings"
	if mutation.Scope == "project" {
		source = "projectSettings"
	}
	for _, definition := range catalog.All {
		base := definition.Fields.String("baseDir")
		if definition.Name == name && definition.Source == source && base != "" && definition.FilePath != "" && normalized(base) == normalized(written.AgentsDir) && normalized(definition.FilePath) == normalized(written.Target) {
			active := false
			for _, candidate := range catalog.Active {
				if sameDefinition(candidate, definition) {
					active = true
					break
				}
			}
			return s.serialize(ctx, definition, active, cwd)
		}
	}
	return protocol.JSONObject{}, fail(500, "INTERNAL_ERROR", "Agent was written but is not visible to the shared loader: "+name)
}
func (s *Service) loadBuiltin(ctx context.Context, name, cwd string) (protocol.JSONObject, error) {
	catalog, err := s.options.Load(ctx, cwd)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	for _, definition := range catalog.All {
		if definition.Name == name && definition.Source == "built-in" {
			active := false
			for _, candidate := range catalog.Active {
				if sameDefinition(candidate, definition) {
					active = true
					break
				}
			}
			return s.serialize(ctx, definition, active, cwd)
		}
	}
	return protocol.JSONObject{}, fail(500, "INTERNAL_ERROR", "Built-in agent disappeared after writing its override: "+name)
}
func (s *Service) serialize(ctx context.Context, definition agentcatalog.Definition, active bool, cwd string) (protocol.JSONObject, error) {
	editable := false
	target := ""
	if (definition.Source == "userSettings" || definition.Source == "projectSettings") && definition.Fields.String("baseDir") != "" {
		scopeName := "user"
		if definition.Source == "projectSettings" {
			scopeName = "project"
		}
		file, err := s.GetFile(ctx, definition.Name, MutationContext{scopeName, cwd, definition.FilePath})
		if err != nil {
			var api *APIError
			if !errors.As(err, &api) || api.StatusCode >= 500 {
				return protocol.JSONObject{}, err
			}
		} else if file != nil {
			editable = true
			target = file.Target
		}
	}
	out := protocol.NewJSONObject()
	out.Set("agentType", quote(definition.Name))
	out.Set("description", quote(definition.Description))
	for _, name := range []string{"model", "effort", "color", "baseDir"} {
		if raw := definition.Fields.Get(name); raw != nil {
			out.Set(name, raw)
		}
	}
	model := definition.Fields.Get("model")
	if text, _ := jsString(model); text == "" {
		model = quote("inherit")
	}
	out.Set("modelDisplay", model)
	tools := definition.Fields.Get("tools")
	if editable && definition.Fields.Get("rawTools") != nil {
		tools = definition.Fields.Get("rawTools")
	}
	if tools != nil {
		out.Set("tools", tools)
	}
	if definition.Source == "userSettings" || definition.Source == "projectSettings" {
		if raw := definition.Fields.Get("rawSystemPrompt"); raw != nil {
			out.Set("systemPrompt", raw)
		} else if definition.PromptArity == 0 {
			out.Set("systemPrompt", quote(definition.SystemPrompt))
		}
	} else if definition.PromptArity == 0 {
		out.Set("systemPrompt", quote(definition.SystemPrompt))
	}
	out.Set("source", quote(definition.Source))
	if target != "" {
		out.Set("target", quote(target))
	}
	value, _ := json.Marshal(active)
	out.Set("isActive", value)
	value, _ = json.Marshal(editable)
	out.Set("editable", value)
	if definition.Source == "built-in" {
		out.Set("overridable", []byte("true"))
		defaults := protocol.NewJSONObject()
		for _, stock := range agentbuiltinmetadata.Stock() {
			if stock.Fields.String("agentType") == definition.Name {
				for _, key := range []string{"model", "effort"} {
					if raw := stock.Fields.Get(key); raw != nil {
						defaults.Set(key, raw)
					}
				}
				break
			}
		}
		value, _ = defaults.MarshalJSON()
		out.Set("defaults", value)
		override := protocol.NewJSONObject()
		sourceName := ""
		for _, field := range []struct{ Name, Source string }{{"model", definition.ModelSource}, {"effort", definition.EffortSource}} {
			if field.Source != "" {
				if raw := definition.Fields.Get(field.Name); raw != nil {
					override.Set(field.Name, raw)
					if sourceRank(field.Source) > sourceRank(sourceName) {
						sourceName = field.Source
					}
				}
			}
		}
		if sourceName != "" {
			override.Set("source", quote(sourceName))
			value, _ = override.MarshalJSON()
			out.Set("override", value)
		}
	}
	return out, nil
}
func sourceRank(source string) int {
	for i, name := range []string{"userSettings", "projectSettings", "localSettings", "flagSettings", "policySettings"} {
		if name == source {
			return i + 1
		}
	}
	return 0
}
