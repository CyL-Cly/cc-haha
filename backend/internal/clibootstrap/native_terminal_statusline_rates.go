package clibootstrap

import (
	"context"
	"errors"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
)

func (m *MainPrint) nativeQuotaStatus(scope *cooperative.Scope, headers *provider.BunHeaders, failure error) error {
	if m == nil || !scope.BelongsTo(m.Owner) || m.options.Native == nil || !m.options.Native.NativeProcess || m.Controller == nil || !m.Controller.BootstrapOwns(m.Owner) {
		return cooperative.ErrRestricted
	}
	if failure != nil {
		var wrapped *retrypolicy.CannotRetryError
		if errors.As(failure, &wrapped) {
			failure = wrapped.Original
		}
		var api *provider.SDKStreamError
		if !errors.As(failure, &api) || api.Status == nil || *api.Status != 429 {
			return nil
		}
		headers = api.OriginalHeaders
	}
	if headers == nil && failure == nil {
		return nil
	}
	var subscriber bool
	var err error
	if d := m.options.DetachedAgent; m.agentChild && d != nil && d.sourceMCP != nil {
		// An ordinary Agent uses the parent's bootstrap auth reader, just as
		// its actual HTTP client does. Child headless presentation cannot
		// select a different process-global subscriber domain.
		binding, failure := d.sourceMCP.ordinaryClaudeAuthBinding(scope)
		err = failure
		if err == nil {
			subscriber, err = binding.Subscriber(context.Background())
		}
	} else {
		subscriber, err = m.openAIAuth.IsClaudeSubscriber(context.Background(), scope, !m.options.Interactive)
	}
	if err != nil {
		return err
	}
	if failure != nil && !subscriber {
		return nil
	}
	if headers == nil {
		if failure != nil && subscriber {
			return m.publishNativeSuggestionQuota(scope, "rejected")
		}
		return nil
	}
	value := protocol.NewJSONObject()
	if subscriber {
		for _, pair := range [][2]string{{"five_hour", "5h"}, {"seven_day", "7d"}} {
			util, haveUtil, err := headers.Get("anthropic-ratelimit-unified-" + pair[1] + "-utilization")
			if err != nil {
				return err
			}
			reset, haveReset, err := headers.Get("anthropic-ratelimit-unified-" + pair[1] + "-reset")
			if err != nil {
				return err
			}
			if haveUtil && haveReset {
				value.Set(pair[0], rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "used_percentage", Value: statusLineNumberJSON(statusLineHeaderNumber(util) * 100)}, protocol.JSONField{Name: "resets_at", Value: statusLineNumberJSON(statusLineHeaderNumber(reset))})))
			}
		}
	}
	// Source rawUtilization is process-global, shared by ordinary children.
	// Only the concrete detached factory's existing parent authority selects
	// this shared process state; foreign scopes cannot replace that identity.
	root := m
	for root.agentChild && root.detachedConstructed && root.options.DetachedAgent != nil && root.options.DetachedAgent.authParent != nil {
		root = root.options.DetachedAgent.authParent
	}
	runtime := &root.nativeTerminalStatusLine
	status := "allowed"
	if subscriber {
		status, err = nativeSuggestionQuotaStatus(headers, m.options.Native.NowMillis())
		if err != nil {
			return err
		}
		if failure != nil {
			status = "rejected"
		}
	}
	runtime.mu.Lock()
	if !runtime.closed {
		runtime.rates = value
		runtime.quotaStatus = status
	}
	runtime.mu.Unlock()
	return nil
}

func statusLineHeaderNumber(text string) float64 {
	text = protocol.TrimJavaScriptString(text)
	if text == "" {
		return 0
	}
	if len(text) > 2 && text[0] == '0' {
		base := 0
		switch text[1] {
		case 'x', 'X':
			base = 16
		case 'b', 'B':
			base = 2
		case 'o', 'O':
			base = 8
		}
		if base != 0 {
			n, valid := new(big.Int).SetString(text[2:], base)
			if valid && n.Sign() >= 0 && !strings.ContainsAny(text[2:], "+-_") {
				value, _ := new(big.Float).SetInt(n).Float64()
				return value
			}
			return math.NaN()
		}
	}
	if text == "Infinity" || text == "+Infinity" {
		return math.Inf(1)
	}
	if text == "-Infinity" {
		return math.Inf(-1)
	}
	// Number() requires the entire decimal spelling (not parseFloat prefixes).
	if strings.ContainsAny(text, "_xXbBoOpP") {
		return math.NaN()
	}
	n, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return math.NaN()
	}
	return n
}

func statusLineNumberJSON(n float64) []byte {
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return []byte("null")
	}
	return protocol.StringifyValidatedJavaScriptJSON(rawMain(n))
}
