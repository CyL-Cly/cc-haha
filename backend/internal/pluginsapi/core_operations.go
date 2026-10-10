package pluginsapi

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"path/filepath"
	"time"
)

// CoreOperationsOptions binds only the original pure plugin operations. CLI
// consumers do not invent desktop component/session/agent producers.
type CoreOperationsOptions struct {
	WorkDir, PluginsDir string
	WriteEnabledMany    func(context.Context, string, []string) error
	EnabledOrder        func(context.Context) ([]string, error)
	Installed           *pluginregistry.InstalledRegistry
	Materializer        *pluginregistry.Materializer
	Load                func(context.Context, bool) (pluginregistry.Result, error)
	ReadSettings        func(context.Context, string, string) (protocol.JSONObject, error)
	WriteEnabled        func(context.Context, string, string, string, *bool) error
	EditableEnabled     func(context.Context) (map[string]bool, error)
	ClearCaches         func(context.Context) error
	KnownMarketplaces   func(context.Context) (protocol.JSONObject, error)
	LookupPlugin        func(context.Context, string) (*MarketplacePlugin, error)
	DeleteOptions       func(context.Context, string) error
	IsBuiltin           func(string) bool
	Now                 func() time.Time
}

func NewCoreOperations(o CoreOperationsOptions) (*Service, error) {
	if !filepath.IsAbs(o.WorkDir) || !filepath.IsAbs(o.PluginsDir) || o.Installed == nil || o.Materializer == nil || o.Load == nil || o.ReadSettings == nil || o.WriteEnabled == nil || o.EditableEnabled == nil || o.ClearCaches == nil || o.KnownMarketplaces == nil || o.LookupPlugin == nil || o.DeleteOptions == nil || o.IsBuiltin == nil || o.WriteEnabledMany == nil || o.EnabledOrder == nil {
		return nil, errors.New("CLI plugin operations require actual registry/materializer/settings/options/cache producers")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	creator, err := pluginregistry.NewCreator(nil)
	if err != nil {
		return nil, err
	}
	return &Service{creator: creator, core: &o, options: Options{WorkDir: o.WorkDir, PluginsDir: o.PluginsDir, Installed: o.Installed, Materializer: o.Materializer, Load: o.Load, ReadSettings: o.ReadSettings, WriteEnabled: o.WriteEnabled, EditableEnabled: o.EditableEnabled, ClearCaches: o.ClearCaches, KnownMarketplaces: o.KnownMarketplaces, LookupPlugin: o.LookupPlugin, DeleteOptions: o.DeleteOptions, IsBuiltin: o.IsBuiltin, Now: o.Now}}, nil
}
