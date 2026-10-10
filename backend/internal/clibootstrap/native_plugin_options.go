package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginoptions"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

// PluginOptions uses the process's actual CLI settings writer and the calling
// application's shared secure store, retaining one credential owner.
func (p *NativeProcessSettings) PluginOptions(secure *securestorage.Store) (*pluginoptions.Service, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ensureSettings(); err != nil {
		return nil, err
	}
	service, err := pluginoptions.New(pluginoptions.Options{Secure: secure, Settings: func(ctx context.Context) (protocol.JSONObject, error) {
		if err := ctx.Err(); err != nil {
			return protocol.JSONObject{}, err
		}
		value, err := p.InitialSettings()
		if value == nil {
			return protocol.NewJSONObject(), err
		}
		return value.Clone(), err
	}, WriteSettings: p.WriteUserSettings})
	if err == nil {
		p.pluginOptions = append(p.pluginOptions, service)
	}
	return service, err
}
func (p *NativeProcessSettings) WriteUserSettings(ctx context.Context, patch clisettings.Patch) error {
	p.mu.Lock()
	if err := ctx.Err(); err != nil {
		p.mu.Unlock()
		return err
	}
	if err := p.ensureSettings(); err != nil {
		p.mu.Unlock()
		return err
	}
	result, err := p.settings.UpdateEditableSource("userSettings", "", patch)
	p.mu.Unlock()
	if err != nil {
		return err
	}
	if result.Error != nil {
		return result.Error
	}
	p.InvalidateSettingsCache()
	return nil
}

func (p *NativeProcessSettings) Policy(ctx context.Context) (protocol.JSONObject, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return protocol.JSONObject{}, mainCap("closed server settings context")
	}
	if err := ctx.Err(); err != nil {
		return protocol.JSONObject{}, err
	}
	value, err := p.n.ResolvePolicy()
	if value == nil {
		return protocol.NewJSONObject(), err
	}
	return value.Clone(), err
}
