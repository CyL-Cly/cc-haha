package provider

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestmapping"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// CreateNativeSideQuery is the beta SDK nonstream consumer. It uses the same
// signed encoder, native owner transport and original JSON/error parser.
func (c *Anthropic) CreateNativeSideQuery(ctx context.Context, params protocol.JSONObject, observe func(*MessagesRequest)) (MessageResponse, error) {
	bytes, e := params.MarshalJSON()
	if e != nil {
		return MessageResponse{}, e
	}
	value, e := requestmapping.ParseValue(bytes)
	if e != nil {
		return MessageResponse{}, e
	}
	prepared, e := PrepareBetaMessages(value, BetaMessageOptions{DeferCCH: c.config.OpenAICodex != nil || c.config.GrokOAuth != nil})
	if e != nil {
		return MessageResponse{}, e
	}
	if observe != nil {
		observe(prepared)
	}
	exchange, cancel, e := c.openPreparedResponse(ctx, prepared, true)
	if e != nil {
		return MessageResponse{}, sdkRequestError(e)
	}
	defer cancel()
	defer exchange.Response.Body.Close()
	if c.config.OwnerBodyIO == nil {
		return MessageResponse{}, errors.New("side query requires actual owner body reader")
	}
	raw, e := c.config.OwnerBodyIO.ReadFetchBody(exchange.Response.Body)
	if e != nil {
		return MessageResponse{}, e
	}
	if exchange.Response.StatusCode < 200 || exchange.Response.StatusCode >= 300 {
		return MessageResponse{}, NewSDKResponseError(&exchange.Response.StatusCode, string(raw), exchange.OriginalHeaders, true)
	}
	return finishModelValidationJSON(raw, exchange.Response.StatusCode)
}
func (c *Anthropic) WithNativeSDKRetries(ctx context.Context, max float64, clock AuxiliaryRetry, operation func(*Anthropic) (MessageResponse, error)) (MessageResponse, error) {
	if clock.Now == nil || clock.Random == nil || clock.Sleep == nil {
		return MessageResponse{}, errors.New("SDK retry requires original clock, RNG and timer")
	}
	copy := *c
	copy.config.DefaultHeaders = c.config.DefaultHeaders.Clone()
	if copy.config.DefaultHeaders == nil {
		copy.config.DefaultHeaders = http.Header{}
	}
	for attempt := 0; ; attempt++ {
		if c.config.AuxiliaryRetryHeader == nil {
			copy.config.DefaultHeaders.Set("x-stainless-retry-count", strconv.Itoa(attempt))
		}
		result, e := operation(&copy)
		if e == nil || !(float64(attempt) < max) || ctx.Err() != nil || !sdkAuxiliaryRetryable(e) {
			return result, e
		}
		headers := http.Header{}
		var failure *SDKStreamError
		if errors.As(e, &failure) {
			headers = failure.Headers
		}
		delay := math.NaN()
		if v := headers.Get("retry-after-ms"); v != "" {
			delay = auxiliaryDecimal(v)
		}
		if v := headers.Get("retry-after"); v != "" && (math.IsNaN(delay) || delay == 0) {
			if n := auxiliaryDecimal(v); !math.IsNaN(n) {
				delay = n * 1000
			} else if d, e := http.ParseTime(v); e == nil {
				delay = float64(d.UnixMilli() - clock.Now().UnixMilli())
			}
		}
		if math.IsNaN(delay) {
			delay = math.Min(500*math.Pow(2, float64(attempt)), 8000) * (1 - clock.Random()*.25)
		}
		if e = clock.Sleep(ctx, delay); e != nil {
			return MessageResponse{}, e
		}
	}
}
func ParseSDKRetryCount(s string) float64 {
	s = strings.TrimSpace(s)
	for n := len(s); n > 0; n-- {
		if v, e := strconv.ParseInt(s[:n], 10, 64); e == nil {
			return float64(v)
		}
	}
	return math.NaN()
}
