package provider

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// AuxiliaryRetry is the pinned SDK retry boundary, distinct from the main
// Harness's recovery policy. Token counting constructs clients with one retry.
type AuxiliaryRetry struct {
	Now    func() time.Time
	Random func() float64
	Sleep  func(context.Context, float64) error
}

func sdkAuxiliaryRetryable(err error) bool {
	var failure *SDKStreamError
	if errors.As(err, &failure) {
		if directive := failure.Headers.Get("x-should-retry"); directive == "true" {
			return true
		} else if directive == "false" {
			return false
		}
		if failure.Status != nil {
			n := *failure.Status
			return n == 408 || n == 409 || n == 429 || n >= 500
		}
		return failure.Class == "APIConnectionError" || failure.Class == "APIConnectionTimeoutError"
	}
	return err != nil && (err.Error() == "Connection error." || err.Error() == "Request timed out.")
}

func auxiliaryDecimal(value string) float64 {
	value = strings.TrimSpace(value)
	// SDK uses parseFloat, including its numeric-prefix acceptance.
	for end := len(value); end > 0; end-- {
		if n, err := strconv.ParseFloat(value[:end], 64); err == nil {
			return n
		}
	}
	return math.NaN()
}

func auxiliaryDelay(headers http.Header, clock AuxiliaryRetry) float64 {
	delay := math.NaN()
	if value := headers.Get("retry-after-ms"); value != "" {
		delay = auxiliaryDecimal(value)
	}
	if value := headers.Get("retry-after"); value != "" && (math.IsNaN(delay) || delay == 0) {
		if n := auxiliaryDecimal(value); !math.IsNaN(n) {
			delay = n * 1000
		} else if date, err := http.ParseTime(value); err == nil {
			delay = float64(date.UnixMilli() - clock.Now().UnixMilli())
		}
	}
	if math.IsNaN(delay) {
		delay = 500 * (1 - clock.Random()*.25)
	}
	return delay
}

func (client *Anthropic) WithOneSDKRetry(ctx context.Context, clock AuxiliaryRetry, operation func(*Anthropic) (MessageResponse, error)) (MessageResponse, error) {
	if clock.Now == nil || clock.Random == nil || clock.Sleep == nil {
		return MessageResponse{}, errors.New("auxiliary retry requires original clock, RNG and timer")
	}
	copy := *client
	copy.config.DefaultHeaders = client.config.DefaultHeaders.Clone()
	if copy.config.DefaultHeaders == nil {
		copy.config.DefaultHeaders = http.Header{}
	}
	for attempt := 0; ; attempt++ {
		// SDK constructor custom headers override its retry-count default.
		if client.config.AuxiliaryRetryHeader == nil {
			copy.config.DefaultHeaders.Set("x-stainless-retry-count", strconv.Itoa(attempt))
		} else {
			copy.config.DefaultHeaders.Set("x-stainless-retry-count", *client.config.AuxiliaryRetryHeader)
		}
		response, err := operation(&copy)
		if err == nil || attempt == 1 || ctx.Err() != nil || !sdkAuxiliaryRetryable(err) {
			return response, err
		}
		headers := http.Header{}
		var failure *SDKStreamError
		if errors.As(err, &failure) {
			headers = failure.Headers
		}
		if err = clock.Sleep(ctx, auxiliaryDelay(headers, clock)); err != nil {
			return MessageResponse{}, err
		}
	}
}
