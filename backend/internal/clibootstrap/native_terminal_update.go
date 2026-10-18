package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliinstallation"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
	"runtime"
)

// State belongs to the same Owner as the TUI's parsed settings and Agent.
// The caller's terminal lifetime owns the physical HTTP/install worker.
type nativeTerminalUpdateState struct {
	running bool
	latest  string
	status  string
	result  protocol.JSONObject
}

func (m *MainPrint) CheckTerminalUpdate(ctx context.Context, emit func(protocol.JSONObject) error) error {
	var completion *ownerasync.OwnerFuture[struct{}]
	var dispatched *ownerasync.OwnerFuture[cliinstallation.Result]
	var physicalDone chan struct{}
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if m.closed || m.Host == nil {
			return cooperative.ErrClosed
		}
		if !m.options.Interactive {
			return nil
		}
		global, err := m.Global.Get()
		if err != nil {
			return err
		}
		if terminalAutoUpdaterDisabledReason(m.nativeOpenAIEnvironment(), global) != nil {
			return nil
		}
		if m.nativeTerminalUpdate == nil {
			m.nativeTerminalUpdate = &nativeTerminalUpdateState{}
		}
		state := m.nativeTerminalUpdate
		// Channel changes and timer callbacks observe the actual running ref;
		// neither can start a second install while this worker is suspended.
		if state.running {
			return nil
		}
		initial, err := m.Startup.GetInitialSettings()
		if err != nil {
			return err
		}
		channel, minimum := "latest", ""
		if initial != nil {
			if value := initial.String("autoUpdatesChannel"); value != "" {
				channel = value
			}
			minimum = initial.String("minimumVersion")
		}
		tls, err := tlsconfig.NewState(m.options.Native.readFile)
		if err != nil {
			return err
		}
		client, err := webfetch.NewHTTPClient(m.nativeOpenAIEnvironment(), tls)
		if err != nil {
			return err
		}
		installer, err := cliinstallation.New(cliinstallation.Options{Home: m.options.Native.Home, Environment: m.nativeOpenAIEnvironment(), Platform: m.options.Platform, Architecture: runtime.GOARCH, CurrentVersion: m.options.Version, MinimumVersion: minimum, Repository: cliinstallation.RepositoryClient{HTTP: client, APIBase: m.options.Native.Environment["CC_HAHA_CLI_RELEASE_API"]}, Now: m.options.Native.Now})
		if err != nil {
			return err
		}
		state.running = true
		completionOwnsRunning := false
		defer func() {
			if !completionOwnsRunning {
				state.running = false
			}
		}()
		if emit != nil && state.latest != "" {
			if err = emit(mainObj(rawMain(map[string]any{"phase": "checking", "current": m.options.Version, "latest": state.latest, "channel": channel}))); err != nil {
				return err
			}
		}
		physicalDone = make(chan struct{})
		dispatched, err = nativeSkillWork(m, scope, func() (cliinstallation.Result, error) {
			defer close(physicalDone)
			result, err := installer.Install(ctx, channel, false)
			if err == nil && !result.LockFailed {
				installer.CleanupOldVersions(ctx)
			}
			return result, err
		})
		if err != nil {
			physicalDone = nil
			return err
		}
		completion, err = dispatched.Then(scope, func(_ *cooperative.Scope, result cliinstallation.Result, failure error) error {
			defer func() { state.running = false }()
			if failure != nil && ctx.Err() != nil {
				return ctx.Err()
			}
			phase := state.status
			if failure != nil {
				phase = "install_failed"
			} else if !result.LockFailed {
				state.latest = result.Version
				if result.Updated {
					phase = "success"
					if err = m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
						next := current.Clone()
						if next.String("installMethod") != "native" {
							next.Set("installMethod", quoteMain("native"))
							next.Set("autoUpdates", []byte("false"))
							next.Set("autoUpdatesProtectedForNative", []byte("true"))
						}
						return &next, nil
					}); err != nil {
						return err
					}
				}
			}
			state.status = phase
			snapshot := mainObj(rawMain(map[string]any{"phase": phase, "current": m.options.Version, "latest": state.latest, "channel": channel}))
			if failure != nil {
				snapshot.Set("error", quoteMain(failure.Error()))
			}
			state.result = snapshot.Clone()
			if emit != nil {
				return emit(snapshot)
			}
			return nil
		})
		if err != nil {
			return err
		}
		completionOwnsRunning = true
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	if completion != nil {
		// The physical worker keeps the caller context. Observe its admitted
		// completion off-owner so another Owner wait can resume independently.
		// Keep the completion's original error, including emit errors and the
		// original ctx.Err cancellation branch, rather than racing Wait(ctx).
		_, failure := completion.Wait(context.Background())
		if err == nil {
			err = failure
		}
	} else if dispatched != nil {
		// A closing Owner can reject reaction registration after dispatch.
		// Observe that immutable obligation without re-entering an Owner wait.
		_, _ = dispatched.Wait(context.Background())
	}
	if physicalDone != nil && ctx.Err() != nil {
		// Cancellation stops the original HTTP context and joins its physical
		// worker even if the Owner administratively rejected completion.
		<-physicalDone
	}
	return err
}
func (m *MainPrint) TerminalUpdateState(ctx context.Context) (protocol.JSONObject, error) {
	result := protocol.NewJSONObject()
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(*cooperative.Scope) error {
		if m.nativeTerminalUpdate != nil {
			result = m.nativeTerminalUpdate.result.Clone()
			result.Set("running", json.RawMessage(rawMain(m.nativeTerminalUpdate.running)))
		}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}
