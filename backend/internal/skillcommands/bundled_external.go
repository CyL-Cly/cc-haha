package skillcommands

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/markdownfrontmatter"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// Authored Source templates, with their interpolation retained for the native
// producer. These are not generated/captured command outputs.
//
//go:embed assets/bundled/*
var externalBundledTemplates embed.FS

type BundledOptions struct {
	Environment        map[string]string
	SettingsJSONSchema func(context.Context) (string, error)
	IsGit              func(context.Context) (bool, error)
	DebugPrompt        func(context.Context, string) (string, error)
}

type BundledCapabilityError struct{ Boundary string }

func (e *BundledCapabilityError) Error() string {
	return "bundled Skill requires unported capability: " + e.Boundary
}

func bundledTemplate(name string) string {
	data, _ := externalBundledTemplates.ReadFile("assets/bundled/" + name + ".md")
	return string(data)
}
func bundledDeclaration(name, description string, allowed []string, disabled bool, fields ...protocol.JSONField) protocol.JSONObject {
	if allowed == nil {
		allowed = []string{}
	}
	m := protocol.NewJSONObject()
	set(m, "type", "prompt")
	set(m, "name", name)
	set(m, "description", description)
	set(m, "hasUserSpecifiedDescription", true)
	set(m, "allowedTools", allowed)
	for _, field := range fields {
		m.Set(field.Name, field.Value)
	}
	set(m, "disableModelInvocation", disabled)
	set(m, "userInvocable", true)
	set(m, "contentLength", 0)
	set(m, "source", "bundled")
	set(m, "loadedFrom", "bundled")
	set(m, "isHidden", false)
	set(m, "progressMessage", "running")
	return m
}
func bundledField(key, value string) protocol.JSONField {
	data, _ := json.Marshal(value)
	return protocol.JSONField{Name: key, Value: data}
}
func argumentString(args *string) string {
	if args == nil {
		return ""
	}
	return *args
}

func NativeExternalBundled(o BundledOptions) ([]Definition, error) {
	if o.Environment == nil {
		return nil, fmt.Errorf("bundled Skill requires actual process environment")
	}
	if o.Environment["USER_TYPE"] == "ant" {
		return nil, &BundledCapabilityError{"internal bundled Skill producers"}
	}
	out := []Definition{}
	add := func(metadata protocol.JSONObject, prompt func(context.Context, *string) (string, error)) error {
		d, e := NewBundled(metadata, "", prompt)
		if e == nil {
			out = append(out, d)
		}
		return e
	}
	e := add(bundledDeclaration("update-config", `Use this skill to configure the Claude Code harness via settings.json. Automated behaviors ("from now on when X", "each time X", "whenever X", "before/after X") require hooks configured in settings.json - the harness executes these, not Claude, so memory/preferences cannot fulfill them. Also use for: permissions ("allow X", "add permission", "move permission to"), env vars ("set X=Y"), hook troubleshooting, or any changes to settings.json/settings.local.json files. Examples: "allow npm commands", "add bq permission to global settings", "move permission to user settings", "set DEBUG=true", "when claude stops show X". For simple settings like theme/model, use Config tool.`, []string{"Read"}, false), func(ctx context.Context, args *string) (string, error) {
		argument := argumentString(args)
		if strings.HasPrefix(argument, "[hooks-only]") {
			req := trimJS(strings.TrimPrefix(argument, "[hooks-only]"))
			prompt := bundledTemplate("hooks") + "\n\n" + bundledTemplate("hook-verification")
			if req != "" {
				prompt += "\n\n## Task\n\n" + req
			}
			return prompt, ctx.Err()
		}
		if o.SettingsJSONSchema == nil {
			return "", &BundledCapabilityError{"actual SettingsSchema input JSON-schema generator"}
		}
		schema, e := o.SettingsJSONSchema(ctx)
		if e != nil {
			return "", e
		}
		prompt := bundledTemplate("update-config")
		prompt = strings.NewReplacer("${SETTINGS_EXAMPLES_DOCS}", bundledTemplate("settings-examples"), "${HOOKS_DOCS}", bundledTemplate("hooks"), "${HOOK_VERIFICATION_FLOW}", bundledTemplate("hook-verification")).Replace(prompt)
		prompt += "\n\n## Full Settings JSON Schema\n\n```json\n" + schema + "\n```"
		if argument != "" {
			prompt += "\n\n## User Request\n\n" + argument
		}
		return prompt, ctx.Err()
	})
	if e != nil {
		return nil, e
	}
	// This fork has no GrowthBook client key; its keybinding release getter
	// returns the false default. No cached value bypasses that Source gate.
	if nativeImagegenAvailable(o.Environment) {
		// Original Bun's .md loader renders HTML before imagegenContent parses
		// frontmatter. Preserve that actual compiled-asset boundary.
		var rendered bytes.Buffer
		e := goldmark.New(goldmark.WithExtensions(extension.GFM), goldmark.WithRendererOptions(html.WithXHTML(), html.WithUnsafe())).Convert([]byte(bundledTemplate("imagegen")), &rendered)
		if e != nil {
			return nil, e
		}
		parsed, e := markdownfrontmatter.Parse(rendered.String())
		if e != nil {
			return nil, e
		}
		description := parsed.Get("description")
		text := "Generate images with the desktop image provider."
		if description != nil && description.Kind == "string" {
			text = description.String()
		}
		if e = add(bundledDeclaration("imagegen", text, []string{"ImageGen", "ImageEdit"}, false), func(ctx context.Context, args *string) (string, error) {
			prompt := strings.TrimLeftFunc(parsed.Content, func(r rune) bool { return trimJS(string(r)) == "" })
			if argument := argumentString(args); argument != "" {
				prompt += "\n\n## User request\n\n" + argument
			}
			return prompt, ctx.Err()
		}); e != nil {
			return nil, e
		}
	}
	if e = add(bundledDeclaration("debug", "Enable debug logging for this session and help diagnose issues", []string{"Read", "Grep", "Glob"}, true, bundledField("argumentHint", "[issue description]")), func(ctx context.Context, args *string) (string, error) {
		if o.DebugPrompt == nil {
			return "", &BundledCapabilityError{"shared debug logging enablement/tail/settings producer"}
		}
		return o.DebugPrompt(ctx, argumentString(args))
	}); e != nil {
		return nil, e
	}
	if e = add(bundledDeclaration("simplify", "Review changed code for reuse, quality, and efficiency, then fix any issues found.", nil, false), func(ctx context.Context, args *string) (string, error) {
		prompt := strings.ReplaceAll(bundledTemplate("simplify"), "${AGENT_TOOL_NAME}", "Agent")
		if argument := argumentString(args); argument != "" {
			prompt += "\n\n## Additional Focus\n\n" + argument
		}
		return prompt, ctx.Err()
	}); e != nil {
		return nil, e
	}
	if e = add(bundledDeclaration("batch", "Research and plan a large-scale change, then execute it in parallel across 5–30 isolated worktree agents that each open a PR.", nil, true, bundledField("argumentHint", "<instruction>"), bundledField("whenToUse", "Use when the user wants to make a sweeping, mechanical change across many files (migrations, refactors, bulk renames) that can be decomposed into independent parallel units.")), func(ctx context.Context, args *string) (string, error) {
		instruction := trimJS(argumentString(args))
		if instruction == "" {
			return bundledTemplate("batch-missing"), ctx.Err()
		}
		if o.IsGit == nil {
			return "", &BundledCapabilityError{"actual memoized getIsGit/current cwd"}
		}
		git, e := o.IsGit(ctx)
		if e != nil {
			return "", e
		}
		if !git {
			return bundledTemplate("batch-not-git"), ctx.Err()
		}
		prompt := bundledTemplate("batch")
		worker := strings.ReplaceAll(bundledTemplate("batch-worker"), "${SKILL_TOOL_NAME}", "Skill")
		prompt = strings.NewReplacer("${instruction}", instruction, "${ENTER_PLAN_MODE_TOOL_NAME}", "EnterPlanMode", "${EXIT_PLAN_MODE_TOOL_NAME}", "ExitPlanMode", "${AGENT_TOOL_NAME}", "Agent", "${ASK_USER_QUESTION_TOOL_NAME}", "AskUserQuestion", "${MIN_AGENTS}", "5", "${MAX_AGENTS}", "30", "${WORKER_INSTRUCTIONS}", worker).Replace(prompt)
		return prompt, ctx.Err()
	}); e != nil {
		return nil, e
	}
	return out, nil
}

func nativeImagegenAvailable(env map[string]string) bool {
	kind, id, model := trimJS(env["CC_HAHA_IMAGE_PROVIDER_KIND"]), trimJS(env["CC_HAHA_IMAGE_PROVIDER_ID"]), trimJS(env["CC_HAHA_IMAGE_MODEL"])
	if id == "" || model == "" || (kind != "openai_oauth" && kind != "grok_oauth" && kind != "openai_images") {
		return false
	}
	return kind != "openai_images" || trimJS(env["CC_HAHA_IMAGE_BASE_URL"]) != "" && trimJS(env["CC_HAHA_IMAGE_API_KEY"]) != ""
}
