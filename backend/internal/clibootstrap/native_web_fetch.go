package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
	"os"
	"path/filepath"
)

type webPersistenceKey struct{}

func (m *MainPrint) nativeWebFetchBindings() (*builtintools.WebFetchBindings, error) {
	if !m.options.Native.NativeProcess {
		return nil, nil
	}
	n := m.options.Native
	client, e := webfetch.NewHTTPClient(n.Environment, m.tls)
	if e != nil {
		return nil, e
	}
	service := m.webFetch
	if service == nil {
		service, e = webfetch.New(webfetch.Options{HTTPClient: client, Now: n.Now, UserAgent: fmt.Sprintf("Claude-User (claude-code/%s; +https://support.anthropic.com/)", m.options.Version), Settings: func(context.Context) (webfetch.Settings, error) {
			settings, e := m.Startup.GetInitialSettings()
			if e != nil {
				return webfetch.Settings{}, e
			}
			result := webfetch.Settings{DesktopServerURL: n.Environment["CC_HAHA_DESKTOP_SERVER_URL"]}
			if value := settings.Get("skipWebFetchPreflight"); value != nil {
				var skip bool
				if json.Unmarshal(value, &skip) == nil {
					result.SkipPreflight = &skip
				}
			}
			return result, nil
		}, PersistID: func() string {
			return fmt.Sprintf("webfetch-%d-%s", n.Now().UnixMilli(), webfetch.RandomBase36Suffix(nativeMathRandom()))
		}, PersistBinary: func(ctx context.Context, data []byte, mime, id string) (webfetch.Persisted, error) {
			dir, ok := ctx.Value(webPersistenceKey{}).(string)
			if !ok || !filepath.IsAbs(dir) {
				return webfetch.Persisted{}, errors.New("WebFetch requires original session persistence directory")
			}
			if e := n.check("mkdir", dir); e != nil {
				return webfetch.Persisted{}, e
			}
			if e := os.MkdirAll(dir, 0700); e != nil {
				return webfetch.Persisted{}, e
			}
			path := filepath.Join(dir, id+"."+webfetch.ExtensionForMIME(mime))
			if e := n.check("writeFile", path); e != nil {
				return webfetch.Persisted{Error: e}, nil
			}
			if e := os.WriteFile(path, data, 0600); e != nil {
				return webfetch.Persisted{Error: e}, nil
			}
			return webfetch.Persisted{Path: path, Size: len(data)}, nil
		}, ApplyPrompt: func(context.Context, json.RawMessage, bool) (json.RawMessage, error) {
			return nil, mainCap("WebFetch model call outside original owner")
		}})
		if e != nil {
			return nil, e
		}
		m.webFetch = service
	}
	return &builtintools.WebFetchBindings{Fetch: service, Now: n.Now, Prepare: func(scope *cooperative.Scope, ctx context.Context) (context.Context, error) {
		if !scope.BelongsTo(m.Owner) {
			return nil, cooperative.ErrRestricted
		}
		snapshot := m.state.Snapshot()
		config := n.ConfigHomeDirectory()
		if config == "" {
			config = filepath.Join(n.Home, ".claude")
		}
		dir := filepath.Join(config, "projects", projectpath.Sanitize(snapshot.OriginalCWD), m.Controller.SessionID(), "tool-results")
		return context.WithValue(ctx, webPersistenceKey{}, dir), nil
	}, ApplyPrompt: func(ctx context.Context, scope *cooperative.Scope, prompt json.RawMessage, nonInteractive bool) (json.RawMessage, error) {
		if m.Host == nil || m.nativeModelSelector == nil {
			return nil, mainCap("original initialized secondary model host")
		}
		settings, e := m.Startup.GetInitialSettings()
		if e != nil {
			return nil, e
		}
		environment := map[string]string{}
		snapshot := m.environment.Snapshot()
		for _, p := range snapshot.OwnProperties() {
			var key, value string
			_ = json.Unmarshal(p.NameJSON, &key)
			_ = json.Unmarshal(p.Value, &value)
			environment[key] = value
		}
		selector, e := modelselection.NewParsing(modelselection.Options{Environment: environment, Settings: settings.Clone()})
		if e != nil {
			return nil, e
		}
		return m.Host.ApplyWebFetchPrompt(ctx, scope, prompt, selector.SmallFastModel(), nonInteractive)
	}}, nil
}
