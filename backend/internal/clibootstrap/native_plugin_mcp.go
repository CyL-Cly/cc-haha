package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpb"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginmcp"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginoptions"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

// PluginMCP shares the process's actual settings writer, plugin options and
// credential store with the bundle loader. Its callbacks are evaluated after
// construction, so sensitive values have one storage/merge authority.
func (p *NativeProcessSettings) PluginMCP(options *pluginoptions.Service, secure *securestorage.Store) (*pluginmcp.Service, *mcpb.Service, error) {
	n := p.n
	var plugins *pluginmcp.Service
	environment := n.nativeEnvironmentValue
	settings := func(ctx context.Context) (protocol.JSONObject, error) {
		if err := ctx.Err(); err != nil {
			return protocol.JSONObject{}, err
		}
		value, err := p.InitialSettings()
		if value == nil {
			return protocol.NewJSONObject(), err
		}
		return value.Clone(), err
	}
	loader, err := mcpb.New(mcpb.Options{Home: n.Home, Platform: p.platform, Environment: environment, Now: n.Now, LoadUserConfig: func(ctx context.Context, id, name string) (*protocol.JSONObject, error) {
		return plugins.LoadServerUserConfig(ctx, id, name)
	}, SaveUserConfig: func(ctx context.Context, id, name string, values, schema protocol.JSONObject) error {
		return plugins.SaveServerUserConfig(ctx, id, name, values, schema)
	}})
	if err != nil {
		return nil, nil, err
	}
	plugins, err = pluginmcp.New(pluginmcp.Options{Home: n.Home, ConfigDir: n.ConfigHomeDirectory(), CWD: n.CWD, Platform: p.platform, Environment: environment, PluginOptions: options, Secure: secure, Settings: settings, WriteSettings: p.WriteUserSettings, LoadMCPB: func(ctx context.Context, source, path, id string, progress func(string)) (pluginmcp.MCPBResult, error) {
		r, err := loader.Load(ctx, source, path, id, progress, nil, false)
		return pluginmcp.MCPBResult{NeedsConfig: r.NeedsConfig, Manifest: r.Manifest, Config: r.Config, ExtractedPath: r.ExtractedPath, ContentHash: r.ContentHash}, err
	}})
	if err != nil {
		return nil, nil, err
	}
	return plugins, loader, nil
}
