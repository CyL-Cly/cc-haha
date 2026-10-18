package clibootstrap

import (
	"math"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

// Source currentLimits is process scoped, like rawUtilization. Ordinary Agent
// response producers update the same parent process state, never a new domain.
func (m *MainPrint) nativeSuggestionQuotaRoot() *MainPrint {
	for m.agentChild && m.detachedConstructed && m.options.DetachedAgent != nil && m.options.DetachedAgent.authParent != nil {
		m = m.options.DetachedAgent.authParent
	}
	return m
}

func (m *MainPrint) publishNativeSuggestionQuota(scope *cooperative.Scope, status string) error {
	if m == nil || !scope.BelongsTo(m.Owner) || m.options.Native == nil || !m.options.Native.NativeProcess || m.Controller == nil || !m.Controller.BootstrapOwns(m.Owner) {
		return cooperative.ErrRestricted
	}
	runtime := &m.nativeSuggestionQuotaRoot().nativeTerminalStatusLine
	runtime.mu.Lock()
	if !runtime.closed {
		runtime.quotaStatus = status
	}
	runtime.mu.Unlock()
	return nil
}

func (m *MainPrint) nativeSuggestionSuppression(scope *cooperative.Scope) (string, error) {
	if m == nil || !scope.BelongsTo(m.Owner) || m.options.Native == nil || !m.options.Native.NativeProcess || m.Controller == nil || !m.Controller.BootstrapOwns(m.Owner) {
		return "", cooperative.ErrRestricted
	}
	if m.options.Native.Environment["USER_TYPE"] != "external" {
		return "", nil
	}
	runtime := &m.nativeSuggestionQuotaRoot().nativeTerminalStatusLine
	runtime.mu.Lock()
	status := runtime.quotaStatus
	runtime.mu.Unlock()
	if status != "" && status != "allowed" {
		return "rate_limit", nil
	}
	return "", nil
}

// This is the status projection of computeNewLimitsFromHeaders, including its
// header-based warning and time-relative fallback. No mocks or new requests.
func nativeSuggestionQuotaStatus(headers *provider.BunHeaders, nowMillis float64) (string, error) {
	status, _, err := headers.Get("anthropic-ratelimit-unified-status")
	if err != nil {
		return "", err
	}
	if status == "" {
		status = "allowed"
	}
	if status != "allowed" && status != "allowed_warning" {
		return status, nil
	}
	for _, claim := range []string{"5h", "7d", "overage"} {
		_, present, err := headers.Get("anthropic-ratelimit-unified-" + claim + "-surpassed-threshold")
		if err != nil {
			return "", err
		}
		if present {
			return "allowed_warning", nil
		}
	}
	for _, config := range []struct {
		claim      string
		window     float64
		thresholds [][2]float64
	}{
		{"5h", 5 * 60 * 60, [][2]float64{{0.9, 0.72}}},
		{"7d", 7 * 24 * 60 * 60, [][2]float64{{0.75, 0.6}, {0.5, 0.35}, {0.25, 0.15}}},
	} {
		util, haveUtil, err := headers.Get("anthropic-ratelimit-unified-" + config.claim + "-utilization")
		if err != nil {
			return "", err
		}
		reset, haveReset, err := headers.Get("anthropic-ratelimit-unified-" + config.claim + "-reset")
		if err != nil {
			return "", err
		}
		if !haveUtil || !haveReset {
			continue
		}
		progress := math.Max(0, math.Min(1, (nowMillis/1000-(statusLineHeaderNumber(reset)-config.window))/config.window))
		for _, threshold := range config.thresholds {
			if statusLineHeaderNumber(util) >= threshold[0] && progress <= threshold[1] {
				return "allowed_warning", nil
			}
		}
	}
	return "allowed", nil
}
