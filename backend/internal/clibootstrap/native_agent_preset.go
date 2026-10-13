package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcptools"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// Agent preset business is independent of Team's main-session identity, tasks
// and hooks. The same actual parent graph supplies project command discovery;
// the actual child graph executes prompts under its worker cwd.
type nativeAgentPreset struct {
	parent     *MainPrint
	definition agentcatalog.Definition
	inherited  []mcpruntime.Connection
	resources  *mcptools.AgentResourceSnapshot
}

func (m *MainPrint) nativeAgentPreset(scope *cooperative.Scope, definition agentcatalog.Definition) (*nativeAgentPreset, error) {
	if !scope.BelongsTo(m.Owner) || m.mcpRuntime == nil || m.mcpTools == nil {
		return nil, cooperative.ErrRestricted
	}
	peers, e := m.mcpTools.AgentConnections(scope)
	if e != nil {
		return nil, e
	}
	resources, e := m.mcpTools.AgentResources(scope)
	if e != nil {
		return nil, e
	}
	return &nativeAgentPreset{parent: m, definition: definition, inherited: peers, resources: resources}, nil
}
func (p *nativeAgentPreset) skillDefinitions(ctx context.Context, child *MainPrint, scope *cooperative.Scope) ([]skillcommands.Definition, error) {
	return nativeAgentOwnerCall(child, scope, p.parent, func(parentScope *cooperative.Scope) ([]skillcommands.Definition, error) {
		root := p.parent.state.Snapshot().ProjectRoot
		local, e := p.parent.skillCommands.Local(ctx, root)
		if e != nil {
			return nil, e
		}
		plugins, e := p.parent.nativeSettings.LoadPlugins(ctx, "", false)
		if e != nil {
			return nil, e
		}
		all := append([]skillcommands.Definition{}, local...)
		workflows, e := p.parent.nativeWorkflowCommandDefinitions(ctx, parentScope)
		if e != nil {
			return nil, e
		}
		all = append(all, workflows...)
		all = append(all, p.parent.skillCommands.PluginCommands(ctx, plugins.Enabled)...)
		all = append(all, p.parent.skillCommands.PluginSkills(ctx, plugins.Enabled)...)
		bundled, e := p.parent.nativeBundledSkillDefinitions()
		if e != nil {
			return nil, e
		}
		if computerUse, enabled, e := p.parent.nativeComputerUseSkill(); e != nil {
			return nil, e
		} else if enabled {
			bundled = append(bundled, computerUse)
		}
		all = append(bundled, all...)
		seen := map[string]bool{}
		for _, d := range all {
			seen[d.Name()] = true
		}
		for _, d := range p.parent.skillCommands.Dynamic() {
			if !seen[d.Name()] {
				all = append(all, d)
				seen[d.Name()] = true
			}
		}
		selected := []skillcommands.Definition{}
		for _, d := range all {
			meta := d.Metadata()
			if meta.String("type") != "prompt" || string(meta.Get("disableModelInvocation")) == "true" || meta.String("source") == "builtin" {
				continue
			}
			loaded := meta.String("loadedFrom")
			if loaded == "bundled" || loaded == "skills" || loaded == "commands_DEPRECATED" || string(meta.Get("hasUserSpecifiedDescription")) == "true" || planRawTruthy(meta.Get("whenToUse")) {
				selected = append(selected, d)
			}
		}
		return selected, nil
	})
}
func resolveNativeAgentSkill(name, agentType string, all []skillcommands.Definition) (skillcommands.Definition, bool) {
	matches := func(d skillcommands.Definition, candidate string) bool {
		if d.Name() == candidate || d.UserFacingName() == candidate {
			return true
		}
		var aliases []string
		_ = json.Unmarshal(d.Metadata().Get("aliases"), &aliases)
		for _, alias := range aliases {
			if alias == candidate {
				return true
			}
		}
		return false
	}
	for _, candidate := range []string{name, strings.Split(agentType, ":")[0] + ":" + name} {
		for _, d := range all {
			if matches(d, candidate) {
				return d, true
			}
		}
	}
	for _, d := range all {
		if strings.HasSuffix(d.Name(), ":"+name) {
			return d, true
		}
	}
	return skillcommands.Definition{}, false
}
func (p *nativeAgentPreset) skills(ctx context.Context, child *MainPrint, scope *cooperative.Scope) ([]protocol.JSONObject, error) {
	var names []string
	if e := json.Unmarshal(p.definition.Fields.Get("skills"), &names); e != nil && p.definition.Fields.Get("skills") != nil {
		return nil, e
	}
	if len(names) == 0 {
		return []protocol.JSONObject{}, nil
	}
	definitions, e := p.skillDefinitions(ctx, child, scope)
	if e != nil {
		return nil, e
	}
	return p.loadSkills(ctx, child, scope, names, definitions)
}

func (p *nativeAgentPreset) loadSkills(ctx context.Context, child *MainPrint, scope *cooperative.Scope, names []string, definitions []skillcommands.Definition) ([]protocol.JSONObject, error) {
	type pendingSkill struct {
		name       string
		definition skillcommands.Definition
	}
	valid := []pendingSkill{}
	for _, name := range names {
		d, found := resolveNativeAgentSkill(name, p.definition.Name, definitions)
		if !found {
			if child.options.Error != nil {
				_, _ = fmt.Fprintf(child.options.Error, "[Agent: %s] Warning: Skill '%s' specified in frontmatter was not found\n", p.definition.Name, name)
			}
			continue
		}
		valid = append(valid, pendingSkill{name, d})
	}
	if len(valid) == 0 {
		return []protocol.JSONObject{}, nil
	}
	loaded := make([]string, len(valid))
	all, e := ownerasync.NewOwnerFuture[[]string](child.Owner)
	if e != nil {
		return nil, e
	}
	remaining, rejected := len(valid), false
	for i, item := range valid {
		_, e = child.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(current *cooperative.Scope) error {
			text, failure := p.skillPrompt(ctx, child, current, item.definition)
			remaining--
			if failure != nil && !rejected {
				rejected = true
				return all.Reject(current, failure)
			}
			loaded[i] = text
			if remaining == 0 && !rejected {
				return all.Resolve(current, loaded)
			}
			return nil
		})
		if e != nil {
			return nil, e
		}
	}
	loaded, e = all.Await(scope)
	if e != nil {
		return nil, e
	}
	// Promise.all preserves frontmatter order and allocates metadata messages
	// only after every content producer has completed.
	out := []protocol.JSONObject{}
	for i, item := range valid {
		name, text := item.name, loaded[i]
		metadata := "<command-message>" + name + "</command-message>\n<command-name>" + name + "</command-name>\n<skill-format>true</skill-format>"
		blocks := []protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("text")}, protocol.JSONField{Name: "text", Value: quoteMain(metadata)}), protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("text")}, protocol.JSONField{Name: "text", Value: quoteMain(text)})}
		out = append(out, messages.OrderedUser(child.factory.CreateUser(storage.Object{"content": rawMain(blocks), "isMeta": json.RawMessage("true")})))
	}
	return out, nil
}

func (p *nativeAgentPreset) skillPrompt(ctx context.Context, child *MainPrint, scope *cooperative.Scope, d skillcommands.Definition) (string, error) {
	return nativeAgentOwnerCall(child, scope, p.parent, func(parentScope *cooperative.Scope) (string, error) {
		inv, e := p.parent.nativeSkillInvocation(parentScope)
		if e != nil {
			return "", e
		}
		inv.ExecuteShell = func(ctx context.Context, request skillcommands.ShellRequest) (string, error) {
			if err := p.parent.validateNativePromptShell(parentScope, request); err != nil {
				return "", err
			}
			if !embeddedPromptBlock.MatchString(request.Text) && !embeddedPromptInline.MatchString(request.Text) {
				return request.Text, context.Cause(ctx)
			}
			permission, e := p.parent.nativeToolPermissionContext(parentScope)
			if e != nil {
				return "", e
			}
			return nativeAgentOwnerCall(p.parent, parentScope, child, func(childScope *cooperative.Scope) (string, error) {
				return child.nativeSkillShellWithPermission(ctx, childScope, request, &permission)
			})
		}
		args := ""
		return d.Prompt(ctx, &args, inv)
	})
}
func (p *nativeAgentPreset) connectMCP(ctx context.Context, child *MainPrint, scope *cooperative.Scope) ([]mcpruntime.Connection, []mcpruntime.Connection, error) {
	var specs []json.RawMessage
	if e := json.Unmarshal(p.definition.Fields.Get("mcpServers"), &specs); e != nil && p.definition.Fields.Get("mcpServers") != nil {
		return nil, nil, e
	}
	if len(specs) == 0 {
		return []mcpruntime.Connection{}, []mcpruntime.Connection{}, nil
	}
	policy, e := child.options.Native.ResolvePolicy()
	if e != nil {
		return nil, nil, e
	}
	restricted := false
	if policy != nil {
		restricted = string(policy.Get("strictPluginOnlyCustomization")) == "true"
		var surfaces []string
		_ = json.Unmarshal(policy.Get("strictPluginOnlyCustomization"), &surfaces)
		for _, surface := range surfaces {
			restricted = restricted || surface == "mcp"
		}
	}
	trusted := false
	for _, source := range []string{"plugin", "policySettings", "built-in", "builtin", "bundled"} {
		trusted = trusted || p.definition.Source == source
	}
	if restricted && !trusted {
		return []mcpruntime.Connection{}, []mcpruntime.Connection{}, nil
	}
	type connected struct{ all, inline []mcpruntime.Connection }
	future, e := nativeSkillWork(child, scope, func() (connected, error) {
		result := connected{all: []mcpruntime.Connection{}, inline: []mcpruntime.Connection{}}
		complete := false
		defer func() {
			if !complete {
				for _, peer := range result.inline {
					_ = child.mcpRuntime.CloseConnection(context.Background(), peer)
				}
			}
		}()
		for _, raw := range specs {
			var name string
			config := protocol.NewJSONObject()
			inline := false
			if json.Unmarshal(raw, &name) == nil {
				value, e := child.mcpConfigurations.ByName(ctx, name)
				if e != nil {
					return result, e
				}
				if value == nil {
					continue
				}
				config = value.Clone()
			} else {
				var spec protocol.JSONObject
				if spec.UnmarshalJSON(raw) != nil || len(spec.OwnProperties()) != 1 {
					continue
				}
				field := spec.OwnProperties()[0]
				_ = json.Unmarshal(field.NameJSON, &name)
				if e := config.UnmarshalJSON(field.Value); e != nil {
					return result, e
				}
				config.Set("scope", quoteMain("dynamic"))
				inline = true
			}
			peer, e := child.mcpRuntime.Connect(ctx, name, config)
			if e != nil {
				return result, e
			}
			result.all = append(result.all, peer)
			if inline {
				result.inline = append(result.inline, peer)
			}
		}
		complete = true
		return result, nil
	})
	if e != nil {
		return nil, nil, e
	}
	result, e := future.Await(scope)
	return result.all, result.inline, e
}

func (p *nativeAgentPreset) registerHooks(child *MainPrint, scope *cooperative.Scope) error {
	if !scope.BelongsTo(child.Owner) {
		return cooperative.ErrRestricted
	}
	raw := p.definition.Fields.Get("hooks")
	if raw == nil || string(raw) == "null" {
		return nil
	}
	policy, e := child.options.Native.ResolvePolicy()
	if e != nil {
		return e
	}
	restricted := false
	if policy != nil {
		restricted = string(policy.Get("strictPluginOnlyCustomization")) == "true"
		var surfaces []string
		_ = json.Unmarshal(policy.Get("strictPluginOnlyCustomization"), &surfaces)
		for _, surface := range surfaces {
			restricted = restricted || surface == "hooks"
		}
	}
	trusted := false
	for _, source := range []string{"plugin", "policySettings", "built-in", "builtin", "bundled"} {
		trusted = trusted || p.definition.Source == source
	}
	if restricted && !trusted {
		return nil
	}
	var hooks protocol.JSONObject
	if e = hooks.UnmarshalJSON(raw); e != nil {
		return e
	}
	return child.hook.RegisterNativeAgentHooks(child.options.AgentID, hooks)
}
