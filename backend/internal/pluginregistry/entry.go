package pluginregistry

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// FinishEntry is the common local/cached/downloaded tail. Marketplace
// supplementation intentionally keeps Source's different inline-command and
// hook replacement rules when there is no plugin manifest.
func (c *Creator) FinishEntry(ctx context.Context, entry protocol.JSONObject, id string, enabled bool, path string) (*Loaded, []protocol.JSONObject, error) {
	hasManifest := c.exists(filepath.Join(path, ".claude-plugin", "plugin.json"))
	strict := string(entry.Get("strict")) != "false"
	p, errors, err := c.Create(ctx, path, id, enabled, entry.String("name"), strict)
	if err != nil {
		return nil, nil, err
	}
	if source := object(entry.Get("source")); source.String("sha") != "" {
		p.SHA = source.String("sha")
	}
	if hasManifest && !strict {
		for _, field := range []string{"commands", "agents", "skills", "hooks", "outputStyles"} {
			if truthy(entry.Get(field)) {
				return nil, []protocol.JSONObject{errorRecord("generic-error", id, fieldValue("error", "Plugin "+entry.String("name")+" has conflicting manifests: both plugin.json and marketplace entry specify components. Set strict: true in marketplace entry or remove component specs from one location."))}, nil
			}
		}
	}
	if !hasManifest {
		p.Manifest = entry.Clone()
		p.Manifest.Delete("id")
		p.Manifest.Delete("source")
		p.Manifest.Delete("strict")
		p.Name = p.Manifest.String("name")
	}
	if value := entry.Get("commands"); truthy(value) {
		commands := object(value)
		fields := commands.OwnProperties()
		metadataMapping := len(fields) > 0 && (object(fields[0].Value).Get("source") != nil || object(fields[0].Value).Get("content") != nil)
		if metadataMapping {
			metadata := protocol.NewJSONObject()
			if hasManifest && p.CommandsMetadata != nil {
				metadata = p.CommandsMetadata.Clone()
			}
			paths := []string{}
			for _, property := range fields {
				m := object(property.Value)
				relative := m.String("source")
				if relative == "" {
					continue
				}
				full := filepath.Join(path, relative)
				if c.exists(full) {
					paths = append(paths, full)
					_ = metadata.SetPropertyJSON(property.NameJSON, property.Value)
				} else {
					errors = append(errors, errorRecord("path-not-found", id, fieldValue("plugin", entry.String("name")), fieldValue("path", full), fieldValue("component", "commands")))
				}
			}
			if len(paths) > 0 {
				if hasManifest {
					p.CommandsPaths = append(p.CommandsPaths, paths...)
				} else {
					p.CommandsPaths = paths
				}
				p.CommandsMetadata = &metadata
			}
		} else {
			paths := c.paths(path, entry.String("name"), id, "commands", value, &errors)
			if len(paths) > 0 {
				if hasManifest {
					p.CommandsPaths = append(p.CommandsPaths, paths...)
				} else {
					p.CommandsPaths = paths
				}
			}
		}
	}
	for _, component := range []struct {
		key, component string
		paths          *[]string
	}{{"agents", "agents", &p.AgentsPaths}, {"skills", "skills", &p.SkillsPaths}, {"outputStyles", "output-styles", &p.OutputStylesPaths}} {
		if value := entry.Get(component.key); truthy(value) {
			paths := c.paths(path, entry.String("name"), id, component.component, value, &errors)
			if len(paths) > 0 {
				if hasManifest {
					*component.paths = append(*component.paths, paths...)
				} else {
					*component.paths = paths
				}
			}
		}
	}
	if value := entry.Get("hooks"); truthy(value) {
		hooks := object(value)
		if !hasManifest {
			p.HooksConfig = &hooks
		} else {
			base := protocol.NewJSONObject()
			if p.HooksConfig != nil {
				base = p.HooksConfig.Clone()
			}
			for _, property := range hooks.OwnProperties() {
				_ = base.SetPropertyJSON(property.NameJSON, property.Value)
			}
			p.HooksConfig = &base
		}
	}
	return &p, errors, nil
}
func fieldValue(name string, value any) protocol.JSONField {
	valueJSON, _ := json.Marshal(value)
	return protocol.JSONField{Name: name, Value: valueJSON}
}
