package toolschema

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	weburl "github.com/nlnwa/whatwg-url/url"
)

func firstParty(base string) bool {
	if base == "" {
		return true
	}
	parsed, err := weburl.Parse(base)
	// Source's external empty-policy profile checks URL.host, so an explicit
	// non-default port and internal-only staging host are third-party here.
	return err == nil && parsed.Host() == "api.anthropic.com"
}
func (session *Session) RenderBatch(definitions []Definition, options Options) ([]protocol.JSONObject, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.cache == nil {
		session.cache = map[string]protocol.JSONObject{}
	}
	if session.promptCalls == nil {
		session.promptCalls = map[string]int{}
	}
	// Source Promise.all starts every cache lookup before awaiting immediate
	// builtin/literal prompts. Repeated missing names therefore render twice.
	initial := map[string]protocol.JSONObject{}
	for key, value := range session.cache {
		initial[key] = value
	}
	result := make([]protocol.JSONObject, 0, len(definitions))
	for _, definition := range definitions {
		key := definition.Name
		if len(definition.InputJSONSchema) > 0 && string(definition.InputJSONSchema) != "null" {
			serialized, err := stringify(definition.InputJSONSchema)
			if err != nil {
				return nil, err
			}
			key += ":" + string(serialized)
		}
		base, exists := initial[key]
		if !exists {
			input := definition.InputJSONSchema
			if len(input) == 0 || string(input) == "null" {
				if definition.InputSchema == nil {
					return nil, unsupported("missing original schema")
				}
				var err error
				input, err = compileSchema(*definition.InputSchema, true)
				if err != nil {
					return nil, err
				}
			}
			description, err := session.prompt(definition, options.Host)
			if err != nil {
				return nil, err
			}
			session.promptCalls[definition.Name]++
			base = protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quote(definition.Name)}, protocol.JSONField{Name: "description", Value: description}, protocol.JSONField{Name: "input_schema", Value: input})
			if options.Host.StrictEnabled && definition.Strict && options.Model != "" && firstParty(options.Host.BaseURL) && structuredModel(options.Model) {
				base.Set("strict", json.RawMessage("true"))
			}
			if options.Host.FGTSEnabled && firstParty(options.Host.BaseURL) {
				base.Set("eager_input_streaming", json.RawMessage("true"))
			}
			session.cache[key] = base
		}
		schema := base.Clone()
		if options.DeferLoading {
			schema.Set("defer_loading", json.RawMessage("true"))
		}
		if len(options.CacheControl) > 0 && string(options.CacheControl) != "null" {
			schema.Set("cache_control", options.CacheControl)
		}
		if options.Host.DisableExperimentalBetas {
			for _, name := range []string{"strict", "eager_input_streaming", "defer_loading"} {
				schema.Delete(name)
			}
		}
		result = append(result, schema)
	}
	return result, nil
}
