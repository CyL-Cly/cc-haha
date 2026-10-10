package clibootstrap

import (
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
	"math"
)

func (m *MainPrint) nativeModelFastDisablePreference() error {
	return m.writePickerSetting(clisettings.Patch{Object: protocol.NewJSONObject(), UndefinedPaths: [][]json.RawMessage{{quoteMain("fastMode")}}})
}
func (m *MainPrint) nativeModelFastNotify(scope *cooperative.Scope, key, text, color string, invalidates ...string) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	if m.options.OnNativeFastModeNotification == nil || !m.options.Interactive || pickerTrue(m.nativeOpenAIEnvironment()["CLAUDE_CODE_REMOTE"]) {
		return nil
	}
	value := protocol.NewJSONObject(protocol.JSONField{Name: "key", Value: quoteMain(key)}, protocol.JSONField{Name: "text", Value: quoteMain(text)}, protocol.JSONField{Name: "color", Value: quoteMain(color)}, protocol.JSONField{Name: "priority", Value: quoteMain("immediate")})
	if len(invalidates) > 0 {
		value.Set("invalidates", rawMain(invalidates))
	}
	return m.options.OnNativeFastModeNotification(value)
}
func (m *MainPrint) nativeModelFastOrgNotice(scope *cooperative.Scope, enabled bool) error {
	if enabled {
		return m.nativeModelFastNotify(scope, "fast-mode-org-changed", "Fast mode is now available · /fast to turn on", "fastMode")
	}
	state, err := m.Controller.TypedModelStateOnOwner(scope)
	if err != nil {
		return err
	}
	if !state.FastMode {
		return nil
	}
	if err = m.Controller.SetTypedFastMode(scope, false); err != nil {
		return err
	}
	return m.nativeModelFastNotify(scope, "fast-mode-org-changed", "Fast mode has been disabled by your organization", "warning")
}
func (m *MainPrint) nativeModelFastEffect(scope *cooperative.Scope, value retrypolicy.FastEffect) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	state, err := m.Controller.TypedModelStateOnOwner(scope)
	if err != nil {
		return err
	}
	switch value.Kind {
	case "org_disabled":
		runtime := m.modelCommandRuntime
		if !runtime.orgEnabled {
			return nil
		}
		runtime.orgEnabled = false
		runtime.orgReason = "preference"
		if err = m.nativeModelFastDisablePreference(); err != nil {
			return err
		}
		if err = m.Host.AcceptInternalFastModeSettings(scope); err != nil {
			return err
		}
		if err = m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
			next := current.Clone()
			next.Set("penguinModeOrgEnabled", []byte("false"))
			return &next, nil
		}); err != nil {
			return err
		}
		return m.nativeModelFastOrgNotice(scope, false)
	case "overage":
		if value.Permanent {
			if err = m.nativeModelFastDisablePreference(); err != nil {
				return err
			}
			if err = m.Host.AcceptInternalFastModeSettings(scope); err != nil {
				return err
			}
			if err = m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
				next := current.Clone()
				next.Set("penguinModeOrgEnabled", []byte("false"))
				return &next, nil
			}); err != nil {
				return err
			}
		}
		if err = m.Controller.SetTypedFastMode(scope, false); err != nil {
			return err
		}
		return m.nativeModelFastNotify(scope, "fast-mode-overage-rejected", value.Message, "warning")
	case "cooldown":
		if !state.FastMode {
			return nil
		}
		duration := nativeFastResetDuration(value.ResetAt - m.options.Native.NowMillis())
		text := "Fast limit reached and temporarily disabled · resets in " + duration
		if value.Reason == "overloaded" {
			text = "Fast mode overloaded and is temporarily unavailable · resets in " + duration
		}
		return m.nativeModelFastNotify(scope, "fast-mode-cooldown-started", text, "warning", "fast-mode-cooldown-expired")
	case "cooldown_expired":
		if !state.FastMode {
			return nil
		}
		return m.nativeModelFastNotify(scope, "fast-mode-cooldown-expired", "Fast limit reset · now using fast mode", "fastMode", "fast-mode-cooldown-started")
	}
	return nil
}
func nativeFastResetDuration(ms float64) string {
	if ms < 60000 {
		if ms < 1 {
			return fmt.Sprintf("%.1fs", ms/1000)
		}
		return fmt.Sprintf("%ds", int64(math.Floor(ms/1000)))
	}
	days := int64(math.Floor(ms / 86400000))
	hours := int64(math.Floor(math.Mod(ms, 86400000) / 3600000))
	minutes := int64(math.Floor(math.Mod(ms, 3600000) / 60000))
	seconds := int64(math.Floor(math.Mod(ms, 60000)/1000 + 0.5))
	if seconds == 60 {
		seconds = 0
		minutes++
	}
	if minutes == 60 {
		minutes = 0
		hours++
	}
	if hours == 24 {
		hours = 0
		days++
	}
	if days > 0 {
		if hours == 0 && minutes == 0 {
			return fmt.Sprintf("%dd", days)
		}
		if minutes == 0 {
			return fmt.Sprintf("%dd %dh", days, hours)
		}
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		if minutes == 0 && seconds == 0 {
			return fmt.Sprintf("%dh", hours)
		}
		if seconds == 0 {
			return fmt.Sprintf("%dh %dm", hours, minutes)
		}
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	}
	if seconds == 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dm %ds", minutes, seconds)
}
