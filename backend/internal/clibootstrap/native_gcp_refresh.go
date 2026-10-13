package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/authhelper"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"time"
)

// Vertex consumes the same one-hour refresh cache as the original auth
// producer. A failed validity probe runs only the explicitly configured command.
func (m *MainPrint) nativeRefreshGCPCredentials(ctx context.Context, scope *cooperative.Scope) error {
	if !scope.BelongsTo(m.Owner) || m.gcpCredentials == nil {
		return cooperative.ErrRestricted
	}
	proof, err := cooperative.NewNativeCloudHTTP(scope, m.Owner, "gcp_credentials")
	if err != nil {
		return err
	}
	lookup, err := m.gcpCredentials.Begin(func(active context.Context) (json.RawMessage, error) {
		refreshed := false
		receipt, err := m.Owner.SubmitIndependent(active, cooperative.Read, func(callback *cooperative.Scope) error {
			return proof.Run(callback, active, "gcp_credentials", func(next *cooperative.Scope) error {
				settings, err := m.Startup.GetInitialSettings()
				if err != nil {
					return err
				}
				command := settings.String("gcpAuthRefresh")
				if command == "" {
					return nil
				}
				fromProject := false
				for _, source := range []string{"projectSettings", "localSettings"} {
					project, err := m.Startup.GetSettingsForSource(source)
					if err != nil {
						return err
					}
					if project != nil {
						fromProject = fromProject || authhelper.ConfiguredInSource(settings.Get("gcpAuthRefresh"), project.Get("gcpAuthRefresh"))
					}
				}
				if fromProject && m.options.Interactive {
					trusted, err := m.nativeGoalTrust(next)
					if err != nil {
						return err
					}
					if !trusted {
						return nil
					}
				}
				probe, stopProbe := context.WithTimeout(active, 5*time.Second)
				auth, probeErr := m.nativeGoogleAuth(next, m.nativeOpenAIEnvironment())
				if probeErr == nil {
					client, err := auth.GetClient(probe)
					probeErr = err
					if err == nil {
						_, probeErr = client.GetRequestHeaders(probe)
					}
				}
				stopProbe()
				if active.Err() != nil {
					return active.Err()
				}
				if probeErr == nil {
					return nil
				}
				output, failure := m.nativeCloudCommand(active, next, command, 180*time.Second, m.nativeOpenAIEnvironment())
				refreshed = failure == nil && output.ExitCode == 0
				if !refreshed {
					m.environmentHookNotice("Error running gcpAuthRefresh (in settings or ~/.claude.json):", true)
				}
				return nil
			})
		})
		if err != nil {
			return nil, err
		}
		if err = receipt.Wait(); err != nil {
			return nil, err
		}
		return json.Marshal(refreshed)
	})
	if err != nil {
		return err
	}
	future, err := nativeSkillWork(m, scope, func() (json.RawMessage, error) { return lookup.Wait(ctx) })
	if err != nil {
		return err
	}
	_, err = future.Await(scope)
	return err
}
