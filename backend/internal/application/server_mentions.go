package application

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/capabilitymentions"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/connectors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

var mentionUnexpandedBundle = regexp.MustCompile(`(?i)\.(?:mcpb|dxt)(?:$|[?#])`)

func newDesktopCapabilityMentions(native *clibootstrap.NativeBoundary, settings *clibootstrap.NativeProcessSettings, mcp *desktopMCPRuntime, providers *providerconfig.Service) (func(context.Context, string, string) (capabilitymentions.Response, error), error) {
	commands, err := settings.SkillCommands(clibootstrap.NativeSkillOptions{})
	if err != nil {
		return nil, err
	}
	catalog, err := connectors.DefaultCatalog()
	if err != nil {
		return nil, err
	}
	compare := collate.New(language.English)
	var mu sync.Mutex
	return func(ctx context.Context, cwd, providerID string) (capabilitymentions.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		if cwd == "" {
			cwd = native.CWD
		}
		settings.InvalidateSettingsCache()
		commands.Clear()
		state, err := settings.RefreshInstalledPluginsForProject(ctx, cwd)
		if err != nil {
			return capabilitymentions.Response{}, err
		}
		local, err := commands.Local(ctx, cwd)
		if err != nil {
			return capabilitymentions.Response{}, err
		}
		env := map[string]string{}
		for key, value := range native.Environment {
			env[key] = value
		}
		// The server's inherited image provider is deliberately not used by the
		// Source mention endpoint: availability follows this session selection.
		for key := range env {
			if len(key) >= 14 && key[:14] == "CC_HAHA_IMAGE_" {
				delete(env, key)
			}
		}
		for key, value := range providers.MentionImageEnvironment(ctx, providerID) {
			env[key] = value
		}
		bundled, err := skillcommands.NativeExternalBundled(skillcommands.BundledOptions{Environment: env})
		if err != nil {
			return capabilitymentions.Response{}, err
		}
		all := append(append(append(bundled, local...), commands.PluginCommands(ctx, state.Enabled)...), commands.PluginSkills(ctx, state.Enabled)...)
		snapshot := capabilitymentions.Snapshot{Brands: catalog.Definitions, Compare: compare.CompareString}
		for _, definition := range all {
			snapshot.Commands = append(snapshot.Commands, definition.Metadata())
		}
		var entry desktopMCPEntry
		if len(state.Enabled) > 0 {
			// Construction only: no Runtime.Connect or probe is called here.
			if _, err = mcp.get(ctx, cwd); err != nil {
				return capabilitymentions.Response{}, err
			}
			entryCWD := cwd
			if !filepath.IsAbs(entryCWD) {
				entryCWD = filepath.Join(native.CWD, entryCWD)
			}
			if real, failure := filepath.EvalSymlinks(entryCWD); failure == nil {
				entryCWD = real
			}
			mcp.mu.Lock()
			entry = mcp.entries[entryCWD]
			mcp.mu.Unlock()
		}
		for _, plugin := range state.Enabled {
			data, _ := json.Marshal(plugin)
			var record protocol.JSONObject
			_ = record.UnmarshalJSON(data)
			var specs []json.RawMessage
			if json.Unmarshal(plugin.Manifest.Get("mcpServers"), &specs) != nil {
				specs = []json.RawMessage{plugin.Manifest.Get("mcpServers")}
			}
			bundle := false
			for _, spec := range specs {
				var value string
				if json.Unmarshal(spec, &value) == nil && mentionUnexpandedBundle.MatchString(value) {
					bundle = true
					break
				}
			}
			if !bundle && plugin.MCPServers == nil && entry.plugins != nil {
				servers, failure := entry.plugins.LoadPluginMCPServers(ctx, record, &state.Errors)
				if failure == nil && servers != nil {
					body, _ := servers.MarshalJSON()
					record.Set("mcpServers", body)
				}
			}
			snapshot.EnabledPlugins = append(snapshot.EnabledPlugins, record)
		}
		for _, failure := range state.Errors {
			snapshot.FailedPluginIDs = append(snapshot.FailedPluginIDs, failure.String("source"))
		}
		return capabilitymentions.Build(snapshot), nil
	}, nil
}
