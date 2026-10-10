// Package capabilitymentions projects callable identities from the same native
// command and installed-plugin snapshots used by the Agent. Discovery never
// installs a plugin, authenticates, or connects an MCP peer.
package capabilitymentions

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

type Snapshot struct {
	Commands, EnabledPlugins, Brands []protocol.JSONObject
	FailedPluginIDs                  []string
	Compare                          func(string, string) int
}

type Response struct {
	Skills  []protocol.JSONObject `json:"skills"`
	Plugins []protocol.JSONObject `json:"plugins"`
}

func quote(text string) json.RawMessage {
	return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(text)))
}
func raw(value any) json.RawMessage { data, _ := json.Marshal(value); return data }
func obj(value json.RawMessage) protocol.JSONObject {
	var out protocol.JSONObject
	_ = out.UnmarshalJSON(value)
	return out
}
func truthy(value json.RawMessage) bool {
	return len(value) > 0 && string(value) != "null" && string(value) != "false" && string(value) != "0" && string(value) != "-0" && string(value) != `""`
}
func or(value json.RawMessage, fallback json.RawMessage) json.RawMessage {
	if truthy(value) {
		return value
	}
	return fallback
}
func identity(value json.RawMessage) string {
	if len(value) == 0 {
		return "undefined"
	}
	return string(protocol.StringifyValidatedJavaScriptJSON(value))
}
func join(parts ...json.RawMessage) json.RawMessage {
	value, _ := protocol.JoinJavaScriptValuesJSON(parts, "")
	return value
}
func literal(value json.RawMessage) json.RawMessage {
	return quote(string(protocol.StringifyValidatedJavaScriptJSON(value)))
}

var iconID = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func Build(snapshot Snapshot) Response {
	result := Response{Skills: []protocol.JSONObject{}, Plugins: []protocol.JSONObject{}}
	failed := map[string]bool{}
	for _, id := range snapshot.FailedPluginIDs {
		failed[identity(quote(id))] = true
	}
	plugins := map[string]protocol.JSONObject{}
	order := []string{}
	for _, plugin := range snapshot.EnabledPlugins {
		key := identity(plugin.Get("source"))
		if string(plugin.Get("enabled")) == "false" || failed[key] {
			continue
		}
		if _, exists := plugins[key]; !exists {
			order = append(order, key)
		}
		plugins[key] = plugin
	}
	brands := map[string]protocol.JSONObject{}
	for _, brand := range snapshot.Brands {
		brands[identity(brand.Get("pluginId"))] = brand
	}
	addIcon := func(candidate protocol.JSONObject, source json.RawMessage) {
		brand := brands[identity(source)]
		if id := brand.String("id"); iconID.MatchString(id) {
			candidate.Set("icon", quote("/connectors/"+id+".svg"))
		}
	}
	seen := map[string]bool{}
	for _, command := range snapshot.Commands {
		if !skillcommands.Listed(command) || truthy(command.Get("disableNonInteractive")) || string(command.Get("userInvocable")) == "false" || truthy(command.Get("isHidden")) || string(command.Get("isEnabled")) == "false" {
			continue
		}
		name := command.Get("name")
		pluginID := json.RawMessage(nil)
		if command.String("source") == "plugin" {
			pluginID = obj(command.Get("pluginInfo")).Get("repository")
			if !truthy(pluginID) {
				continue
			}
			if _, exists := plugins[identity(pluginID)]; !exists {
				continue
			}
		}
		key := identity(name)
		if !truthy(name) || seen[key] {
			continue
		}
		seen[key] = true
		source := or(pluginID, command.Get("source"))
		preferred := or(command.Get("userFacingName"), name)
		if owner, exists := plugins[identity(pluginID)]; exists && identity(preferred) == key {
			prefix := owner.String("name") + ":"
			if strings.HasPrefix(command.String("name"), prefix) {
				preferred = quote(strings.TrimPrefix(command.String("name"), prefix))
			}
		}
		candidate := protocol.NewJSONObject(protocol.JSONField{Name: "kind", Value: quote("skill")}, protocol.JSONField{Name: "id", Value: name}, protocol.JSONField{Name: "name", Value: name}, protocol.JSONField{Name: "displayName", Value: preferred}, protocol.JSONField{Name: "description", Value: or(command.Get("description"), quote(""))}, protocol.JSONField{Name: "source", Value: source})
		if path := command.Get("skillRoot"); truthy(path) {
			candidate.Set("path", path)
		}
		addIcon(candidate, source)
		candidate.Set("modelText", join(quote("Use the Skill tool with skill: "), literal(name), quote(" for this request.")))
		result.Skills = append(result.Skills, candidate)
	}
	for _, key := range order {
		plugin := plugins[key]
		skills := []json.RawMessage{}
		for _, skill := range result.Skills {
			if identity(skill.Get("source")) == key {
				skills = append(skills, skill.Get("name"))
			}
		}
		servers := []json.RawMessage{}
		for _, property := range obj(plugin.Get("mcpServers")).OwnProperties() {
			servers = append(servers, join(quote("plugin:"), plugin.Get("name"), quote(":"), property.NameJSON))
		}
		if len(skills) == 0 && len(servers) == 0 {
			continue
		}
		brand := brands[key]
		source := plugin.Get("source")
		candidate := protocol.NewJSONObject(protocol.JSONField{Name: "kind", Value: quote("plugin")}, protocol.JSONField{Name: "id", Value: source}, protocol.JSONField{Name: "name", Value: plugin.Get("name")}, protocol.JSONField{Name: "displayName", Value: or(brand.Get("displayName"), plugin.Get("name"))}, protocol.JSONField{Name: "description", Value: or(brand.Get("description"), or(obj(plugin.Get("manifest")).Get("description"), quote("")))}, protocol.JSONField{Name: "source", Value: source}, protocol.JSONField{Name: "path", Value: plugin.Get("path")})
		addIcon(candidate, source)
		candidate.Set("skillNames", raw(skills))
		candidate.Set("mcpServerNames", raw(servers))
		candidate.Set("modelText", join(quote("Use the enabled plugin "), literal(source), quote(" for this request. Available skill identities: "), literal(raw(skills)), quote(". MCP server identities: "), literal(raw(servers)), quote(". Invoke skills using the Skill tool and use only tools currently available from these servers. If unavailable or authentication is required, report that; do not install or authenticate automatically.")))
		result.Plugins = append(result.Plugins, candidate)
	}
	less := func(rows []protocol.JSONObject) func(int, int) bool {
		return func(i, j int) bool {
			if compared := snapshot.Compare(rows[i].String("displayName"), rows[j].String("displayName")); compared != 0 {
				return compared < 0
			}
			return snapshot.Compare(rows[i].String("id"), rows[j].String("id")) < 0
		}
	}
	sort.SliceStable(result.Skills, less(result.Skills))
	sort.SliceStable(result.Plugins, less(result.Plugins))
	return result
}
