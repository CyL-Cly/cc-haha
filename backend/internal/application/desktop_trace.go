package application

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// Source buildChildEnv discards inherited provider labels, then stamps only
// the explicit desktop selection. CLI and cron keep their own capture policy.
func desktopTraceEnvironment(ctx context.Context, o Options) (map[string]string, error) {
	env := make(map[string]string, len(o.Environment)+4)
	for key, value := range o.Environment {
		env[key] = value
	}
	for _, key := range []string{"CC_HAHA_TRACE_PROVIDER_ID", "CC_HAHA_TRACE_PROVIDER_NAME", "CC_HAHA_TRACE_PROVIDER_FORMAT", "CC_HAHA_OPENAI_FAST_MODE"} {
		delete(env, key)
	}
	if o.Trace == nil || !o.Trace.Settings(ctx).Enabled {
		return env, nil
	}
	env["CC_HAHA_TRACE_API_CALLS"] = "1"
	if o.RuntimeSelection == nil || o.RuntimeSelection.ProviderID == nil {
		return env, nil
	}
	options := o.ProviderOptions
	if options == nil {
		store := o.Store
		if store == nil {
			store = storage.New(storage.Options{})
		}
		config := env["CLAUDE_CONFIG_DIR"]
		if config == "" {
			config = filepath.Join(env["HOME"], ".claude")
		}
		options = &providerconfig.Options{Store: store, ConfigDir: config}
	}
	providers, err := providerconfig.New(*options)
	if err != nil {
		return nil, err
	}
	provider, err := providers.Provider(ctx, *o.RuntimeSelection.ProviderID)
	if err != nil {
		return nil, err
	}
	stringField := func(key string) string {
		var value string
		_ = json.Unmarshal(provider[key], &value)
		return value
	}
	env["CC_HAHA_TRACE_PROVIDER_ID"] = stringField("id")
	env["CC_HAHA_TRACE_PROVIDER_NAME"] = stringField("name")
	format := stringField("apiFormat")
	if provider["apiFormat"] == nil || string(provider["apiFormat"]) == "null" {
		format = "anthropic"
	}
	env["CC_HAHA_TRACE_PROVIDER_FORMAT"] = format
	return env, nil
}
