package pluginregistry

import (
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func nameAndMarket(id string) (string, string) {
	name, market, _ := strings.Cut(id, "@")
	return name, market
}
func QualifyDependency(dependency, declaring string) string {
	_, depMarket := nameAndMarket(dependency)
	if depMarket != "" {
		return dependency
	}
	_, market := nameAndMarket(declaring)
	if market == "" || market == "inline" {
		return dependency
	}
	return dependency + "@" + market
}
func IsEnabledSetting(value json.RawMessage) bool {
	return string(value) == "true" || jsonKind(value) == "array"
}
func ManagedNames(policy protocol.JSONObject) map[string]bool {
	names := map[string]bool{}
	for _, property := range object(policy.Get("enabledPlugins")).OwnProperties() {
		if jsonKind(property.Value) != "boolean" {
			continue
		}
		var id string
		_ = json.Unmarshal(property.NameJSON, &id)
		name, market := nameAndMarket(id)
		if name != "" && market != "" {
			names[name] = true
		}
	}
	return names
}
func Merge(session, marketplace, builtin []Loaded, managed map[string]bool) ([]Loaded, []protocol.JSONObject) {
	errors := []protocol.JSONObject{}
	plugins := []Loaded{}
	names := map[string]bool{}
	for _, p := range session {
		if managed[p.Name] {
			errors = append(errors, errorRecord("generic-error", p.Source, field("plugin", p.Name), field("error", `--plugin-dir copy of "`+p.Name+`" ignored: plugin is locked by managed settings`)))
			continue
		}
		plugins = append(plugins, p)
		names[p.Name] = true
	}
	for _, p := range marketplace {
		if !names[p.Name] {
			plugins = append(plugins, p)
		}
	}
	plugins = append(plugins, builtin...)
	return plugins, errors
}
func VerifyAndDemote(plugins []Loaded) ([]string, []protocol.JSONObject) {
	known, enabled, knownNames := map[string]bool{}, map[string]bool{}, map[string]bool{}
	counts := map[string]int{}
	for _, p := range plugins {
		known[p.Source] = true
		name, _ := nameAndMarket(p.Source)
		knownNames[name] = true
		if p.Enabled {
			enabled[p.Source] = true
		}
	}
	for id := range enabled {
		name, _ := nameAndMarket(id)
		counts[name]++
	}
	errors := []protocol.JSONObject{}
	changed := true
	for changed {
		changed = false
		for _, p := range plugins {
			if !enabled[p.Source] {
				continue
			}
			var deps []string
			_ = json.Unmarshal(p.Manifest.Get("dependencies"), &deps)
			for _, raw := range deps {
				dependency := QualifyDependency(raw, p.Source)
				_, market := nameAndMarket(dependency)
				satisfied := enabled[dependency]
				present := known[dependency]
				if market == "" {
					satisfied = counts[dependency] > 0
					present = knownNames[dependency]
				}
				if !satisfied {
					delete(enabled, p.Source)
					counts[p.Name]--
					reason := "not-found"
					if present {
						reason = "not-enabled"
					}
					errors = append(errors, errorRecord("dependency-unsatisfied", p.Source, field("plugin", p.Name), field("dependency", dependency), field("reason", reason)))
					changed = true
					break
				}
			}
		}
	}
	demoted := []string{}
	seen := map[string]bool{}
	for _, p := range plugins {
		if p.Enabled && !enabled[p.Source] && !seen[p.Source] {
			seen[p.Source] = true
			demoted = append(demoted, p.Source)
		}
	}
	return demoted, errors
}
