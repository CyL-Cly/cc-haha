// Package skillcommands supplies shared native prompt commands. Definitions are
// immutable snapshots; invocation dependencies are bound by the owning Harness.
package skillcommands

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/markdownfrontmatter"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type ModelResolver func(context.Context, string) (string, error)
type ShellRequest struct {
	Text, SlashCommand, Shell string
	AllowedTools              []string
}

// ExecuteShell must use the current turn's actual permission context/tool and
// result persistence. Calling an unguarded OS shell here would change behavior.
type Invocation struct {
	NativeScope         *cooperative.Scope
	SessionID, Platform string
	ExecuteShell        func(context.Context, ShellRequest) (string, error)
	PluginDataDirectory func(context.Context, string) (string, error)
	PluginOptions       func(context.Context, string) (protocol.JSONObject, error)
}
type Definition struct {
	nativePrompt                                                     func(context.Context, *string, Invocation) (string, error)
	bundledPrompt                                                    func(context.Context, *string) (string, error)
	metadata                                                         protocol.JSONObject
	body, name, baseDir, loadedFrom, pluginPath, pluginSource, shell string
	arguments, allowedTools                                          []string
	pluginUserConfig                                                 protocol.JSONObject
	pluginSkillMode                                                  bool
}

func (d Definition) Metadata() protocol.JSONObject { return d.metadata.Clone() }
func (d Definition) Name() string                  { return d.name }
func (d Definition) Content() string               { return d.body }
func (d Definition) UserFacingName() string        { return getString(d.metadata, "userFacingName") }
func (d Definition) Paths() []string {
	var out []string
	_ = json.Unmarshal(d.metadata.Get("paths"), &out)
	return out
}
func (d Definition) Prompt(ctx context.Context, args *string, inv Invocation) (string, error) {
	if d.nativePrompt != nil {
		return d.nativePrompt(ctx, args, inv)
	}
	if d.bundledPrompt != nil {
		return d.bundledPrompt(ctx, args)
	}
	text := d.body
	if d.baseDir != "" && (d.pluginPath == "" || d.pluginSkillMode) {
		text = "Base directory for this skill: " + d.baseDir + "\n\n" + text
	}
	var err error
	text, err = SubstituteArguments(text, args, d.arguments)
	if err != nil {
		return "", err
	}
	normalize := func(s string) string {
		if inv.Platform == "win32" {
			return strings.ReplaceAll(s, "\\", "/")
		}
		return s
	}
	if d.pluginPath != "" {
		text = strings.ReplaceAll(text, "${CLAUDE_PLUGIN_ROOT}", normalize(d.pluginPath))
		if strings.Contains(text, "${CLAUDE_PLUGIN_DATA}") && d.pluginSource != "" {
			if inv.PluginDataDirectory == nil {
				return "", errors.New("plugin prompt requires actual data-directory producer")
			}
			path, e := inv.PluginDataDirectory(ctx, d.pluginSource)
			if e != nil {
				return "", e
			}
			text = strings.ReplaceAll(text, "${CLAUDE_PLUGIN_DATA}", normalize(path))
		}
		if d.pluginUserConfig.IsObject() {
			if inv.PluginOptions == nil {
				return "", errors.New("plugin prompt requires actual saved option producer")
			}
			values, e := inv.PluginOptions(ctx, d.pluginSource)
			if e != nil {
				return "", e
			}
			text = substituteUserConfig(text, values, d.pluginUserConfig)
		}
	}
	if d.baseDir != "" && (d.pluginPath == "" || d.pluginSkillMode) {
		text = strings.ReplaceAll(text, "${CLAUDE_SKILL_DIR}", normalize(d.baseDir))
	}
	text = strings.ReplaceAll(text, "${CLAUDE_SESSION_ID}", inv.SessionID)
	if d.loadedFrom == "mcp" {
		return text, nil
	}
	if inv.ExecuteShell == nil {
		return "", errors.New("skill prompt requires actual permission-aware shell producer")
	}
	return inv.ExecuteShell(ctx, ShellRequest{text, "/" + d.name, d.shell, append([]string{}, d.allowedTools...)})
}

func Parse(ctx context.Context, markdown, name, source, baseDir, loadedFrom string, model ModelResolver) (Definition, error) {
	p, err := markdownfrontmatter.Parse(markdown)
	if err != nil {
		return Definition{}, err
	}
	return create(ctx, p, name, source, baseDir, loadedFrom, "Skill", model)
}
func create(ctx context.Context, p markdownfrontmatter.Parsed, name, source, baseDir, loadedFrom, label string, model ModelResolver) (Definition, error) {
	m := protocol.NewJSONObject()
	set(m, "type", "prompt")
	set(m, "name", name)
	description, specified := description(p.Get("description"), p.Content, label)
	set(m, "description", description)
	set(m, "hasUserSpecifiedDescription", specified)
	tools := parseTools(p.Frontmatter.Get("allowed-tools"))
	set(m, "allowedTools", tools)
	display := optionalString(p.Get("name"))
	if display == "" {
		display = name
	}
	set(m, "userFacingName", display)
	for _, key := range []string{"argument-hint", "when_to_use", "version", "agent"} {
		value := p.Get(key)
		if value == nil {
			continue
		}
		dest := map[string]string{"argument-hint": "argumentHint", "when_to_use": "whenToUse"}[key]
		if dest == "" {
			dest = key
		}
		if key == "argument-hint" {
			if value.Kind != "null" {
				set(m, dest, value.String())
			}
		} else {
			raw, e := value.JSON()
			if e != nil {
				return Definition{}, e
			}
			m.Set(dest, raw)
		}
	}
	args := parseArgumentNames(p.Frontmatter.Get("arguments"))
	if len(args) > 0 {
		set(m, "argNames", args)
	}
	if v := p.Get("model"); v != nil && v.Truthy() && !(v.Kind == "string" && v.String() == "inherit") {
		if v.Kind != "string" {
			return Definition{}, errors.New("modelInput.trim is not a function")
		}
		if model == nil {
			return Definition{}, errors.New("skill model requires actual native model resolver")
		}
		value, e := model(ctx, v.String())
		if e != nil {
			return Definition{}, e
		}
		set(m, "model", value)
	}
	disabled := boolean(p.Get("disable-model-invocation"))
	user := p.Get("user-invocable") == nil || boolean(p.Get("user-invocable"))
	set(m, "disableModelInvocation", disabled)
	set(m, "userInvocable", user)
	set(m, "isHidden", !user)
	if p.Get("context") != nil && p.Get("context").Kind == "string" && p.Get("context").String() == "fork" {
		set(m, "context", "fork")
	}
	if effort := parseEffort(p.Get("effort")); effort != nil {
		m.Set("effort", effort)
	}
	shell := strings.ToLower(trimJS(optionalString(p.Get("shell"))))
	if shell != "bash" && shell != "powershell" {
		shell = ""
	}
	if hooks := p.Get("hooks"); hooks != nil && hooks.Truthy() {
		schemas, e := pluginschema.Declarations()
		if e != nil {
			return Definition{}, e
		}
		raw, e := hooks.JSON()
		if e != nil {
			return Definition{}, e
		}
		value, issues := pluginschema.Validate(schemas.Hooks, raw)
		if len(issues) == 0 {
			m.Set("hooks", value)
		}
	}
	if loadedFrom != "commands_DEPRECATED" {
		if paths := parsePaths(p.Frontmatter.Get("paths")); len(paths) > 0 {
			set(m, "paths", paths)
		}
	}
	set(m, "contentLength", len(utf16.Encode([]rune(p.Content))))
	set(m, "progressMessage", "running")
	set(m, "source", source)
	if loadedFrom != "" {
		set(m, "loadedFrom", loadedFrom)
	}
	if baseDir != "" {
		set(m, "skillRoot", baseDir)
	}
	return Definition{metadata: m, body: p.Content, name: name, baseDir: baseDir, loadedFrom: loadedFrom, shell: shell, arguments: args, allowedTools: tools}, nil
}
func set(o protocol.JSONObject, key string, v any) { raw, _ := json.Marshal(v); o.Set(key, raw) }
func getString(o protocol.JSONObject, key string) string {
	var out string
	_ = json.Unmarshal(o.Get(key), &out)
	return out
}
func boolean(v *markdownfrontmatter.Value) bool {
	return v != nil && (v.Kind == "boolean" || v.Kind == "string") && v.String() == "true"
}
func optionalString(v *markdownfrontmatter.Value) string {
	if v == nil || v.Kind == "null" {
		return ""
	}
	return v.String()
}
func trimJS(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return r >= 9 && r <= 13 || r == ' ' || r == '\u00a0' || r == '\u1680' || r >= '\u2000' && r <= '\u200a' || r == '\u2028' || r == '\u2029' || r == '\u202f' || r == '\u205f' || r == '\u3000' || r == '\ufeff'
	})
}
func pluginBase(path string, skill bool) string {
	if skill {
		return filepath.Dir(path)
	}
	return ""
}
