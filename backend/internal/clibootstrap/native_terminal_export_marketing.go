package clibootstrap

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sandboxruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func terminalExportNumber(value json.RawMessage) float64 {
	var number float64
	_ = json.Unmarshal(value, &number)
	return number
}

func terminalExportCredit(amount json.RawMessage, currency string, grant bool) string {
	if len(amount) == 0 || string(amount) == "null" || currency == "" {
		return ""
	}
	var cents float64
	if json.Unmarshal(amount, &cents) != nil {
		return ""
	}
	symbol := currency + " "
	if grant {
		if !strings.EqualFold(currency, "USD") {
			return ""
		}
		symbol = "$"
	} else if known, ok := map[string]string{"USD": "$", "EUR": "€", "GBP": "£", "BRL": "R$", "CAD": "CA$", "AUD": "A$", "NZD": "NZ$", "SGD": "S$"}[currency]; ok {
		symbol = known
	}
	dollars, precision := cents/100, 2
	if math.Trunc(dollars) == dollars {
		precision = 0
	}
	return symbol + strconv.FormatFloat(dollars, 'f', precision, 64)
}

// These eligibility getters consume the real private cached org entries; a
// static export never fetches referral data and never invents subscription state.
func (m *MainPrint) populateTerminalExportMarketing(global *protocol.JSONObject, subscriber bool, subscription string, value *tui.ConversationExportPresentation) error {
	if global != nil {
		account, _ := global.Object("oauthAccount")
		org := account.String("organizationUuid")
		if cache, ok := global.Object("passesEligibilityCache"); ok && org != "" && subscriber && subscription == "max" {
			if entry, ok := cache.Object(org); ok && string(entry.Get("eligible")) == "true" {
				remaining := terminalExportNumber(entry.Get("remaining_passes"))
				value.ResetPasses = remaining > 0 && remaining > terminalExportNumber(global.Get("passesLastSeenRemaining"))
				value.PassesRemaining = remaining
				value.GuestPasses = value.ResetPasses || terminalExportNumber(global.Get("passesUpsellSeenCount")) < 3 && string(global.Get("hasVisitedPasses")) != "true"
				value.GuestPassSubtitle = "Share Claude Code with friends"
				if reward, ok := entry.Object("referrer_reward"); ok {
					value.GuestPassReward = true
					value.GuestPassSubtitle = "Share Claude Code and earn " + terminalExportCredit(reward.Get("amount_minor_units"), reward.String("currency"), false) + " of extra usage"
				}
			}
		}
		if cache, ok := global.Object("overageCreditGrantCache"); ok && org != "" {
			if entry, ok := cache.Object(org); ok && m.options.Native.NowMillis()-terminalExportNumber(entry.Get("timestamp")) <= 60*60*1000 {
				if info, ok := entry.Object("info"); ok && string(info.Get("available")) == "true" && string(info.Get("granted")) != "true" {
					value.OverageCreditAmount = terminalExportCredit(info.Get("amount_minor_units"), info.String("currency"), true)
					value.OverageCreditUpsell = value.OverageCreditAmount != "" && terminalExportNumber(global.Get("overageCreditUpsellSeenCount")) < 3 && string(global.Get("hasVisitedExtraUsage")) != "true"
				}
			}
		}
	}
	if state := m.sandbox; state != nil {
		settings, err := m.Startup.GetInitialSettings()
		if err != nil {
			return err
		}
		availability := sandboxruntime.ResolveAvailability(sandboxruntime.AvailabilityInput{Settings: settings.Clone(), InitialSettings: state.initial, Platform: state.platform, Supported: state.supported, DependencyErrors: state.dependencies})
		value.Sandboxed = availability.Enabled && availability.Reason == ""
	}
	return nil
}
