package connectors

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	weburl "github.com/nlnwa/whatwg-url/url"
)

const managedMarketplace = "haha-connectors"

func managedPluginName(def protocol.JSONObject) (string, error) {
	name := "office-" + def.String("id")
	if !recipeID.MatchString(def.String("id")) || def.String("pluginId") != name+"@"+managedMarketplace {
		return "", errors.New("Invalid managed connector plugin identity")
	}
	return name, nil
}

func (c Catalog) skill(id, version string) *SkillRecipe {
	for _, r := range c.Skills {
		if r.ID == id && (version == "" || r.Version == version) {
			return &r
		}
	}
	return nil
}
func (c Catalog) remote(id string) *RemoteRecipe {
	for _, r := range c.Remote {
		if r.ID == id {
			return &r
		}
	}
	return nil
}

func validateRemoteRecipe(r RemoteRecipe) error {
	if !recipeID.MatchString(r.ID) || r.PluginID != "office-"+r.ID+"@"+managedMarketplace {
		return errors.New("Invalid managed remote connector identity")
	}
	u, err := weburl.Parse(r.Endpoint)
	if err != nil {
		return fmt.Errorf("%q cannot be parsed as a URL.", r.Endpoint)
	}
	if u.Scheme() != "https" || u.Username() != "" || u.Password() != "" || u.Hash() != "" || strings.Contains(r.Endpoint, "${") {
		return errors.New("Remote connector requires an official HTTPS endpoint")
	}
	if r.Transport != "http" && r.Transport != "sse" {
		return errors.New("Unsupported remote connector transport")
	}
	if r.Auth.Type == "api-key" {
		if !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`).MatchString(r.Auth.Name) {
			return errors.New("Invalid remote connector credential field")
		}
		if strings.ContainsAny(r.Auth.Prefix, "\r\n") {
			return errors.New("Invalid remote connector credential prefix")
		}
	}
	return nil
}

type RemotePlugin struct {
	Manifest  protocol.JSONObject `json:"manifest"`
	MCPConfig protocol.JSONObject `json:"mcpConfig"`
}

func BuildRemotePlugin(r RemoteRecipe) (RemotePlugin, error) {
	if err := validateRemoteRecipe(r); err != nil {
		return RemotePlugin{}, err
	}
	config := record(field("type", r.Transport), field("url", r.Endpoint))
	if r.Auth.Type == "oauth" && r.Auth.ClientID != "" {
		config.Set("oauth", raw(record(field("clientId", r.Auth.ClientID))))
	}
	if r.Auth.Type == "api-key" {
		placeholder := "${user_config.apiKey}"
		if r.Auth.In == "header" {
			config.Set("headers", raw(record(field(r.Auth.Name, r.Auth.Prefix+placeholder))))
		} else {
			u, _ := weburl.Parse(r.Endpoint)
			u.SearchParams().Delete(r.Auth.Name)
			separator := "?"
			if u.Search() != "" {
				separator = "&"
			}
			config.Set("url", quote(u.Href(false)+separator+encodeURIComponent(r.Auth.Name)+"="+placeholder))
		}
	}
	manifest := record(field("name", "office-"+r.ID), field("version", r.Version), field("description", "Managed "+r.ID+" connector"))
	if r.Auth.Type == "api-key" {
		manifest.Set("userConfig", raw(record(field("apiKey", record(field("type", "string"), field("title", "API Key"), field("description", "Service API key for this connector"), field("sensitive", true), field("required", true))))))
	}
	return RemotePlugin{manifest, record(field("mcpServers", record(field("service", config))))}, nil
}

func encodeURIComponent(s string) string {
	// WHATWG URLSearchParams uses form encoding while the Source marker path
	// uses encodeURIComponent. Only the parameter name is encoded here.
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

type nativeSkillRecipe struct{ Name, Help, Task string }

var nativeSkillRecipes = map[string]nativeSkillRecipe{
	"feishu":   {"飞书 / Feishu", "calendar --help", "Read the user’s calendar agenda with calendar +agenda. Discover document commands with docs --help."},
	"dingtalk": {"钉钉 / DingTalk", "calendar +agenda --help", "For today’s agenda use calendar +agenda --format json. The account needs calendar access and any required organization approval. Discover other domains with --help before use."},
	"wecom":    {"企业微信 / WeCom", "calendar schedules list --help", "List calendar schedules with calendar schedules list --begin-time <start> --end-time <end>, using the requested time window and this version’s documented time format. Omitted dates default to the next 30 days, not today. This command already returns JSON; do not append an unsupported --format flag. Discover other domains with --help before use."},
}

const bundleSkillText = `---
name: {{.PluginName}}
description: Use {{.DisplayName}} for the user's requested creative or development task.
---

# {{.DisplayName}}

{{.Description}}

This plugin contains the upstream skills listed below. Read the corresponding SKILL.md and its referenced files before using the workflow. Paths are relative to this plugin's skills directory:
{{.Entries}}

{{.Requirements}}

Installing this plugin installs instructions and supporting files only, not Node, browsers, renderers, or service accounts. Check the documented dependencies before running a task. Do not claim that installation verifies a render or business operation. Online services and writes require authorization from the user's task.

Source: https://github.com/{{.Repository}}/tree/{{.Commit}}
License: {{.License}}
`
const remoteSkillText = `---
name: {{.PluginName}}
description: Use {{.DisplayName}} when the user requests its connected services.
---

# {{.DisplayName}}

{{.Description}}

Use the tools exposed by plugin:{{.PluginName}}:service. Inspect their schemas and use only available tools. Do not invent CLI commands, install other software, or read credential files.
{{.Requirements}}

Use read-only discovery first. Business writes (messages, documents, transactions) require authorization from the user's task. Connection or tools/list success does not prove every permission or a business operation succeeded. Report missing scopes, limits, and service errors accurately. If authentication fails, direct the user to this connector's settings. Never print tokens.

Official documentation: {{.Homepage}}
`
const nativeSkillText = `---
name: {{.PluginName}}
description: Use {{.Name}} through the desktop-managed connector when the user requests its documents, calendar, or collaboration services.
---

# {{.Name}}

This skill belongs to the desktop connector. The desktop manages installation and account connection. Use the pinned executable below; do not install a global CLI, change accounts, or delete configuration directories.

## Invocation

On macOS / a POSIX shell:

{{.Fence}}sh
{{.BashCommand}} {{.Help}}
{{.Fence}}

On Windows PowerShell:

{{.Fence}}powershell
{{.WindowsCommand}} {{.Help}}
{{.Fence}}

Keep the executable path, environment and prefix arguments when appending a command. Request JSON output where supported and inspect this version's --help instead of guessing parameters. {{.Task}}

## Account and permissions

Use the connected account only. A successful login does not grant every business permission. If authentication expires or required scopes are missing, explain the service's error and direct the user to the desktop connector's connection flow. Do not print tokens, perform login from a task, or read credential files.

Prefer read-only discovery. Send messages, edit documents, create events, or perform other writes only when the user's request authorizes that action. Show errors accurately; never claim a business operation succeeded from login status alone.

Official documentation: {{.Homepage}}
Pinned package: {{.PackageName}}@{{.Version}}
`

func RenderConnectorSkill(c Catalog, def protocol.JSONObject, i Installation) (string, error) {
	name, err := managedPluginName(def)
	if err != nil {
		return "", err
	}
	value := func(key, fallback string) string {
		v := def.Get(key)
		if nullish(v) {
			return fallback
		}
		text, e := protocol.JavaScriptStringJSON(v)
		var result string
		if e == nil {
			_ = json.Unmarshal(text, &result)
		}
		return result
	}
	interpolate := func(key string) string {
		text, _ := protocol.JavaScriptStringJSON(def.Get(key))
		var s string
		_ = json.Unmarshal(text, &s)
		return s
	}
	data := map[string]string{"PluginName": name, "DisplayName": value("displayName", def.String("id")), "Description": value("description", ""), "Requirements": value("requirements", ""), "Homepage": interpolate("homepage"), "PackageName": interpolate("packageName"), "Version": interpolate("version"), "Fence": "```"}
	text := nativeSkillText
	if bundle := c.skill(def.String("id"), def.String("version")); bundle != nil {
		text = bundleSkillText
		entries := []string{}
		seen := map[string]bool{}
		for _, f := range bundle.Files {
			if skillEntry.MatchString(f.Target) && !seen[f.Target] {
				seen[f.Target] = true
				entries = append(entries, "- ../"+strings.TrimPrefix(f.Target, "skills/"))
			}
		}
		data["Entries"] = strings.Join(entries, "\n")
		data["Repository"] = bundle.Repository
		data["Commit"] = bundle.Commit
		data["License"] = bundle.License
	} else if c.remote(def.String("id")) != nil {
		text = remoteSkillText
	} else {
		recipe, ok := nativeSkillRecipes[def.String("id")]
		if !ok {
			return "", errors.New("Unknown native connector skill")
		}
		data["Name"], data["Help"], data["Task"] = recipe.Name, recipe.Help, recipe.Task
		bashQuote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
		psQuote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
		command := append([]string{i.Command}, i.Args...)
		bash, ps := []string{}, []string{}
		keys := i.EnvironmentOrder
		if keys == nil {
			for k := range i.Environment {
				keys = append(keys, k)
			}
			sort.Strings(keys)
		}
		for _, k := range keys {
			if k == "DWS_CONFIG_DIR" || k == "DWS_KEYCHAIN_DIR" || k == "DWS_DISABLE_KEYCHAIN" {
				bash = append(bash, k+"="+bashQuote(i.Environment[k]))
				ps = append(ps, "$env:"+k+" = "+psQuote(i.Environment[k])+"; ")
			}
		}
		for _, v := range command {
			bash = append(bash, bashQuote(v))
		}
		data["BashCommand"] = strings.Join(bash, " ")
		quoted := []string{}
		for _, v := range command {
			quoted = append(quoted, psQuote(v))
		}
		data["WindowsCommand"] = strings.Join(ps, "") + "& " + strings.Join(quoted, " ")
	}
	t, err := template.New("connector-skill").Parse(text)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	err = t.Execute(&out, data)
	return out.String(), err
}
