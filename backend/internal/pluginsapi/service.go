// Package pluginsapi implements the desktop management boundary using the
// same native installed registry, materializer, settings and runtime caches.
package pluginsapi

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
)

type APIError = settings.APIError
type MarketplacePlugin struct {
	Entry           protocol.JSONObject
	InstallLocation string
}
type Options struct {
	WorkDir, PluginsDir string
	Installed           *pluginregistry.InstalledRegistry
	Materializer        *pluginregistry.Materializer
	Load                func(context.Context, bool) (pluginregistry.Result, error)
	Hydrate             func(context.Context, *pluginregistry.Result) error
	ReadSettings        func(context.Context, string, string) (protocol.JSONObject, error)
	WriteEnabled        func(context.Context, string, string, string, *bool) error
	EditableEnabled     func(context.Context) (map[string]bool, error)
	ClearCaches         func(context.Context) error
	KnownMarketplaces   func(context.Context) (protocol.JSONObject, error)
	LookupPlugin        func(context.Context, string) (*MarketplacePlugin, error)
	DeleteOptions       func(context.Context, string) error
	SkillsCount         func(context.Context) (int, error)
	AgentsCount         func(context.Context, string) (int, error)
	LoadHooks           func(context.Context) error
	ReloadSession       func(context.Context, string) (protocol.JSONObject, error)
	IsBuiltin           func(string) bool
	BuiltinDetail       func(string) (protocol.JSONObject, error)
	Now                 func() time.Time
}
type Service struct {
	options Options
	creator *pluginregistry.Creator
	core    *CoreOperationsOptions
}

func New(o Options) (*Service, error) {
	if !filepath.IsAbs(o.WorkDir) || !filepath.IsAbs(o.PluginsDir) || o.Installed == nil || o.Materializer == nil || o.Load == nil || o.Hydrate == nil || o.ReadSettings == nil || o.WriteEnabled == nil || o.EditableEnabled == nil || o.ClearCaches == nil || o.KnownMarketplaces == nil || o.LookupPlugin == nil || o.DeleteOptions == nil || o.SkillsCount == nil || o.AgentsCount == nil || o.LoadHooks == nil || o.ReloadSession == nil || o.IsBuiltin == nil || o.BuiltinDetail == nil {
		return nil, errors.New("Plugins require actual shared registry/materializer/settings/components/options/session producers")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	c, err := pluginregistry.NewCreator(nil)
	if err != nil {
		return nil, err
	}
	return &Service{options: o, creator: c}, nil
}
func bad(message string) error {
	return &APIError{StatusCode: 400, Code: "BAD_REQUEST", Message: message}
}
func missing(message string) error {
	return &APIError{StatusCode: 404, Code: "NOT_FOUND", Message: message}
}
func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func obj(v json.RawMessage) protocol.JSONObject {
	var o protocol.JSONObject
	_ = o.UnmarshalJSON(v)
	if !o.IsObject() {
		return protocol.NewJSONObject()
	}
	return o
}
func rec(fields ...protocol.JSONField) protocol.JSONObject { return protocol.NewJSONObject(fields...) }
func field(name string, v any) protocol.JSONField {
	return protocol.JSONField{Name: name, Value: raw(v)}
}
func arr(v json.RawMessage) []json.RawMessage {
	var a []json.RawMessage
	_ = json.Unmarshal(v, &a)
	return a
}
func properties(o protocol.JSONObject) []string {
	keys := []string{}
	for _, p := range o.OwnProperties() {
		var key string
		_ = json.Unmarshal(p.NameJSON, &key)
		keys = append(keys, key)
	}
	return keys
}
func splitID(id string) (string, string) { // Source splits at the first @ and ignores subsequent segments.
	parts := []string{}
	start := 0
	for i := range id {
		if id[i] == '@' {
			parts = append(parts, id[start:i])
			start = i + 1
		}
	}
	parts = append(parts, id[start:])
	if len(parts) > 1 {
		return parts[0], parts[1]
	}
	return id, ""
}
func (s *Service) load(ctx context.Context, full bool) (pluginregistry.Result, error) {
	result, err := s.options.Load(ctx, full)
	if err == nil && s.options.Hydrate != nil {
		err = s.options.Hydrate(ctx, &result)
	}
	return result, err
}
func (s *Service) source(ctx context.Context, scope, cwd string) (protocol.JSONObject, error) {
	if cwd == "" && (scope == "project" || scope == "local") {
		cwd = s.options.WorkDir
	}
	return s.options.ReadSettings(ctx, scope, cwd)
}
func requireRoot(scope, cwd string) error {
	if (scope == "project" || scope == "local") && cwd == "" {
		return bad("Plugin " + scope + " scope requires the active project directory")
	}
	return nil
}
func success(message string) protocol.JSONObject {
	return rec(field("ok", true), field("message", message))
}
