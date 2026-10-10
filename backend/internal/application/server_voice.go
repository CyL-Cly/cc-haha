package application

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/networksettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/uipreferences"
	"github.com/NanmiCoder/cc-haha/backend/internal/voice"
	"net/http"
	"os"
	"path/filepath"
)

func newDesktopVoice(o ServerOptions, preferences *uipreferences.Service, editable *settings.Service) (*voice.Service, error) {
	environment := map[string]string{}
	for k, v := range o.Environment {
		environment[k] = v
	}
	tls, e := tlsconfig.NewState(os.ReadFile)
	if e != nil {
		return nil, e
	}
	provider, e := voice.NewSenseProvider(voice.SenseOptions{DataRoot: filepath.Join(o.ConfigDir, "cc-haha", "voice"), Environment: environment, Download: voice.DownloadOptions{Client: func(ctx context.Context, _ string) (*http.Client, error) {
		env := map[string]string{}
		for k, v := range environment {
			env[k] = v
		}
		user, e := editable.GetUserSettings(ctx)
		if e == nil {
			data, e := json.Marshal(user)
			if e == nil {
				network, e := networksettings.BuildEnvironment(networksettings.Normalize(data), environment)
				if e == nil {
					for k, v := range network {
						env[k] = v
					}
				}
			}
		}
		return voice.NewHTTPClient(env, tls)
	}}})
	if e != nil {
		return nil, e
	}
	return voice.New(voice.Options{Providers: []voice.Provider{provider}, ReadPreferences: func(ctx context.Context) (protocol.JSONObject, error) {
		result, e := preferences.ReadPreferences(ctx)
		if e != nil {
			return protocol.JSONObject{}, e
		}
		var p protocol.JSONObject
		_ = p.UnmarshalJSON(result.Preferences)
		var v protocol.JSONObject
		_ = v.UnmarshalJSON(p.Get("voiceInput"))
		return v, e
	}, UpdatePreferences: func(ctx context.Context, patch protocol.JSONObject) (protocol.JSONObject, error) {
		data, e := patch.MarshalJSON()
		if e != nil {
			return protocol.JSONObject{}, e
		}
		result, e := preferences.UpdateVoiceInputPreferences(ctx, data)
		if e != nil {
			return protocol.JSONObject{}, e
		}
		var p protocol.JSONObject
		_ = p.UnmarshalJSON(result)
		var v protocol.JSONObject
		_ = v.UnmarshalJSON(p.Get("voiceInput"))
		return v, e
	}})
}
