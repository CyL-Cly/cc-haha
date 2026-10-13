package clibootstrap

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliinstallation"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
)

type nativeDoctorChannels struct{ Latest, Stable *string }

// The original Doctor queries distribution channels. This repository's
// distribution is signed Go assets, so it shares the Go installer resolver
// rather than recommending the old TypeScript executable.
func (m *MainCommand) nativeDoctorChannels(scope *cooperative.Scope) (nativeDoctorChannels, error) {
	if !scope.BelongsTo(m.Owner) {
		return nativeDoctorChannels{}, cooperative.ErrRestricted
	}
	env := map[string]string{}
	for key, value := range m.Native.Environment {
		env[key] = value
	}
	tls, e := tlsconfig.NewState(m.Native.readFile)
	if e != nil {
		return nativeDoctorChannels{}, e
	}
	client, e := webfetch.NewHTTPClient(env, tls)
	if e != nil {
		return nativeDoctorChannels{}, e
	}
	repository := cliinstallation.RepositoryClient{HTTP: client, APIBase: env["CC_HAHA_CLI_RELEASE_API"]}
	data, e := scope.AwaitBytes(cooperative.NetworkOpen, func(ctx context.Context) ([]byte, error) {
		return json.Marshal(probeNativeDoctorChannels(ctx, repository))
	})
	if e != nil {
		return nativeDoctorChannels{}, e
	}
	var result nativeDoctorChannels
	e = json.Unmarshal(data, &result)
	return result, e
}
func probeNativeDoctorChannels(ctx context.Context, repository cliinstallation.RepositoryClient) nativeDoctorChannels {
	if repository.HTTP != nil {
		defer repository.HTTP.CloseIdleConnections()
	}
	result := nativeDoctorChannels{}
	var workers sync.WaitGroup
	for _, channel := range []string{"latest", "stable"} {
		workers.Add(1)
		go func(channel string) {
			defer workers.Done()
			active, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			release, e := repository.Resolve(active, channel)
			if e != nil {
				return
			}
			version := strings.TrimPrefix(release.Tag, "v")
			if channel == "latest" {
				result.Latest = &version
			} else {
				result.Stable = &version
			}
		}(channel)
	}
	workers.Wait()
	return result
}
func nativeDoctorChannelsText(channels nativeDoctorChannels) string {
	if channels.Latest == nil {
		return "\nAvailable versions\nFailed to fetch versions\n"
	}
	text := "\nAvailable versions\n"
	if channels.Stable != nil {
		text += "Stable version: " + *channels.Stable + "\n"
	}
	return text + "Latest version: " + *channels.Latest + "\n"
}

func (m *MainCommand) nativeDoctorChannelsText(scope *cooperative.Scope) (string, error) {
	channels, e := m.nativeDoctorChannels(scope)
	if e != nil {
		return "", e
	}
	return nativeDoctorChannelsText(channels), nil
}
