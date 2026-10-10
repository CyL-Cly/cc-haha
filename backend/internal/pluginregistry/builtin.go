package pluginregistry

import (
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type BuiltinDefinition struct {
	Name, Description string
	Version           *string
	DefaultEnabled    *bool
	Available         func() bool
	Hooks, MCPServers *protocol.JSONObject
}

// BuiltinRegistry mirrors the original declaration registry; shipped native
// declarations register here during init, separately from disk discovery.
type BuiltinRegistry struct {
	mu          sync.Mutex
	order       []string
	definitions map[string]BuiltinDefinition
}

func NewBuiltinRegistry() *BuiltinRegistry {
	return &BuiltinRegistry{definitions: map[string]BuiltinDefinition{}}
}
func (r *BuiltinRegistry) Register(def BuiltinDefinition) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.definitions[def.Name]; !exists {
		r.order = append(r.order, def.Name)
	}
	r.definitions[def.Name] = def
}
func (r *BuiltinRegistry) Load(settings protocol.JSONObject) []Loaded {
	r.mu.Lock()
	defer r.mu.Unlock()
	enabled, disabled := []Loaded{}, []Loaded{}
	for _, name := range r.order {
		d := r.definitions[name]
		if d.Available != nil && !d.Available() {
			continue
		}
		id := name + "@builtin"
		value := object(settings.Get("enabledPlugins")).Get(id)
		isEnabled := true
		if d.DefaultEnabled != nil {
			isEnabled = *d.DefaultEnabled
		}
		if len(value) > 0 {
			isEnabled = string(value) == "true"
		}
		manifest := protocol.NewJSONObject(fieldValue("name", name), fieldValue("description", d.Description))
		if d.Version != nil {
			manifest.Set("version", raw(*d.Version))
		}
		p := Loaded{Name: name, Manifest: manifest, Path: "builtin", Source: id, Repository: id, Enabled: isEnabled, IsBuiltin: true, HooksConfig: d.Hooks, MCPServers: d.MCPServers}
		if isEnabled {
			enabled = append(enabled, p)
		} else {
			disabled = append(disabled, p)
		}
	}
	return append(enabled, disabled...)
}
