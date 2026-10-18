package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func terminalAutoUpdaterDisabledReason(env map[string]string, global *protocol.JSONObject) *protocol.JSONObject {
	value := protocol.NewJSONObject()
	if env["NODE_ENV"] == "development" {
		value.Set("type", quoteMain("development"))
		return &value
	}
	for _, name := range []string{"DISABLE_AUTOUPDATER", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"} {
		disabled := env[name] != ""
		if name == "DISABLE_AUTOUPDATER" {
			disabled = nativeEnvTruthy(env[name])
		}
		if disabled {
			value.Set("type", quoteMain("env"))
			value.Set("envVar", quoteMain(name))
			return &value
		}
	}
	if global != nil && string(global.Get("autoUpdates")) == "false" && (global.String("installMethod") != "native" || string(global.Get("autoUpdatesProtectedForNative")) != "true") {
		value.Set("type", quoteMain("config"))
		return &value
	}
	return nil
}

// Config's channel dialog changes the existing installer inputs. The Source
// dialog performs no download: the next actual updater/install command owns it.
func (m *MainPrint) SetTerminalUpdateChannel(ctx context.Context, choice string) error {
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		global, err := m.Global.Get()
		if err != nil {
			return err
		}
		reason := terminalAutoUpdaterDisabledReason(m.nativeOpenAIEnvironment(), global)
		enable := choice == "enable-latest" || choice == "enable-stable"
		if enable {
			if reason == nil || reason.String("type") != "config" {
				return errors.New("Auto-updates cannot be enabled here")
			}
		} else if reason != nil {
			return errors.New("Auto-updates are disabled")
		}
		channel := "stable"
		patch := clisettings.Patch{Object: protocol.NewJSONObject()}
		switch choice {
		case "latest", "enable-latest":
			channel = "latest"
			patch.UndefinedPaths = [][]json.RawMessage{{quoteMain("minimumVersion")}}
		case "enable-stable":
			patch.UndefinedPaths = [][]json.RawMessage{{quoteMain("minimumVersion")}}
		case "stay":
			patch.Object.Set("minimumVersion", quoteMain(m.options.Version))
		case "downgrade": // Source leaves an existing minimumVersion untouched.
		default:
			return errors.New("Unknown auto-update channel choice")
		}
		if enable {
			if err = m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
				next := current.Clone()
				next.Set("autoUpdates", []byte("true"))
				return &next, nil
			}); err != nil {
				return err
			}
		}
		patch.Object.Set("autoUpdatesChannel", quoteMain(channel))
		result, err := m.settings.UpdateEditableSource("userSettings", "", patch)
		if err != nil {
			return err
		}
		if result.Error != nil {
			return result.Error
		}
		return m.refreshTerminalConfigConsumers(scope, false)
	})
	if err == nil {
		err = receipt.Wait()
	}
	return err
}
